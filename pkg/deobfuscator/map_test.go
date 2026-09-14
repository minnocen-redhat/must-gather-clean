package deobfuscator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift/must-gather-clean/pkg/obfuscator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapBuildsRulesFromLedger(t *testing.T) {
	reports := []obfuscator.ReversibleObfuscatorReport{
		{Type: "IP", Reversible: true, Replacements: []obfuscator.ReversibleReplacement{{Canonical: "10.0.0.1", ReplacedWith: "x-ipv4-0000000001-x", Counter: map[string]uint{"10.0.0.1": 1}}}},
		{Type: "MAC", Reversible: true, Replacements: []obfuscator.ReversibleReplacement{{Canonical: "AA:BB:CC:DD:EE:FF", ReplacedWith: "x-mac-0000000001-x", Counter: map[string]uint{"aa:bb:cc:dd:ee:ff": 1}}}},
		{Type: "Domain", Reversible: true, Replacements: []obfuscator.ReversibleReplacement{{Canonical: "example.com", ReplacedWith: "domain0000000001", Counter: map[string]uint{"node.example.com": 1}}}},
		{Type: "AzureResources", Reversible: true, Replacements: []obfuscator.ReversibleReplacement{{Canonical: "cluster-name", ReplacedWith: "resource-calm-tiger", Counter: map[string]uint{"cluster-name": 1}}}},
	}

	privateMap, err := NewMapFromLedger(reports, "run-id")
	require.NoError(t, err)
	require.Len(t, privateMap.Rules, 4)
	assert.Empty(t, privateMap.Ambiguous)
	assert.Empty(t, privateMap.Unsupported)

	assert.Equal(t, "10.0.0.1", findRule(t, privateMap, "x-ipv4-0000000001-x").Original)
	assert.Equal(t, "example.com", findRule(t, privateMap, "domain0000000001").Original)
}

func TestMapLeavesCollisionsOutOfRules(t *testing.T) {
	reports := []obfuscator.ReversibleObfuscatorReport{{Type: "IP", Reversible: true, Replacements: []obfuscator.ReversibleReplacement{
		{Canonical: "10.0.0.1", ReplacedWith: "same-token", Counter: map[string]uint{"10.0.0.1": 1}},
		{Canonical: "10.0.0.2", ReplacedWith: "same-token", Counter: map[string]uint{"10.0.0.2": 1}},
	}}}

	privateMap, err := NewMapFromLedger(reports, "run-id")
	require.NoError(t, err)
	assert.Empty(t, privateMap.Rules)
	require.Len(t, privateMap.Ambiguous, 1)
	assert.Equal(t, "same-token", privateMap.Ambiguous[0].Obfuscated)
	assert.Equal(t, []string{"10.0.0.1", "10.0.0.2"}, privateMap.Ambiguous[0].Originals)
}

func TestMapSkipsUnusedReplacements(t *testing.T) {
	reports := []obfuscator.ReversibleObfuscatorReport{{Type: "IP", Reversible: true, Replacements: []obfuscator.ReversibleReplacement{
		{Canonical: "used", ReplacedWith: "masked", Counter: map[string]uint{"used": 2}},
		{Canonical: "unused", ReplacedWith: "not-in-output", Counter: map[string]uint{"unused": 0}},
	}}}

	privateMap, err := NewMapFromLedger(reports, "run-id")
	require.NoError(t, err)
	require.Len(t, privateMap.Rules, 1)
	assert.Equal(t, "masked", privateMap.Rules[0].Obfuscated)
}

func TestMapRecordsUnsupportedLedgerEntries(t *testing.T) {
	reports := []obfuscator.ReversibleObfuscatorReport{{
		Type:              "Regex",
		Reversible:        false,
		UnsupportedReason: "regex replacements are static",
		Replacements: []obfuscator.ReversibleReplacement{{
			Canonical: "secret-value", ReplacedWith: "xxxxxxxxxxxx", Counter: map[string]uint{"secret-value": 1},
		}},
	}}

	privateMap, err := NewMapFromLedger(reports, "run-id")
	require.NoError(t, err)
	assert.Empty(t, privateMap.Rules)
	require.Len(t, privateMap.Unsupported, 1)
	assert.Equal(t, "Regex", privateMap.Unsupported[0].Type)
	assert.Contains(t, privateMap.Unsupported[0].Reason, "static")
}

