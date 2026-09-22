// Package deobfuscator builds and applies response mappings from reports.
package deobfuscator

import (
	"fmt"
	"sort"

	"github.com/openshift/must-gather-clean/pkg/obfuscator"
	"github.com/openshift/must-gather-clean/pkg/reporting"
)

// Rule describes one unambiguous obfuscation that can be safely reversed.
// Original is the canonical value used by the obfuscator. For example, IP
// address formatting variants are all restored to their canonical form.
type Rule struct {
	Type       string
	Original   string
	Obfuscated string
}

type AmbiguousRule struct {
	Type       string
	Obfuscated string
	Originals  []string
}

type UnsupportedRule struct {
	Type   string
	Reason string
}

// Mapping is the in-memory reverse lookup built from a report.
type Mapping struct {
	RunID       string
	Rules       []Rule
	Ambiguous   []AmbiguousRule
	Unsupported []UnsupportedRule
}

type candidate struct {
	typ      string
	original string
}

// NewMappingFromReport builds the response lookup from the existing report.
// The report contains both the replacement token and the canonical value, so
// occurrence spellings are intentionally ignored. If a token occurs with
// multiple canonical values, it is left unchanged rather than guessed.
func NewMappingFromReport(report *reporting.Report) (*Mapping, error) {
	if report == nil {
		return nil, fmt.Errorf("cannot build deobfuscation mapping from a nil report")
	}

	result := &Mapping{RunID: report.RunID}
	byObfuscated := map[string][]candidate{}

	for index, replacements := range report.Replacements {
		if index >= len(report.Config.Obfuscate) {
			result.Unsupported = append(result.Unsupported, UnsupportedRule{
				Type:   "report",
				Reason: fmt.Sprintf("replacement group %d has no matching obfuscator configuration", index),
			})
			continue
		}

		configured := report.Config.Obfuscate[index]
		if !obfuscator.IsReversibleConfiguration(configured) {
			result.Unsupported = append(result.Unsupported, UnsupportedRule{
				Type:   string(configured.Type),
				Reason: obfuscator.ReversibleUnsupportedReason(configured),
			})
			continue
		}

		for _, replacement := range replacements {
			if replacement.Canonical == "" || replacement.ReplacedWith == "" || replacement.Canonical == replacement.ReplacedWith {
				continue
			}
			byObfuscated[replacement.ReplacedWith] = append(byObfuscated[replacement.ReplacedWith], candidate{
				typ:      string(configured.Type),
				original: replacement.Canonical,
			})
		}
	}

	for obfuscated, candidates := range byObfuscated {
		unique := map[string]struct{}{}
		typ := candidates[0].typ
		for _, candidate := range candidates {
			unique[candidate.original] = struct{}{}
		}

		if len(unique) != 1 {
			originals := make([]string, 0, len(unique))
			for original := range unique {
				originals = append(originals, original)
			}
			sort.Strings(originals)
			result.Ambiguous = append(result.Ambiguous, AmbiguousRule{
				Type:       typ,
				Obfuscated: obfuscated,
				Originals:  originals,
			})
			continue
		}

		for original := range unique {
			result.Rules = append(result.Rules, Rule{Type: typ, Original: original, Obfuscated: obfuscated})
		}
	}

	// A later obfuscator may have consumed an earlier replacement token. A
	// flat reverse lookup would then restore only to the intermediate token,
	// so leave the affected mappings unchanged rather than produce a plausible
	// but incomplete result.
	unsafeRules := map[int]struct{}{}
	for i, rule := range result.Rules {
		for j, other := range result.Rules {
			if i != j && (rule.Obfuscated == other.Original || other.Obfuscated == rule.Original) {
				unsafeRules[i] = struct{}{}
				unsafeRules[j] = struct{}{}
			}
		}
	}
	if len(unsafeRules) > 0 {
		result.Unsupported = append(result.Unsupported, UnsupportedRule{
			Type:   "chained obfuscations",
			Reason: "an obfuscation output is also an input of another obfuscation; affected mappings were excluded",
		})
		filteredRules := make([]Rule, 0, len(result.Rules)-len(unsafeRules))
		for index, rule := range result.Rules {
			if _, unsafe := unsafeRules[index]; !unsafe {
				filteredRules = append(filteredRules, rule)
			}
		}
		result.Rules = filteredRules
	}

	sort.Slice(result.Rules, func(i, j int) bool {
		if len(result.Rules[i].Obfuscated) != len(result.Rules[j].Obfuscated) {
			return len(result.Rules[i].Obfuscated) > len(result.Rules[j].Obfuscated)
		}
		return result.Rules[i].Obfuscated < result.Rules[j].Obfuscated
	})
	sort.Slice(result.Ambiguous, func(i, j int) bool {
		return result.Ambiguous[i].Obfuscated < result.Ambiguous[j].Obfuscated
	})
	sort.Slice(result.Unsupported, func(i, j int) bool {
		if result.Unsupported[i].Type != result.Unsupported[j].Type {
			return result.Unsupported[i].Type < result.Unsupported[j].Type
		}
		return result.Unsupported[i].Reason < result.Unsupported[j].Reason
	})

	return result, nil
}
