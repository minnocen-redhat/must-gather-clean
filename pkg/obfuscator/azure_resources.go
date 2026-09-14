package obfuscator

import (
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/openshift/must-gather-clean/pkg/schema"
	"k8s.io/klog/v2"
	"k8s.io/utils/set"
)

const (
	staticAzureSubscriptionReplacement    = "obfuscated-subscription"
	staticAzureResourceGroupReplacement   = "obfuscated-resourcegroup"
	staticAzureResourceNameReplacement    = "obfuscated-resource-name"
	staticAzureSubresourceNameReplacement = "obfuscated-subresource-name"
)

var (
	//     /subscriptions/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx/resourceGroups/myResourceGroup/providers/Microsoft.Network/virtualNetworks/myVNet/subnets/mySubnet
	// Azure resource path pattern
	azureSubscriptionPattern  = `(?i)/subscriptions/([^(/\s'")]+)`
	azureResourceGroupPattern = `(?i)/resource[Gg]roups/([^(/\s'")]+)`
	azureResourcePattern      = `(?i)/providers/([^/]+)/([^/]+)/([^(/\s'")]+)`
	azureSubresourcePattern   = `(?i)` + azureResourcePattern + `/([^/]+)/([^(/\s'")]+)`
	azureNodePoolPattern      = `(?i)Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools/([^(/\s'")]+)`
)

type partialRegexReplacer struct {
	pattern string
	regex   *regexp.Regexp
	repl    func(string) string

	lock                  sync.RWMutex
	generator             *petNameReplacementGenerator
	canonicalReplacements set.Set[string]
}

func newPartialRegexReplacer(pattern string, generator *petNameReplacementGenerator, replaceFn func(original string, matches []string, replacer *partialRegexReplacer) string) *partialRegexReplacer {
	currRegex := regexp.MustCompile(pattern)
	ret := &partialRegexReplacer{
		pattern: pattern,
		regex:   currRegex,

		generator:             generator,
		canonicalReplacements: set.Set[string]{},
	}
	ret.repl = func(s string) string {
		matches := currRegex.FindStringSubmatch(s)
		if matches == nil {
			return s
		}

		return replaceFn(s, matches, ret)
	}

	return ret
}

func (t *partialRegexReplacer) generateReplacement(canonical, original string, count uint, tracker ReplacementTracker) string {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.canonicalReplacements.Insert(canonical)
	return t.generator.generateReplacement(canonical, original, count, tracker)
}

type azureResourceObfuscator struct {
	ReplacementTracker

	// we always check all of them because more than one can match a line, but they are evaluated in order because some are more specific than others.
	orderedPartialRegexReplacers []*partialRegexReplacer
}

type azureProtectedToken struct {
	token       string
	placeholder string
}

func (o *azureResourceObfuscator) Path(s string) string {
	return o.replace(s)
}

func (o *azureResourceObfuscator) Contents(s string) string {
	return o.replace(s)
}

func (o *azureResourceObfuscator) ReversibleReport() []ReversibleReplacement {
	return reversibleReportFor(o.ReplacementTracker)
}

