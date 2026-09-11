package deobfuscator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/openshift/must-gather-clean/pkg/obfuscator"
	"github.com/openshift/must-gather-clean/pkg/schema"
	"gopkg.in/yaml.v3"
)

const CurrentMapVersion = 1

// Rule describes one unambiguous obfuscation that can be safely reversed.
// Original is the canonical value used by the obfuscator. For example, IP
// address formatting variants are all restored to their canonical form.
type Rule struct {
	Type       string `yaml:"type"`
	Original   string `yaml:"original"`
	Obfuscated string `yaml:"obfuscated"`
}

type AmbiguousRule struct {
	Type       string   `yaml:"type"`
	Obfuscated string   `yaml:"obfuscated"`
	Originals  []string `yaml:"originals"`
}

type UnsupportedRule struct {
	Type   string `yaml:"type"`
	Reason string `yaml:"reason"`
}

// Map is deliberately separate from report.yaml. It is a private recovery
// artifact and must not be included in the cleaned must-gather.
type Map struct {
	Version     int               `yaml:"version"`
	RunID       string            `yaml:"runId"`
	Rules       []Rule            `yaml:"rules,omitempty"`
	Ambiguous   []AmbiguousRule   `yaml:"ambiguous,omitempty"`
	Unsupported []UnsupportedRule `yaml:"unsupported,omitempty"`
}

type candidate struct {
	typ      string
	original string
}

// NewMap builds a reversible map from the legacy public reports. New cleaning
// runs should use NewMapFromLedger instead.
func NewMap(config schema.SchemaJsonConfig, reports []obfuscator.ReplacementReport) (*Map, error) {
	runID, err := NewRunID()
	if err != nil {
		return nil, err
	}
	ledger := make([][]obfuscator.ReversibleReplacement, len(reports))
	for i, report := range reports {
		ledger[i] = make([]obfuscator.ReversibleReplacement, 0, len(report.Replacements))
		for _, replacement := range report.Replacements {
			ledger[i] = append(ledger[i], obfuscator.ReversibleReplacement{
				Canonical:    replacement.Canonical,
				ReplacedWith: replacement.ReplacedWith,
				Counter:      replacement.Counter,
			})
		}
	}
	return NewMapFromLedger(config, ledger, runID)
}

// NewMapFromLedger builds a private map from the reversible ledger emitted by
// supported obfuscators. Unsupported configured types are recorded as a
// limitation instead of being silently inferred from the public report.
func NewMapFromLedger(config schema.SchemaJsonConfig, reports [][]obfuscator.ReversibleReplacement, runID string) (*Map, error) {
	if runID == "" {
		return nil, fmt.Errorf("deobfuscation map run id is empty")
	}

	result := &Map{Version: CurrentMapVersion, RunID: runID}
	byObfuscated := map[string][]candidate{}

	for i, cfg := range config.Obfuscate {
		if i >= len(reports) {
			result.Unsupported = append(result.Unsupported, UnsupportedRule{
				Type:   string(cfg.Type),
				Reason: "no obfuscator report was available",
			})
			continue
		}

		if reason, unsupported := unsupportedObfuscation(cfg); unsupported {
			if len(reports[i]) == 0 {
				continue
			}
			result.Unsupported = append(result.Unsupported, UnsupportedRule{
				Type:   string(cfg.Type),
				Reason: reason,
			})
			continue
		}

		if cfg.Type == schema.ObfuscateTypeExact && exactReplacementChains(cfg.ExactReplacements) {
			result.Unsupported = append(result.Unsupported, UnsupportedRule{
				Type:   string(cfg.Type),
				Reason: "exact replacements contain a chain and are not safely reversible",
			})
			continue
		}

		for _, replacement := range reports[i] {
			if replacement.Canonical == "" || replacement.ReplacedWith == "" || replacement.ReplacedWith == replacement.Canonical {
				continue
			}
			if reversibleReplacementCount(replacement) == 0 {
				continue
			}

			byObfuscated[replacement.ReplacedWith] = append(byObfuscated[replacement.ReplacedWith], candidate{
				typ:      string(cfg.Type),
				original: replacement.Canonical,
			})
		}

	}

	for obfuscated, candidates := range byObfuscated {
		unique := map[string]struct{}{}
		typ := candidates[0].typ
		for _, c := range candidates {
			unique[c.original] = struct{}{}
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

	// A replacement from one obfuscator can become the input of another one.
	// The current map format intentionally uses one-pass replacement, so such
	// overlapping stages cannot be safely restored without stage metadata.
	unsafeRules := map[int]struct{}{}
	for i, rule := range result.Rules {
		for j, other := range result.Rules {
			if i != j && rule.Original == other.Obfuscated {
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
		for i, rule := range result.Rules {
			if _, unsafe := unsafeRules[i]; !unsafe {
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

func replacementCount(replacement obfuscator.Replacement) uint {
	var count uint
	for _, occurrenceCount := range replacement.Counter {
		count += occurrenceCount
	}
	return count
}

func reversibleReplacementCount(replacement obfuscator.ReversibleReplacement) uint {
	var count uint
	for _, occurrenceCount := range replacement.Counter {
		count += occurrenceCount
	}
	return count
}

func hasReplacements(report obfuscator.ReplacementReport) bool {
	for _, replacement := range report.Replacements {
		if replacement.Canonical != "" && replacement.ReplacedWith != "" && replacement.ReplacedWith != replacement.Canonical && replacementCount(replacement) > 0 {
			return true
		}
	}
	return false
}

func unsupportedObfuscation(cfg schema.Obfuscate) (string, bool) {
	switch cfg.Type {
	case schema.ObfuscateTypeRegex:
		return "regex replacements are static and do not preserve a reversible mapping", true
	case schema.ObfuscateTypeIP, schema.ObfuscateTypeMAC, schema.ObfuscateTypeDomain, schema.ObfuscateTypeAzureResources, schema.ObfuscateTypeHostname:
		if cfg.ReplacementType == "" || cfg.ReplacementType == schema.ObfuscateReplacementTypeStatic {
			return "static replacements do not preserve a reversible mapping", true
		}
	}
	return "", false
}

func exactReplacementChains(replacements []schema.ObfuscateExactReplacementsElem) bool {
	originals := map[string]struct{}{}
	for _, replacement := range replacements {
		originals[replacement.Original] = struct{}{}
	}
	for _, replacement := range replacements {
		if replacement.Original != replacement.Replacement {
			if _, found := originals[replacement.Replacement]; found {
				return true
			}
		}
	}
	return false
}

func NewRunID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func NewRunSecret() (string, error) {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func (m *Map) Write(path string) error {
	if m == nil {
		return fmt.Errorf("cannot write a nil deobfuscation map")
	}

	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("failed to encode deobfuscation map: %w", err)
	}

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fmt.Errorf("failed to create deobfuscation map directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".deobfuscation-map-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary deobfuscation map: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("failed to secure temporary deobfuscation map: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("failed to write temporary deobfuscation map: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("failed to sync temporary deobfuscation map: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("failed to close temporary deobfuscation map: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("failed to publish deobfuscation map %s: %w", path, err)
	}
	return nil
}

func ReadMap(path string) (*Map, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read deobfuscation map %s: %w", path, err)
	}

	result := &Map{}
	if err := yaml.Unmarshal(data, result); err != nil {
		return nil, fmt.Errorf("failed to decode deobfuscation map %s: %w", path, err)
	}
	if result.Version != CurrentMapVersion {
		return nil, fmt.Errorf("unsupported deobfuscation map version %d, expected %d", result.Version, CurrentMapVersion)
	}
	return result, nil
}
