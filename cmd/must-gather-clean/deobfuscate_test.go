package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift/must-gather-clean/pkg/deobfuscator"
	"github.com/stretchr/testify/require"
)

func TestDeobfuscateCommandUsesMapAndStandardIO(t *testing.T) {
	initFlags()

	privateMap := &deobfuscator.Map{
		Version: deobfuscator.CurrentMapVersion,
		Rules: []deobfuscator.Rule{{
			Type:       "IP",
			Original:   "10.0.0.1",
			Obfuscated: "x-ipv4-0000000001-x",
		}},
	}
	mapPath := filepath.Join(t.TempDir(), "deobfuscation-map.yaml")
	require.NoError(t, privateMap.Write(mapPath))

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

	rootCmd.SetArgs([]string{"deobfuscate", "--map", mapPath})
	t.Cleanup(func() { rootCmd.SetArgs(nil) })

	outputDone := make(chan []byte, 1)
	go func() {
		output, _ := io.ReadAll(stdoutReader)
		outputDone <- output
	}()

	executionDone := make(chan error, 1)
	go func() { executionDone <- rootCmd.Execute() }()

	_, err = io.WriteString(stdinWriter, "node x-ipv4-0000000001-x\n")
	require.NoError(t, err)
	require.NoError(t, stdinWriter.Close())
	require.NoError(t, <-executionDone)
	require.NoError(t, stdoutWriter.Close())

	output := <-outputDone
	require.Equal(t, "node 10.0.0.1\n", string(output))
}