func (o *azureResourceObfuscator) replace(s string) string {
	patternReplacedString := s

	for _, currPartialRegexReplacer := range o.orderedPartialRegexReplacers {
		if !currPartialRegexReplacer.regex.MatchString(s) {
			continue
		}

		patternReplacedString = currPartialRegexReplacer.regex.ReplaceAllStringFunc(patternReplacedString, currPartialRegexReplacer.repl)
	}

	// In the reversible workflow, generated tokens are run-scoped and can
	// contain canonical Azure names in their human-readable suffix (for
	// example, a token generated for "resource" starts with "resource-").
	// Protect those tokens before applying the remaining canonical replacements
	// so that the global pass cannot rewrite a token that is already obfuscated.
	var protectedTokens []azureProtectedToken
	if tokenPrefix := replacementTokenPrefix(o.ReplacementTracker); tokenPrefix != "" {
		protectedTokens = o.protectGeneratedTokens(patternReplacedString, tokenPrefix)
		for _, protected := range protectedTokens {
			patternReplacedString = strings.ReplaceAll(patternReplacedString, protected.token, protected.placeholder)
		}
	}

	// at this point we have found all new substitutions, but we must still replace all previously discovered substitutions in the remaining string
	// we do these in reverse order because it appears to substitute slightly better to replace subscriptions and resourcegroups before resource names.
	canonicalToReplacer := map[string]*partialRegexReplacer{}
	for _, currGenerator := range o.orderedPartialRegexReplacers {
		currGenerator.lock.RLock()
		canonicalReplacements := currGenerator.canonicalReplacements.UnsortedList()
		currGenerator.lock.RUnlock()

		for _, canonicalStringToReplace := range canonicalReplacements {
			if strings.Contains(patternReplacedString, canonicalStringToReplace) {
				if len(canonicalStringToReplace) < 5 {
					klog.Warningf("Azure resource obfuscator will skip '%s' because it's too short", canonicalStringToReplace)
					// we don't want to replace the canonical string if it's too short, because it's probably a trivial string like "0"
					continue
				}
				canonicalToReplacer[canonicalStringToReplace] = currGenerator
				continue
			}
		}
	}

	// now we have all strings.  order by longest so that we replace as few times as possible.
	// Sort by length (descending) and alphabetically
	canonicalStrings := set.KeySet(canonicalToReplacer)
	canonicalStringsList := canonicalStrings.UnsortedList()
	sort.Slice(canonicalStringsList, func(i, j int) bool {
		if len(canonicalStringsList[i]) != len(canonicalStringsList[j]) {
			return len(canonicalStringsList[i]) > len(canonicalStringsList[j])
		}
		return canonicalStringsList[i] < canonicalStringsList[j]
	})

	// now do the replace
	for _, canonicalStringToReplace := range canonicalStringsList {
		currGenerator := canonicalToReplacer[canonicalStringToReplace]
		replacementString := currGenerator.generator.generateReplacement(canonicalStringToReplace, canonicalStringToReplace, 1, o.ReplacementTracker)
		patternReplacedString = strings.ReplaceAll(patternReplacedString, canonicalStringToReplace, replacementString)
		if tokenPrefix := replacementTokenPrefix(o.ReplacementTracker); tokenPrefix != "" && strings.HasPrefix(replacementString, tokenPrefix) {
			patternReplacedString = protectAzureToken(patternReplacedString, replacementString, &protectedTokens)
		}
	}

	for _, protected := range protectedTokens {
		patternReplacedString = strings.ReplaceAll(patternReplacedString, protected.placeholder, protected.token)
	}

	return patternReplacedString
}

func protectAzureToken(value, token string, protected *[]azureProtectedToken) string {
	if token == "" || !strings.Contains(value, token) {
		return value
	}
	placeholderIndex := len(*protected)
	placeholder := azureTokenPlaceholder(placeholderIndex)
	for strings.Contains(value, placeholder) {
		placeholderIndex++
		placeholder = azureTokenPlaceholder(placeholderIndex)
	}
	*protected = append(*protected, azureProtectedToken{token: token, placeholder: placeholder})
	return strings.ReplaceAll(value, token, placeholder)
}

// replacementTokenPrefix returns the optional run-scoped token prefix without
// changing the ReplacementTracker interface. Legacy trackers do not expose a
// prefix and therefore retain the historical Azure replacement behavior.
func replacementTokenPrefix(tracker ReplacementTracker) string {
	prefixProvider, ok := tracker.(interface{ runTokenPrefix() string })
	if !ok {
		return ""
	}
	return prefixProvider.runTokenPrefix()
}

// protectGeneratedTokens finds tokens generated by the Azure obfuscator in
// the current call. The longest tokens are protected first so one generated
// token cannot be mistaken for a substring of another generated token.
func (o *azureResourceObfuscator) protectGeneratedTokens(value, tokenPrefix string) []azureProtectedToken {
	replacementsByCanonical := make(map[string]string)
	for _, replacement := range o.ReplacementTracker.Report().Replacements {
		if strings.HasPrefix(replacement.ReplacedWith, tokenPrefix) {
			replacementsByCanonical[replacement.Canonical] = replacement.ReplacedWith
		}
	}

	tokens := make(map[string]struct{})
	for _, currGenerator := range o.orderedPartialRegexReplacers {
		currGenerator.lock.RLock()
		for _, canonical := range currGenerator.canonicalReplacements.UnsortedList() {
			if token, ok := replacementsByCanonical[canonical]; ok {
				tokens[token] = struct{}{}
			}
		}
		currGenerator.lock.RUnlock()
	}

	tokenList := make([]string, 0, len(tokens))
	for token := range tokens {
		tokenList = append(tokenList, token)
	}
	sort.Slice(tokenList, func(i, j int) bool {
		if len(tokenList[i]) != len(tokenList[j]) {
			return len(tokenList[i]) > len(tokenList[j])
		}
		return tokenList[i] < tokenList[j]
	})

	protected := make([]azureProtectedToken, 0, len(tokenList))
	placeholderIndex := 0
	for _, token := range tokenList {
		if !strings.Contains(value, token) {
			continue
		}
		placeholder := azureTokenPlaceholder(placeholderIndex)
		for strings.Contains(value, placeholder) {
			placeholderIndex++
			placeholder = azureTokenPlaceholder(placeholderIndex)
		}
		placeholderIndex++
		protected = append(protected, azureProtectedToken{token: token, placeholder: placeholder})
	}
	return protected
}

