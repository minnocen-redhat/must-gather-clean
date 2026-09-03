package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/openshift/must-gather-clean/pkg/deobfuscator"
	"k8s.io/klog/v2"
)

// RunDeobfuscate applies a private map to a support response. Empty input or
// output paths mean stdin or stdout respectively, which supports shell pipes.
func RunDeobfuscate(mapPath string, inputPath string, outputPath string) error {
	if inputPath != "" && outputPath != "" {
		inputAbsolute, err := filepath.Abs(inputPath)
		if err != nil {
			return fmt.Errorf("failed to resolve support response input %s: %w", inputPath, err)
		}
		outputAbsolute, err := filepath.Abs(outputPath)
		if err != nil {
			return fmt.Errorf("failed to resolve deobfuscated response output %s: %w", outputPath, err)
		}
		if inputAbsolute == outputAbsolute {
			return fmt.Errorf("support response input and output must be different files")
		}
	}

	privateMap, err := deobfuscator.ReadMap(mapPath)
	if err != nil {
		return err
	}
	if len(privateMap.Ambiguous) > 0 || len(privateMap.Unsupported) > 0 {
		klog.Warningf("deobfuscation map contains %d ambiguous and %d unsupported mappings; affected tokens will be left unchanged", len(privateMap.Ambiguous), len(privateMap.Unsupported))
	}

	var input io.Reader = os.Stdin
	var inputFile *os.File
	if inputPath != "" {
		inputFile, err = os.Open(inputPath)
		if err != nil {
			return fmt.Errorf("failed to open support response %s: %w", inputPath, err)
		}
		defer func() { _ = inputFile.Close() }()
		input = inputFile
	}

	var output io.Writer = os.Stdout
	var outputFile *os.File
	if outputPath != "" {
		outputFile, err = os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return fmt.Errorf("failed to create deobfuscated response %s: %w", outputPath, err)
		}
		defer func() { _ = outputFile.Close() }()
		if err := outputFile.Chmod(0600); err != nil {
			return fmt.Errorf("failed to secure deobfuscated response %s: %w", outputPath, err)
		}
		output = outputFile
	}

	return deobfuscator.Process(privateMap, input, output)
}
