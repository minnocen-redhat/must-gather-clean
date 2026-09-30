package deobfuscator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift/must-gather-clean/pkg/reporting"
	"github.com/openshift/must-gather-clean/pkg/schema"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLoadReportPreservesSelfChainContainingShorterToken(t *testing.T) {
	report := reporting.Report{
		Config: schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{{
			Type: schema.ObfuscateTypeIP, ReplacementType: schema.ObfuscateReplacementTypeConsistent,
			Replacement: schema.ObfuscateReplacement{"192.0.2.1": "token", "token-long": "token-long"},
		}}},
		Replacements: [][]reporting.Replacement{{
			{Canonical: "192.0.2.1", ReplacedWith: "token", Occurrences: []reporting.Occurrence{{Original: "192.0.2.1"}}},
			{Canonical: "token-long", ReplacedWith: "token-long", Occurrences: []reporting.Occurrence{{Original: "token-long"}}},
		}},
	}
	mapping := loadReviewReport(t, report)
	require.Equal(t, "token-long token", mapping.Replace("token-long token"))
}

func TestLoadReportPreservesChainsFromExactOutputs(t *testing.T) {
	for _, original := range []string{"192.0.2.1", "prefix-192.0.2.1-suffix"} {
		for _, target := range []schema.ObfuscateTarget{schema.ObfuscateTargetAll, schema.ObfuscateTargetFileContents, schema.ObfuscateTargetFilePath} {
			t.Run(original+string(target), func(t *testing.T) {
				report := reporting.Report{
					Config: schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{
						{Type: schema.ObfuscateTypeExact, Target: target, ExactReplacements: []schema.ObfuscateExactReplacementsElem{{Original: "keyword", Replacement: "192.0.2.1"}}},
						{Type: schema.ObfuscateTypeIP, Target: target, ReplacementType: schema.ObfuscateReplacementTypeConsistent, Replacement: schema.ObfuscateReplacement{original: "token"}},
					}},
					Replacements: [][]reporting.Replacement{{}, {{Canonical: original, ReplacedWith: "token", Occurrences: []reporting.Occurrence{{Original: original}}}}},
				}
				mapping := loadReviewReport(t, report)
				require.Equal(t, "token", mapping.Replace("token"))
			})
		}
	}
}

func TestLoadReportDoesNotChainExactOutputAcrossTargets(t *testing.T) {
	report := reporting.Report{
		Config: schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{
			{Type: schema.ObfuscateTypeExact, Target: schema.ObfuscateTargetFilePath, ExactReplacements: []schema.ObfuscateExactReplacementsElem{{Original: "keyword", Replacement: "192.0.2.1"}}},
			{Type: schema.ObfuscateTypeIP, Target: schema.ObfuscateTargetFileContents, ReplacementType: schema.ObfuscateReplacementTypeConsistent, Replacement: schema.ObfuscateReplacement{"192.0.2.1": "token"}},
		}},
		Replacements: [][]reporting.Replacement{{}, {{Canonical: "192.0.2.1", ReplacedWith: "token", Occurrences: []reporting.Occurrence{{Original: "192.0.2.1"}}}}},
	}
	mapping := loadReviewReport(t, report)
	require.Equal(t, "192.0.2.1", mapping.Replace("token"))
}

func TestLoadReportPreservesChainsAcrossCanonicalization(t *testing.T) {
	for _, firstType := range []schema.ObfuscateType{schema.ObfuscateTypeExact, schema.ObfuscateTypeKeywords} {
		for _, tc := range []struct {
			typ                 schema.ObfuscateType
			original, canonical string
		}{
			{schema.ObfuscateTypeMAC, "aa:bb:cc:dd:ee:ff", "AA:BB:CC:DD:EE:FF"},
			{schema.ObfuscateTypeIP, "192-0-2-1", "192.0.2.1"},
		} {
			t.Run(string(firstType)+string(tc.typ), func(t *testing.T) {
				first := schema.Obfuscate{Type: firstType}
				var firstGroup []reporting.Replacement
				if firstType == schema.ObfuscateTypeExact {
					first.ExactReplacements = []schema.ObfuscateExactReplacementsElem{{Original: "keyword", Replacement: tc.original}}
				} else {
					first.Replacement = schema.ObfuscateReplacement{"keyword": tc.original}
					firstGroup = []reporting.Replacement{{Canonical: "keyword", ReplacedWith: tc.original, Occurrences: []reporting.Occurrence{{Original: "keyword"}}}}
				}
				report := reporting.Report{
					Config: schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{
						first,
						{Type: tc.typ, ReplacementType: schema.ObfuscateReplacementTypeConsistent, Replacement: schema.ObfuscateReplacement{tc.original: "token"}},
					}},
					Replacements: [][]reporting.Replacement{firstGroup, {{Canonical: tc.canonical, ReplacedWith: "token", Occurrences: []reporting.Occurrence{{Original: tc.original}}}}},
				}
				mapping := loadReviewReport(t, report)
				require.Equal(t, "token", mapping.Replace("token"))
			})
		}
	}
}

func loadReviewReport(t *testing.T, report reporting.Report) Mapping {
	t.Helper()
	data, err := yaml.Marshal(report)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "report.yaml")
	require.NoError(t, os.WriteFile(path, data, 0600))
	mapping, err := LoadReport(path)
	require.NoError(t, err)
	return mapping
}
