package deobfuscator

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
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

func TestMappingSkipsEmptyValues(t *testing.T) {
	mapping := NewMapping([][]reporting.Replacement{{
		{Canonical: "", ReplacedWith: "token"},
		{Canonical: "original", ReplacedWith: ""},
	}})

	require.Empty(t, mapping)
	require.Equal(t, "token", mapping.Replace("token"))
}

func TestMappingReplaceReaderStreamsAndPreservesLineEndings(t *testing.T) {
	mapping := NewMapping([][]reporting.Replacement{{
		{Canonical: "10.0.0.1", ReplacedWith: "x-ip-1-x"},
		{Canonical: "cluster.example", ReplacedWith: "x-domain-1-x"},
	}})
	input := &chunkReader{reader: strings.NewReader("first x-ip-1-x\r\nsecond x-domain-1-x"), size: 3}
	var output bytes.Buffer

	require.NoError(t, mapping.ReplaceReader(input, &output))
	require.Equal(t, "first 10.0.0.1\r\nsecond cluster.example", output.String())
}

func TestMappingReplaceReaderHandlesTokensContainingNewlines(t *testing.T) {
	mapping := NewMapping([][]reporting.Replacement{{
		{Canonical: "original", ReplacedWith: "token\npart"},
	}})
	var output bytes.Buffer

	require.NoError(t, mapping.ReplaceReader(strings.NewReader("before token\npart after"), &output))
	require.Equal(t, "before original after", output.String())
}

type chunkReader struct {
	reader *strings.Reader
	size   int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(p) > r.size {
		p = p[:r.size]
	}
	return r.reader.Read(p)
}

func TestLoadReport(t *testing.T) {
	report := []byte("replacements:\n  - - canonical: original\n      replacedWith: token\n")
	path := t.TempDir() + "/report.yaml"
	require.NoError(t, writeFile(path, report))

	mapping, err := LoadReport(path)
	require.NoError(t, err)
	require.Equal(t, "original", mapping.Replace("token"))
}

func TestLoadReportReturnsReadAndParseErrors(t *testing.T) {
	_, err := LoadReport(filepath.Join(t.TempDir(), "missing.yaml"))
	require.ErrorIs(t, err, os.ErrNotExist)

	path := filepath.Join(t.TempDir(), "invalid.yaml")
	require.NoError(t, writeFile(path, []byte("replacements: [")))
	_, err = LoadReport(path)
	require.Error(t, err)
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0600)
}
