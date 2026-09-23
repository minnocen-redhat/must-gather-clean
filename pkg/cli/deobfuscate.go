package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/openshift/must-gather-clean/pkg/deobfuscator"
)

// RunDeobfuscate restores values in input using the report at reportPath.
func RunDeobfuscate(reportPath string, input io.Reader, output io.Writer) error {
	mapping, err := deobfuscator.LoadReport(reportPath)
	if err != nil {
		return err
	}
	return runDeobfuscate(mapping, input, output)
}

func runDeobfuscate(mapping deobfuscator.Mapping, input io.Reader, output io.Writer) error {
	data, err := io.ReadAll(input)
	if err != nil {
		return fmt.Errorf("failed to read support response: %w", err)
	}
	restored := mapping.Replace(string(data))
	written, err := io.WriteString(output, restored)
	if err != nil {
		return fmt.Errorf("failed to write deobfuscated support response: %w", err)
	}
	if written != len(restored) {
		return fmt.Errorf("failed to write deobfuscated support response: %w", io.ErrShortWrite)
	}
	return nil
}

// RunDeobfuscateFile restores values from inputPath to outputPath. Empty paths
// use stdin and stdout respectively, which also makes the command suitable for
// shell pipelines.
func RunDeobfuscateFile(reportPath, inputPath, outputPath string) (err error) {
	mapping, loadErr := deobfuscator.LoadReport(reportPath)
	if loadErr != nil {
		return loadErr
	}
	if inputPath != "" && outputPath != "" {
		inputAbs, absErr := filepath.Abs(inputPath)
		if absErr != nil {
			return fmt.Errorf("failed to resolve support response %s: %w", inputPath, absErr)
		}
		outputAbs, absErr := filepath.Abs(outputPath)
		if absErr != nil {
			return fmt.Errorf("failed to resolve output path %s: %w", outputPath, absErr)
		}
		if inputAbs == outputAbs {
			return fmt.Errorf("input and output paths must differ")
		}
	}

	input := io.Reader(os.Stdin)
	if inputPath != "" {
		file, openErr := os.Open(inputPath)
		if openErr != nil {
			return fmt.Errorf("failed to open support response %s: %w", inputPath, openErr)
		}
		defer func() {
			if closeErr := file.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("failed to close support response %s: %w", inputPath, closeErr)
			}
		}()
		input = file
	}

	output := io.Writer(os.Stdout)
	if outputPath != "" {
		file, createErr := os.Create(outputPath)
		if createErr != nil {
			return fmt.Errorf("failed to create deobfuscated support response %s: %w", outputPath, createErr)
		}
		defer func() {
			if closeErr := file.Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("failed to close deobfuscated support response %s: %w", outputPath, closeErr)
			}
		}()
		output = file
	}

	return runDeobfuscate(mapping, input, output)
}
