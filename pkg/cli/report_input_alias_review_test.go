package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunDeobfuscateFileRejectsInputReportAliases(t *testing.T) {
	for _, mode := range []string{"exact", "hardlink", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			reportPath := filepath.Join(dir, "report.yaml")
			reportData := []byte(validDeobfuscationReport)
			require.NoError(t, os.WriteFile(reportPath, reportData, 0600))

			inputPath := reportPath
			switch mode {
			case "hardlink":
				inputPath = filepath.Join(dir, "input-hardlink")
				require.NoError(t, os.Link(reportPath, inputPath))
			case "symlink":
				inputPath = filepath.Join(dir, "input-symlink")
				require.NoError(t, os.Symlink(reportPath, inputPath))
			}
			outputPath := filepath.Join(dir, "output.txt")

			err := RunDeobfuscateFile(reportPath, inputPath, outputPath)
			require.EqualError(t, err, "input and report paths must differ")
			after, readErr := os.ReadFile(reportPath)
			require.NoError(t, readErr)
			require.True(t, bytes.Equal(reportData, after))
			_, statErr := os.Stat(outputPath)
			require.ErrorIs(t, statErr, os.ErrNotExist)
		})
	}
}
