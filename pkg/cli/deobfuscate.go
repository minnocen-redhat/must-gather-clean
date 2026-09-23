package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/openshift/must-gather-clean/pkg/deobfuscator"
)

// RunDeobfuscate restores values in input using the report at reportPath.
func RunDeobfuscate(reportPath string, input io.Reader, output io.Writer) error {
	mapping, err := deobfuscator.LoadReport(reportPath)
	if err != nil {
		return err
	}

	data, err := io.ReadAll(input)
	if err != nil {
		return fmt.Errorf("failed to read support response: %w", err)
	}
	if _, err := io.WriteString(output, mapping.Replace(string(data))); err != nil {
		return fmt.Errorf("failed to write deobfuscated support response: %w", err)
	}
	return nil
}

// RunDeobfuscateFile restores values from inputPath to outputPath. Empty paths
// use stdin and stdout respectively, which also makes the command suitable for
// shell pipelines.
func RunDeobfuscateFile(reportPath, inputPath, outputPath string) error {
	input := io.Reader(os.Stdin)
	if inputPath != "" {
		file, err := os.Open(inputPath)
		if err != nil {
			return fmt.Errorf("failed to open support response %s: %w", inputPath, err)
		}
		defer file.Close()
		input = file
	}

	output := io.Writer(os.Stdout)
	if outputPath != "" {
		file, err := os.Create(outputPath)
		if err != nil {
			return fmt.Errorf("failed to create deobfuscated support response %s: %w", outputPath, err)
		}
		defer file.Close()
		output = file
	}

	return RunDeobfuscate(reportPath, input, output)
}
