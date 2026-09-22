package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift/must-gather-clean/pkg/reporting"
	"github.com/openshift/must-gather-clean/pkg/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDeobfuscateCommandUsesReport(t *testing.T) {
	initFlags()
	report := &reporting.Report{
		RunID: "0123456789abcdef0123456789abcdef",
		Config: schema.SchemaJsonConfig{Obfuscate: []schema.Obfuscate{{
			Type:            schema.ObfuscateTypeIP,
			ReplacementType: schema.ObfuscateReplacementTypeConsistent,
		}}},
		Replacements: [][]reporting.Replacement{{{
			Canonical:    "10.0.0.1",
			ReplacedWith: "x-mgc1-0123456789abcdef01234567-o1-x-ipv4-0000000001-x",
		}}},
	}
	reportPath := filepath.Join(t.TempDir(), "report.yaml")
	file, err := os.Create(reportPath)
	require.NoError(t, err)
	data, err := yaml.Marshal(report)
	require.NoError(t, err)
	_, err = file.Write(data)
	require.NoError(t, err)
	require.NoError(t, file.Close())

	oldStdin, oldStdout := os.Stdin, os.Stdout
	stdinReader, stdinWriter, err := os.Pipe()
	require.NoError(t, err)
	stdoutReader, stdoutWriter, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
		_ = stdinReader.Close()
		_ = stdinWriter.Close()
		_ = stdoutReader.Close()
		_ = stdoutWriter.Close()
	})
	os.Stdin = stdinReader
	os.Stdout = stdoutWriter

	rootCmd.SetArgs([]string{"deobfuscate", "--report", reportPath})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	outputDone := make(chan []byte, 1)
	go func() {
		output, _ := io.ReadAll(stdoutReader)
		outputDone <- output
	}()

	executionDone := make(chan error, 1)
	go func() { executionDone <- rootCmd.Execute() }()

	_, err = io.WriteString(stdinWriter, "node x-mgc1-0123456789abcdef01234567-o1-x-ipv4-0000000001-x\n")
	require.NoError(t, err)
	require.NoError(t, stdinWriter.Close())
	require.NoError(t, <-executionDone)
	require.NoError(t, stdoutWriter.Close())

	assert.Equal(t, "node 10.0.0.1\n", string(<-outputDone))
}