// azureTokenPlaceholder is deliberately made from non-printable bytes. A
// printable placeholder could itself contain a canonical Azure name (for
// example, "resource") and be rewritten by the same global pass it protects.
func azureTokenPlaceholder(index int) string {
	value := uint64(index)
	return string([]byte{
		0x00,
		0x01,
		byte(value >> 56),
		byte(value >> 48),
		byte(value >> 40),
		byte(value >> 32),
		byte(value >> 24),
		byte(value >> 16),
		byte(value >> 8),
		byte(value),
		0x02,
		0x00,
	})
}

func NewAzureResourceObfuscator(replacementType schema.ObfuscateReplacementType, tracker ReplacementTracker, desiredSeed *int) (ReportingObfuscator, error) {
	var randSource RandomSource
	randSource = cryptoRandSource{}
	if desiredSeed != nil {
		randSource = rand.New(rand.NewSource(int64(*desiredSeed)))
	}

	if replacementType != schema.ObfuscateReplacementTypeStatic && replacementType != schema.ObfuscateReplacementTypeConsistent {
		return nil, fmt.Errorf("unsupported replacement type: %s", replacementType)
	}

	// create a shared petname generator with a fixed seed for reproducibility
	petNameGen := NewPetNameGenerator("-", randSource)

	// shared by a couple regexes
	resourceNameGen := newPetNameReplacementGenerator("resource", staticAzureResourceNameReplacement, petNameGen, replacementType)

	orderedPartialRegexReplacers := []*partialRegexReplacer{
		newPartialRegexReplacer(
			azureSubresourcePattern,
			newPetNameReplacementGenerator("subresource", staticAzureSubresourceNameReplacement, petNameGen, replacementType),
			func(original string, matches []string, replacer *partialRegexReplacer) string {
				if len(matches) < 6 {
					return original
				}

				providerName := matches[1]
				resourceType := matches[2]
				resourceName := matches[3]
				subresourceType := matches[4]
				subresourceNameReplacement := replacer.generateReplacement(matches[5], matches[5], 1, tracker)
				return fmt.Sprintf("/providers/%s/%s/%s/%s/%s", providerName, resourceType, resourceName, subresourceType, subresourceNameReplacement)
			}),
		newPartialRegexReplacer(
			azureResourcePattern,
			resourceNameGen,
			func(original string, matches []string, replacer *partialRegexReplacer) string {
				if len(matches) < 4 {
					return original
				}

				providerName := matches[1]
				resourceType := matches[2]
				resourceNameReplacement := replacer.generateReplacement(matches[3], matches[3], 1, tracker)
				return fmt.Sprintf("/providers/%s/%s/%s", providerName, resourceType, resourceNameReplacement)
			}),
		newPartialRegexReplacer(
			azureResourceGroupPattern,
			newPetNameReplacementGenerator("resourcegroup", staticAzureResourceGroupReplacement, petNameGen, replacementType),
			func(original string, matches []string, replacer *partialRegexReplacer) string {
				if len(matches) < 2 {
					return original
				}

				resourceGroupNameReplacement := replacer.generateReplacement(matches[1], matches[1], 1, tracker)
				return fmt.Sprintf("/resourcegroups/%s", resourceGroupNameReplacement)
			}),
		newPartialRegexReplacer(
			azureSubscriptionPattern,
			newPetNameReplacementGenerator("subscription", staticAzureSubscriptionReplacement, petNameGen, replacementType),
			func(original string, matches []string, replacer *partialRegexReplacer) string {
				if len(matches) < 2 {
					return original
				}

				subscriptionReplacement := replacer.generateReplacement(matches[1], matches[1], 1, tracker)
				return fmt.Sprintf("/subscriptions/%s", subscriptionReplacement)
			}),
		newPartialRegexReplacer(
			azureNodePoolPattern,
			resourceNameGen,
			func(original string, matches []string, replacer *partialRegexReplacer) string {
				if len(matches) < 2 {
					return original
				}

				nodePoolReplacement := replacer.generateReplacement(matches[1], matches[1], 1, tracker)
				return fmt.Sprintf("Microsoft.RedHatOpenShift/hcpOpenShiftClusters/nodePools/%s", nodePoolReplacement)
			}),
	}

	return &azureResourceObfuscator{
		ReplacementTracker:           tracker,
		orderedPartialRegexReplacers: orderedPartialRegexReplacers,
	}, nil
}
