package deobfuscator

import (
	"testing"

	"github.com/openshift/must-gather-clean/pkg/reporting"
	"github.com/openshift/must-gather-clean/pkg/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMappingUsesCanonicalReportValues(t *testing.T) {
	report := &reporting.Report{
		RunID: "0123456789abcdef0123456789abcdef",
		Config: schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{{
			Type:            schema.ObfuscateTypeIP,
			ReplacementType: schema.ObfuscateReplacementTypeConsistent,
		}}},
		Replacements: [][]reporting.Replacement{{{
			Canonical:    "10.0.0.1",
			ReplacedWith: "x-mgc1-0123456789abcdef01234567-o1-x-ipv4-0000000001-x",
			Occurrences: []reporting.Occurrence{
				{Original: "10-0-0-1", Count: 3},
				{Original: "10.0.0.1", Count: 1},
			},
		}}},
	}

	mapping, err := NewMappingFromReport(report)
	require.NoError(t, err)
	require.Len(t, mapping.Rules, 1)
	assert.Equal(t, report.RunID, mapping.RunID)
	assert.Equal(t, "10.0.0.1", mapping.Rules[0].Original)
	assert.Equal(t, "10.0.0.1", mapping.Deobfuscate(mapping.Rules[0].Obfuscated))
}

func TestMappingLeavesAmbiguousReportTokensUnchanged(t *testing.T) {
	report := &reporting.Report{
		Config: schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{{
			Type:            schema.ObfuscateTypeIP,
			ReplacementType: schema.ObfuscateReplacementTypeConsistent,
		}}},
		Replacements: [][]reporting.Replacement{{
			{Canonical: "10.0.0.1", ReplacedWith: "same-token"},
			{Canonical: "10.0.0.2", ReplacedWith: "same-token"},
		}},
	}

	mapping, err := NewMappingFromReport(report)
	require.NoError(t, err)
	assert.Empty(t, mapping.Rules)
	require.Len(t, mapping.Ambiguous, 1)
	assert.Equal(t, "same-token", mapping.Ambiguous[0].Obfuscated)
}

func TestMappingSkipsUnsupportedReportGroups(t *testing.T) {
	report := &reporting.Report{
		Config: schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{{
			Type:            schema.ObfuscateTypeIP,
			ReplacementType: schema.ObfuscateReplacementTypeStatic,
		}}},
		Replacements: [][]reporting.Replacement{{{
			Canonical:    "10.0.0.1",
			ReplacedWith: "xxx.xxx.xxx.xxx",
		}}},
	}

	mapping, err := NewMappingFromReport(report)
	require.NoError(t, err)
	assert.Empty(t, mapping.Rules)
	require.Len(t, mapping.Unsupported, 1)
	assert.Equal(t, string(schema.ObfuscateTypeIP), mapping.Unsupported[0].Type)
}

func TestMappingExcludesChainedReplacements(t *testing.T) {
	report := &reporting.Report{
		Config: schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{
			{Type: schema.ObfuscateTypeIP, ReplacementType: schema.ObfuscateReplacementTypeConsistent},
			{Type: schema.ObfuscateTypeMAC, ReplacementType: schema.ObfuscateReplacementTypeConsistent},
		}},
		Replacements: [][]reporting.Replacement{
			{{Canonical: "original", ReplacedWith: "token-one"}},
			{{Canonical: "token-one", ReplacedWith: "token-two"}},
		},
	}

	mapping, err := NewMappingFromReport(report)
	require.NoError(t, err)
	assert.Empty(t, mapping.Rules)
	require.Len(t, mapping.Unsupported, 1)
	assert.Equal(t, "chained obfuscations", mapping.Unsupported[0].Type)
}
