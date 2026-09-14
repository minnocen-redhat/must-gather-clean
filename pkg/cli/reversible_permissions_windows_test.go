//go:build windows
// +build windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReversibleWorkflowPermissionsRejectedOnWindows(t *testing.T) {
	err := ensureReversibleWorkflowSupported()
	if err == nil || !strings.Contains(err.Error(), "owner-only permissions") {
		t.Fatalf("expected an owner-only permissions error, got %v", err)
	}
}

func TestRunWithOptionsRejectsReversibleWorkflowBeforeCreatingOutputOnWindows(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "cleaned")
	reportDir := t.TempDir()

	err := RunWithOptions("missing-config.yaml", inputDir, outputDir, RunOptions{
		ReportingFolder: reportDir,
		WorkerCount:     1,
		Reversible:      true,
	})
	if err == nil || !strings.Contains(err.Error(), "unavailable on Windows") {
		t.Fatalf("expected Windows support error, got %v", err)
	}
	if _, statErr := os.Stat(outputDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected no output directory, stat error: %v", statErr)
	}
}

func TestRunWithResponseDeobfuscationRejectsWindowsBeforeValidation(t *testing.T) {
	err := runWithResponseDeobfuscation("missing-config.yaml", "missing-input", "missing-output", false, "", 0)
	if err == nil || !strings.Contains(err.Error(), "unavailable on Windows") {
		t.Fatalf("expected Windows support error before validation, got %v", err)
	}
}

func TestLegacyCleaningRemainsAvailableOnWindows(t *testing.T) {
	err := Run("", "", "", false, "", 0)
	if err == nil || !strings.Contains(err.Error(), "invalid number of workers specified") {
		t.Fatalf("expected legacy validation error, got %v", err)
	}
}
