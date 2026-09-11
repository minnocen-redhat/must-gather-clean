package obfuscator

import (
	"fmt"

	"github.com/openshift/must-gather-clean/pkg/schema"
)

// BuildOptions contains run-scoped state needed by obfuscators without making
// the obfuscator package depend on the deobfuscation workflow.
type BuildOptions struct {
	TokenPrefix string
	RandSeed    *int
}

// ConfiguredObfuscator is the result of building one schema entry. Prescan is
// non-nil only for obfuscators that need to discover values before the final
// cleaning pass. It deliberately shares state with Final.
type ConfiguredObfuscator struct {
	Type       string
	Final      ReportingObfuscator
	Prescan    ReportingObfuscator
	Reversible bool
}

// IsReversibleConfiguration is the single capability predicate used by the
// factory and the deobfuscation capability checks.
func IsReversibleConfiguration(value schema.Obfuscate) bool {
	switch value.Type {
	case schema.ObfuscateTypeIP,
		schema.ObfuscateTypeMAC,
		schema.ObfuscateTypeDomain,
		schema.ObfuscateTypeAzureResources:
		return value.ReplacementType == schema.ObfuscateReplacementTypeConsistent
	default:
		return false
	}
}

func ReversibleUnsupportedReason(value schema.Obfuscate) string {
	if IsReversibleConfiguration(value) {
		return ""
	}
	switch value.Type {
	case schema.ObfuscateTypeRegex:
		return "regex replacements are static and do not preserve a reversible mapping"
	case schema.ObfuscateTypeIP, schema.ObfuscateTypeMAC, schema.ObfuscateTypeDomain, schema.ObfuscateTypeAzureResources:
		if value.ReplacementType == "" || value.ReplacementType == schema.ObfuscateReplacementTypeStatic {
			return "static replacements do not preserve a reversible mapping"
		}
	case schema.ObfuscateTypeExact:
		return "exact replacements are static and do not preserve a reversible mapping"
	}
	return "obfuscator type is not supported by the reversible workflow"
}

// BuildConfiguredObfuscator creates the final obfuscator and any preparation
// obfuscator required by the configured type. Keeping this registry in the
// obfuscator package prevents the CLI and capability code from maintaining
// separate type switches.
func BuildConfiguredObfuscator(value schema.Obfuscate, options BuildOptions) (ConfiguredObfuscator, error) {
	tracker := NewSimpleTrackerMap(value.Replacement)
	if options.TokenPrefix != "" && IsReversibleConfiguration(value) {
		tracker = NewSimpleTrackerWithTokenPrefix(options.TokenPrefix)
	}

	var (
		built ReportingObfuscator
		err   error
	)
	switch value.Type {
	case schema.ObfuscateTypeKeywords:
		built = NewKeywordsObfuscator(value.Replacement)
	case schema.ObfuscateTypeMAC:
		built, err = NewMacAddressObfuscator(value.ReplacementType, tracker)
	case schema.ObfuscateTypeRegex:
		if value.Regex == nil {
			return ConfiguredObfuscator{}, fmt.Errorf("obfuscator type Regex requires a regex")
		}
		built, err = NewRegexObfuscator(*value.Regex, tracker)
	case schema.ObfuscateTypeDomain:
		built, err = NewDomainObfuscator(value.DomainNames, value.ReplacementType, tracker)
	case schema.ObfuscateTypeAzureResources:
		built, err = NewAzureResourceObfuscator(value.ReplacementType, tracker, options.RandSeed)
	case schema.ObfuscateTypeExact:
		built = NewExactReplacementObfuscator(value.ExactReplacements, tracker)
	case schema.ObfuscateTypeIP:
		built, err = NewIPObfuscator(value.ReplacementType, tracker)
	default:
		return ConfiguredObfuscator{}, fmt.Errorf("unknown obfuscator type %s", value.Type)
	}
	if err != nil {
		return ConfiguredObfuscator{}, err
	}
	reversible := IsReversibleConfiguration(value)
	if reversible {
		if _, ok := built.(ReversibleReporter); !ok {
			return ConfiguredObfuscator{}, fmt.Errorf("obfuscator type %s is marked reversible but does not provide a reversible ledger", value.Type)
		}
	}

	configured := ConfiguredObfuscator{
		Type:       string(value.Type),
		Final:      NewTargetObfuscator(value.Target, built),
		Reversible: reversible,
	}
	if value.Type == schema.ObfuscateTypeAzureResources {
		// Azure discovery must see the whole input, while Final still respects
		// the configured target. Both wrappers share the same tracker. Keeping
		// the target on the prescan prevents values from an unselected target
		// (for example file contents when only paths are selected) from entering
		// the reversible ledger.
		configured.Prescan = NewTargetObfuscator(value.Target, built)
	}
	return configured, nil
}
