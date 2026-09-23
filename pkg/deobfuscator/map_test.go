package deobfuscator

import (
	"os"
	"testing"

	"github.com/openshift/must-gather-clean/pkg/reporting"
	"github.com/stretchr/testify/require"
)

func TestMappingReplacesUnambiguousTokens(t *testing.T) {
	mapping := NewMapping([][]reporting.Replacement{{
		{Canonical: "10.0.0.1", ReplacedWith: "x-ip-1-x"},
		{Canonical: "cluster.example", ReplacedWith: "x-domain-1-x"},
	}})

	require.Equal(t, "node 10.0.0.1 cluster.example unknown", mapping.Replace("node x-ip-1-x x-domain-1-x unknown"))
}

func TestMappingPrefersTheLongestToken(t *testing.T) {
	mapping := NewMapping([][]reporting.Replacement{{
		{Canonical: "short", ReplacedWith: "token"},
		{Canonical: "long", ReplacedWith: "token-long"},
	}})

	require.Equal(t, "long short", mapping.Replace("token-long token"))
}

func TestMappingLeavesAmbiguousTokensUnchanged(t *testing.T) {
	mapping := NewMapping([][]reporting.Replacement{{
		{Canonical: "10.0.0.1", ReplacedWith: "x-static-ip"},
		{Canonical: "10.0.0.2", ReplacedWith: "x-static-ip"},
	}})

	require.Equal(t, "x-static-ip", mapping.Replace("x-static-ip"))
}

func TestLoadReport(t *testing.T) {
	report := []byte("replacements:\n  - - canonical: original\n      replacedWith: token\n")
	path := t.TempDir() + "/report.yaml"
	require.NoError(t, writeFile(path, report))

	mapping, err := LoadReport(path)
	require.NoError(t, err)
	require.Equal(t, "original", mapping.Replace("token"))
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0600)
}
