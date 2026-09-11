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
	var err error
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

	outputAbsolute := outputPath
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

		inputInfo, err := os.Stat(inputAbsolute)
		if err != nil {
			return fmt.Errorf("failed to stat support response input %s: %w", inputPath, err)
		}
		outputInfo, err := os.Stat(outputAbsolute)
		if err == nil && os.SameFile(inputInfo, outputInfo) {
			return fmt.Errorf("support response input and output must be different files")
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat deobfuscated response output %s: %w", outputPath, err)
		}
	}

	privateMap, err := deobfuscator.ReadMap(mapPath)
	if err != nil {
		return err
	}
	if len(privateMap.Ambiguous) > 0 || len(privateMap.Unsupported) > 0 {
		klog.Warningf("deobfuscation map contains %d ambiguous and %d unsupported mappings; affected tokens will be left unchanged", len(privateMap.Ambiguous), len(privateMap.Unsupported))
	}

	if outputPath == "" {
		return deobfuscator.Process(privateMap, input, os.Stdout)
	}

	outputAbsolute, err = filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("failed to resolve deobfuscated response output %s: %w", outputPath, err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(outputAbsolute), ".deobfuscated-response-*")
	if err != nil {
		return fmt.Errorf("failed to create deobfuscated response %s: %w", outputPath, err)
	}
	temporaryPath := temporary.Name()
	cleanupTemporary := true
	defer func() {
		_ = temporary.Close()
		if cleanupTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0600); err != nil {
		return fmt.Errorf("failed to secure deobfuscated response %s: %w", outputPath, err)
	}
	if err := deobfuscator.Process(privateMap, input, temporary); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("failed to sync deobfuscated response %s: %w", outputPath, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("failed to close deobfuscated response %s: %w", outputPath, err)
	}
	if err := os.Rename(temporaryPath, outputAbsolute); err != nil {
		return fmt.Errorf("failed to publish deobfuscated response %s: %w", outputPath, err)
	}
	cleanupTemporary = false
	return nil
}
