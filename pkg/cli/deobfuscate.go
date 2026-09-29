package cli

import (
	"errors"
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
	if err := mapping.ReplaceReader(input, output); err != nil {
		return fmt.Errorf("failed to deobfuscate support response: %w", err)
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

	if outputPath != "" {
		if inputPath != "" {
			sameFile, statErr := pathsReferToSameFile(inputPath, outputPath)
			if statErr != nil {
				return fmt.Errorf("failed to compare support response and output files: %w", statErr)
			}
			if sameFile {
				return fmt.Errorf("input and output paths must differ")
			}
		}
		sameFile, statErr := pathsReferToSameFile(reportPath, outputPath)
		if statErr != nil {
			return fmt.Errorf("failed to compare report and output files: %w", statErr)
		}
		if sameFile {
			return fmt.Errorf("report and output paths must differ")
		}
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

func pathsReferToSameFile(firstPath, secondPath string) (bool, error) {
	firstInfo, err := os.Stat(firstPath)
	if err != nil {
		return false, err
	}
	secondInfo, err := os.Stat(secondPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return os.SameFile(firstInfo, secondInfo), nil
}
