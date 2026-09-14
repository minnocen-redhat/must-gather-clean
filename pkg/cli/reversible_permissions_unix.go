//go:build !windows
// +build !windows

package cli

// ensureReversibleWorkflowSupported verifies that the reversible workflow can
// enforce the owner-only permissions promised for its private artifacts.
func ensureReversibleWorkflowSupported() error {
	return nil
}
