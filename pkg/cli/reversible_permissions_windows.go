//go:build windows
// +build windows

package cli

// ensureReversibleWorkflowSupported is kept as a platform capability hook.
// Private artifacts are secured by fsutil.EnsurePrivatePath, which applies a
// real owner-only Windows DACL rather than relying on os.Chmod.
func ensureReversibleWorkflowSupported() error {
	return nil
}
