package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/openshift/must-gather-clean/pkg/deobfuscator"
	"github.com/openshift/must-gather-clean/pkg/fsutil"
	"github.com/openshift/must-gather-clean/pkg/reporting"
	"k8s.io/klog/v2"
)

// RunDeobfuscate restores a support response using the existing report.
func RunDeobfuscate(reportPath string, inputPath string, outputPath string) error {
	report, err := reporting.ReadReport(reportPath)
	if err != nil {
		return err
	}
	mapping, err := deobfuscator.NewMappingFromReport(report)
	if err != nil {
		return err
	}
	return runDeobfuscate(mapping, reportPath, inputPath, outputPath)
}

// runDeobfuscate applies a prepared mapping to a support response. Empty
// input or output paths mean stdin or stdout respectively, which supports
// shell pipes.
func runDeobfuscate(mapping *deobfuscator.Mapping, sourcePath string, inputPath string, outputPath string) error {
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

	var outputAbsolute string
	if inputPath != "" && outputPath != "" {
		inputAbsolute, err := filepath.Abs(inputPath)
		if err != nil {
			return fmt.Errorf("failed to resolve support response input %s: %w", inputPath, err)
		}
		resolvedOutput, err := filepath.Abs(outputPath)
		if err != nil {
			return fmt.Errorf("failed to resolve deobfuscated response output %s: %w", outputPath, err)
		}
		if inputAbsolute == resolvedOutput {
			return fmt.Errorf("support response input and output must be different files")
		}

		inputInfo, err := os.Stat(inputAbsolute)
		if err != nil {
			return fmt.Errorf("failed to stat support response input %s: %w", inputPath, err)
		}
		outputInfo, err := os.Stat(resolvedOutput)
		if err == nil && os.SameFile(inputInfo, outputInfo) {
			return fmt.Errorf("support response input and output must be different files")
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat deobfuscated response output %s: %w", outputPath, err)
		}
	}
	if outputPath != "" {
		if outputAbsolute, err = filepath.Abs(outputPath); err != nil {
			return fmt.Errorf("failed to resolve deobfuscated response output %s: %w", outputPath, err)
		}
		if err := ensureReportIsNotOutput(sourcePath, outputAbsolute); err != nil {
			return err
		}
	}

	if len(mapping.Ambiguous) > 0 || len(mapping.Unsupported) > 0 {
		klog.Warningf("deobfuscation report contains %d ambiguous and %d unsupported mappings; affected tokens will be left unchanged", len(mapping.Ambiguous), len(mapping.Unsupported))
	}

	if outputPath == "" {
		// Process into a private temporary first. A response can contain a
		// foreign run token after valid data, and stdout cannot be rolled back
		// once that prefix has been written.
		temporary, err := os.CreateTemp("", ".deobfuscated-response-*")
		if err != nil {
			return fmt.Errorf("failed to create temporary deobfuscated response: %w", err)
		}
		temporaryPath := temporary.Name()
		defer func() {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}()
		if err := fsutil.EnsurePrivatePath(temporaryPath); err != nil {
			return fmt.Errorf("failed to secure temporary deobfuscated response: %w", err)
		}
		if err := deobfuscator.Process(mapping, input, temporary); err != nil {
			return err
		}
		if err := temporary.Sync(); err != nil {
			return fmt.Errorf("failed to sync temporary deobfuscated response: %w", err)
		}
		if _, err := temporary.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("failed to rewind temporary deobfuscated response: %w", err)
		}
		if _, err := io.Copy(os.Stdout, temporary); err != nil {
			return fmt.Errorf("failed to write deobfuscated support response: %w", err)
		}
		return nil
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
	if err := fsutil.EnsurePrivatePath(temporaryPath); err != nil {
		return fmt.Errorf("failed to secure deobfuscated response %s: %w", outputPath, err)
	}
	if err := deobfuscator.Process(mapping, input, temporary); err != nil {
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
	if err := fsutil.EnsurePrivatePath(outputAbsolute); err != nil {
		_ = os.Remove(outputAbsolute)
		return fmt.Errorf("failed to secure deobfuscated response %s: %w", outputPath, err)
	}
	return nil
}

// ensureReportIsNotOutput prevents the report from being replaced by
// the deobfuscated response. The os.SameFile check also catches
// hard links and symlink aliases when both paths exist.
func ensureReportIsNotOutput(sourcePath, outputAbsolute string) error {
	reportAbsolute, err := filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to resolve report %s: %w", sourcePath, err)
	}
	if reportAbsolute == outputAbsolute {
		return fmt.Errorf("report and response output must be different files")
	}

	reportInfo, reportErr := os.Stat(reportAbsolute)
	if reportErr != nil {
		// The caller reports the authoritative report error. There is no
		// existing report to protect in this case.
		return nil
	}
	outputInfo, outputErr := os.Stat(outputAbsolute)
	if outputErr != nil {
		if os.IsNotExist(outputErr) {
			return nil
		}
		return fmt.Errorf("failed to stat deobfuscated response output %s: %w", outputAbsolute, outputErr)
	}
	if os.SameFile(reportInfo, outputInfo) {
		return fmt.Errorf("report and response output must be different files")
	}
	return nil
}
