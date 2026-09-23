package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunDeobfuscate(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "report.yaml")
	report := []byte("replacements:\n  - - canonical: 10.0.0.1\n      replacedWith: x-ip-1-x\n")
	require.NoError(t, os.WriteFile(reportPath, report, 0600))

	var output bytes.Buffer
	err := RunDeobfuscate(reportPath, bytes.NewBufferString("node x-ip-1-x unknown\n"), &output)
	require.NoError(t, err)
	require.Equal(t, "node 10.0.0.1 unknown\n", output.String())
}

func TestRunDeobfuscateFile(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.yaml")
	inputPath := filepath.Join(dir, "response.txt")
	outputPath := filepath.Join(dir, "restored.txt")
	require.NoError(t, os.WriteFile(reportPath, []byte("replacements:\n  - - canonical: original\n      replacedWith: token\n"), 0600))
	require.NoError(t, os.WriteFile(inputPath, []byte("token\n"), 0600))

	require.NoError(t, RunDeobfuscateFile(reportPath, inputPath, outputPath))
	restored, err := os.ReadFile(outputPath)
	require.NoError(t, err)
	require.Equal(t, "original\n", string(restored))
}

func TestRunDeobfuscateFileRejectsSameInputAndOutput(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.yaml")
	inputPath := filepath.Join(dir, "response.txt")
	require.NoError(t, os.WriteFile(reportPath, []byte("replacements:\n  - - canonical: original\n      replacedWith: token\n"), 0600))
	require.NoError(t, os.WriteFile(inputPath, []byte("token\n"), 0600))

	err := RunDeobfuscateFile(reportPath, inputPath, inputPath)
	require.EqualError(t, err, "input and output paths must differ")
	contents, readErr := os.ReadFile(inputPath)
	require.NoError(t, readErr)
	require.Equal(t, "token\n", string(contents))
}

func TestRunDeobfuscateFileRejectsInvalidReportBeforeCreatingOutput(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.yaml")
	outputPath := filepath.Join(dir, "restored.txt")
	require.NoError(t, os.WriteFile(reportPath, []byte("replacements: ["), 0600))

	err := RunDeobfuscateFile(reportPath, "", outputPath)
	require.Error(t, err)
	_, statErr := os.Stat(outputPath)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}