func TestMapWriteReadAndDeobfuscate(t *testing.T) {
	privateMap, err := NewMapFromLedger([]obfuscator.ReversibleObfuscatorReport{{
		Type:       "IP",
		Reversible: true,
		Replacements: []obfuscator.ReversibleReplacement{{
			Canonical: "10.0.0.1", ReplacedWith: "x-ipv4-0000000001-x", Counter: map[string]uint{"10.0.0.1": 1},
		}},
	}}, "run-id")
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "map.yaml")
	require.NoError(t, privateMap.Write(path))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())

	loaded, err := ReadMap(path)
	require.NoError(t, err)
	assert.Equal(t, privateMap.RunID, loaded.RunID)
	assert.Equal(t, "node 10.0.0.1", loaded.Deobfuscate("node x-ipv4-0000000001-x"))
}

func TestMapDoesNotDeobfuscateAmbiguousToken(t *testing.T) {
	privateMap := &Map{
		Version: CurrentMapVersion,
		Rules:   []Rule{{Obfuscated: "safe-token", Original: "original"}},
		Ambiguous: []AmbiguousRule{{
			Obfuscated: "ambiguous-token",
			Originals:  []string{"one", "two"},
		}},
	}

	assert.Equal(t, "original ambiguous-token", privateMap.Deobfuscate("safe-token ambiguous-token"))
}

func TestMapDoesNotMarkSubstringOverlapsAsChains(t *testing.T) {
	reports := []obfuscator.ReversibleObfuscatorReport{
		{
			Type:       "AzureResources",
			Reversible: true,
			Replacements: []obfuscator.ReversibleReplacement{{
				Canonical:    "cluster-name",
				ReplacedWith: "x-mgc1-run-tag-o1-resource-calm-tiger",
				Counter:      map[string]uint{"cluster-name": 1},
			}},
		},
		{
			Type:       "AzureResources",
			Reversible: true,
			Replacements: []obfuscator.ReversibleReplacement{{
				Canonical:    "resource",
				ReplacedWith: "x-mgc1-run-tag-o1-resourcegroup-calm-tiger",
				Counter:      map[string]uint{"resource": 1},
			}},
		},
	}

	privateMap, err := NewMapFromLedger(reports, "run-id")
	require.NoError(t, err)
	assert.Len(t, privateMap.Rules, 2)
	assert.Empty(t, privateMap.Unsupported)
}

func TestMapMarksExactChainsUnsupportedFromLedger(t *testing.T) {
	reports := []obfuscator.ReversibleObfuscatorReport{
		{
			Type:       "first",
			Reversible: true,
			Replacements: []obfuscator.ReversibleReplacement{{
				Canonical:    "original",
				ReplacedWith: "token-one",
				Counter:      map[string]uint{"original": 1},
			}},
		},
		{
			Type:       "second",
			Reversible: true,
			Replacements: []obfuscator.ReversibleReplacement{{
				Canonical:    "token-one",
				ReplacedWith: "token-two",
				Counter:      map[string]uint{"token-one": 1},
			}},
		},
	}

	privateMap, err := NewMapFromLedger(reports, "run-id")
	require.NoError(t, err)
	assert.Empty(t, privateMap.Rules)
	require.Len(t, privateMap.Unsupported, 1)
	assert.Equal(t, "chained obfuscations", privateMap.Unsupported[0].Type)
}

func findRule(t *testing.T, privateMap *Map, obfuscated string) Rule {
	t.Helper()
	for _, rule := range privateMap.Rules {
		if rule.Obfuscated == obfuscated {
			return rule
		}
	}
	t.Fatalf("rule %q not found", obfuscated)
	return Rule{}
}
