package deobfuscator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift/must-gather-clean/pkg/obfuscator"
	"github.com/openshift/must-gather-clean/pkg/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMapBuildsRulesForConsistentObfuscators(t *testing.T) {
	config := schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{
		{Type: schema.ObfuscateTypeIP, ReplacementType: schema.ObfuscateReplacementTypeConsistent},
		{Type: schema.ObfuscateTypeMAC, ReplacementType: schema.ObfuscateReplacementTypeConsistent},
		{Type: schema.ObfuscateTypeDomain, ReplacementType: schema.ObfuscateReplacementTypeConsistent},
		{Type: schema.ObfuscateTypeAzureResources, ReplacementType: schema.ObfuscateReplacementTypeConsistent},
	}}
	reports := []obfuscator.ReplacementReport{
		{Replacements: []obfuscator.Replacement{{Canonical: "10.0.0.1", ReplacedWith: "x-ipv4-0000000001-x", Counter: map[string]uint{"10.0.0.1": 1}}}},
		{Replacements: []obfuscator.Replacement{{Canonical: "AA:BB:CC:DD:EE:FF", ReplacedWith: "x-mac-0000000001-x", Counter: map[string]uint{"aa:bb:cc:dd:ee:ff": 1}}}},
		{Replacements: []obfuscator.Replacement{{Canonical: "example.com", ReplacedWith: "domain0000000001", Counter: map[string]uint{"node.example.com": 1}}}},
		{Replacements: []obfuscator.Replacement{{Canonical: "cluster-name", ReplacedWith: "resource-calm-tiger", Counter: map[string]uint{"cluster-name": 1}}}},
	}

	privateMap, err := NewMap(config, reports)
	require.NoError(t, err)
	require.Len(t, privateMap.Rules, 4)
	assert.Empty(t, privateMap.Ambiguous)
	assert.Empty(t, privateMap.Unsupported)

	assert.Equal(t, "10.0.0.1", findRule(t, privateMap, "x-ipv4-0000000001-x").Original)
	assert.Equal(t, "example.com", findRule(t, privateMap, "domain0000000001").Original)
}

func TestNewMapLeavesCollisionsOutOfRules(t *testing.T) {
	config := schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{{
		Type:            schema.ObfuscateTypeIP,
		ReplacementType: schema.ObfuscateReplacementTypeConsistent,
	}}}
	reports := []obfuscator.ReplacementReport{{Replacements: []obfuscator.Replacement{
		{Canonical: "10.0.0.1", ReplacedWith: "same-token", Counter: map[string]uint{"10.0.0.1": 1}},
		{Canonical: "10.0.0.2", ReplacedWith: "same-token", Counter: map[string]uint{"10.0.0.2": 1}},
	}}}

	privateMap, err := NewMap(config, reports)
	require.NoError(t, err)
	assert.Empty(t, privateMap.Rules)
	require.Len(t, privateMap.Ambiguous, 1)
	assert.Equal(t, "same-token", privateMap.Ambiguous[0].Obfuscated)
	assert.Equal(t, []string{"10.0.0.1", "10.0.0.2"}, privateMap.Ambiguous[0].Originals)
}

func TestNewMapSkipsUnusedConfiguredReplacements(t *testing.T) {
	config := schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{{Type: schema.ObfuscateTypeKeywords}}}
	reports := []obfuscator.ReplacementReport{{Replacements: []obfuscator.Replacement{
		{Canonical: "used", ReplacedWith: "masked", Counter: map[string]uint{"used": 2}},
		{Canonical: "unused", ReplacedWith: "not-in-output", Counter: map[string]uint{"unused": 0}},
	}}}

	privateMap, err := NewMap(config, reports)
	require.NoError(t, err)
	require.Len(t, privateMap.Rules, 1)
	assert.Equal(t, "masked", privateMap.Rules[0].Obfuscated)
}

func TestNewMapDoesNotRestoreStaticReplacements(t *testing.T) {
	config := schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{{
		Type:            schema.ObfuscateTypeIP,
		ReplacementType: schema.ObfuscateReplacementTypeStatic,
	}}}
	reports := []obfuscator.ReplacementReport{{Replacements: []obfuscator.Replacement{{
		Canonical: "10.0.0.1", ReplacedWith: "xxx.xxx.xxx.xxx", Counter: map[string]uint{"10.0.0.1": 1},
	}}}}

	privateMap, err := NewMap(config, reports)
	require.NoError(t, err)
	assert.Empty(t, privateMap.Rules)
	require.Len(t, privateMap.Unsupported, 1)
	assert.Equal(t, string(schema.ObfuscateTypeIP), privateMap.Unsupported[0].Type)
	assert.Contains(t, privateMap.Unsupported[0].Reason, "static")
}

func TestNewMapDoesNotRestoreRegexReplacements(t *testing.T) {
	config := schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{{Type: schema.ObfuscateTypeRegex}}}
	reports := []obfuscator.ReplacementReport{{Replacements: []obfuscator.Replacement{{
		Canonical: "secret-value", ReplacedWith: "xxxxxxxxxxxx", Counter: map[string]uint{"secret-value": 1},
	}}}}

	privateMap, err := NewMap(config, reports)
	require.NoError(t, err)
	assert.Empty(t, privateMap.Rules)
	require.Len(t, privateMap.Unsupported, 1)
	assert.Equal(t, string(schema.ObfuscateTypeRegex), privateMap.Unsupported[0].Type)
}

func TestNewMapBuildsRulesForExactReplacements(t *testing.T) {
	config := schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{{
		Type: schema.ObfuscateTypeExact,
		ExactReplacements: []schema.ObfuscateExactReplacementsElem{
			{Original: "customer-secret", Replacement: "masked-secret"},
		},
	}}}
	reports := []obfuscator.ReplacementReport{{Replacements: []obfuscator.Replacement{{
		Canonical: "customer-secret", ReplacedWith: "masked-secret", Counter: map[string]uint{"customer-secret": 1},
	}}}}

	privateMap, err := NewMap(config, reports)
	require.NoError(t, err)
	assert.Equal(t, "customer-secret", findRule(t, privateMap, "masked-secret").Original)
}

func TestMapWriteReadAndDeobfuscate(t *testing.T) {
	privateMap, err := NewMap(schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{{
		Type:            schema.ObfuscateTypeIP,
		ReplacementType: schema.ObfuscateReplacementTypeConsistent,
	}}}, []obfuscator.ReplacementReport{{Replacements: []obfuscator.Replacement{{
		Canonical: "10.0.0.1", ReplacedWith: "x-ipv4-0000000001-x", Counter: map[string]uint{"10.0.0.1": 1},
	}}}})
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

func TestNewMapMarksExactChainsUnsupported(t *testing.T) {
	config := schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{{
		Type: schema.ObfuscateTypeExact,
		ExactReplacements: []schema.ObfuscateExactReplacementsElem{
			{Original: "one", Replacement: "two"},
			{Original: "two", Replacement: "three"},
		},
	}}}
	reports := []obfuscator.ReplacementReport{{Replacements: []obfuscator.Replacement{{
		Canonical: "one", ReplacedWith: "two", Counter: map[string]uint{"one": 1},
	}}}}

	privateMap, err := NewMap(config, reports)
	require.NoError(t, err)
	assert.Empty(t, privateMap.Rules)
	require.Len(t, privateMap.Unsupported, 1)
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
