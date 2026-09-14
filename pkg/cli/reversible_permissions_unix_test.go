//go:build !windows
// +build !windows

package cli

import "testing"

func TestReversibleWorkflowPermissionsSupported(t *testing.T) {
	if err := ensureReversibleWorkflowSupported(); err != nil {
		t.Fatalf("reversible workflow should be supported: %v", err)
	}
}
