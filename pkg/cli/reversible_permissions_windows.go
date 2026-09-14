//go:build windows
// +build windows

package cli

import "fmt"

// ensureReversibleWorkflowSupported prevents creation of private artifacts on
// Windows. os.Chmod only controls the read-only attribute there and cannot
// enforce the owner-only ACL required by the reversible workflow.
func ensureReversibleWorkflowSupported() error {
	return fmt.Errorf("response deobfuscation is unavailable on Windows: owner-only permissions for private maps and reports cannot be guaranteed")
}
