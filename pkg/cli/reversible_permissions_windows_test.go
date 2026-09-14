//go:build windows
// +build windows

package cli

import (
	"strings"
	"testing"
)

func TestReversibleWorkflowPermissionsSupportedOnWindows(t *testing.T) {
	err := ensureReversibleWorkflowSupported()
	if err != nil {
		t.Fatalf("expected Windows ACL support, got %v", err)
	}
}

func TestRunWithResponseDeobfuscationKeepsValidationOnWindows(t *testing.T) {
	err := runWithResponseDeobfuscation("missing-config.yaml", "missing-input", "missing-output", false, "", 0)
	if err == nil || !strings.Contains(err.Error(), "invalid number of workers specified") {
		t.Fatalf("expected normal reversible workflow validation, got %v", err)
	}
}

func TestLegacyCleaningRemainsAvailableOnWindows(t *testing.T) {
	err := Run("", "", "", false, "", 0)
	if err == nil || !strings.Contains(err.Error(), "invalid number of workers specified") {
		t.Fatalf("expected legacy validation error, got %v", err)
	}
}
