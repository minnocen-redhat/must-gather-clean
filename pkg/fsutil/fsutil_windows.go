//go:build windows
// +build windows

package fsutil

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"strings"
)

// no-op
func chown(path string, stat fs.FileInfo) error {
	return nil
}

// EnsurePrivatePath applies an owner-only DACL using the Windows ACL tool.
// os.Chmod only changes the read-only attribute on Windows and is therefore
// not a confidentiality boundary. Resetting inheritance first is important:
// it removes inherited and pre-existing explicit ACEs before granting the
// current token's SID full control.
func EnsurePrivatePath(path string) error {
	stat, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to stat private path %s: %w", path, err)
	}
	if output, err := exec.Command("icacls", path, "/reset").CombinedOutput(); err != nil {
		return fmt.Errorf("failed to reset ACL for private path %s: %w (%s)", path, err, strings.TrimSpace(string(output)))
	}
	if output, err := exec.Command("icacls", path, "/inheritance:r").CombinedOutput(); err != nil {
		return fmt.Errorf("failed to remove inherited ACL for private path %s: %w (%s)", path, err, strings.TrimSpace(string(output)))
	}
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to determine current Windows user for private path %s: %w", path, err)
	}
	if current.Uid == "" {
		return fmt.Errorf("current Windows user has no SID for private path %s", path)
	}
	// An explicit SID avoids locale-dependent account names and does not invoke
	// a shell. icacls accepts a SID prefixed with '*'.
	grant := "*" + current.Uid + ":F"
	if stat.IsDir() {
		grant = "*" + current.Uid + ":(OI)(CI)F"
	}
	if output, err := exec.Command("icacls", path, "/grant:r", grant).CombinedOutput(); err != nil {
		return fmt.Errorf("failed to grant owner ACL for private path %s: %w (%s)", path, err, strings.TrimSpace(string(output)))
	}
	return nil
}

// CheckPrivatePath verifies an existing directory without changing its DACL.
// icacls renders the owner account name rather than the SID in its normal
// listing, so the check accepts full-control entries (including inherited
// child entries) only for the current account and rejects every other ACE.
func CheckPrivatePath(path string) error {
	stat, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("failed to inspect private path %s: %w", path, err)
	}
	if stat.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private path %s must not be a symbolic link", path)
	}
	if !stat.IsDir() {
		return fmt.Errorf("private path %s must be a directory", path)
	}
	return checkPrivateACL(path)
}

// CheckPrivateFile verifies the owner-only DACL on an existing regular file.
func CheckPrivateFile(path string) error {
	stat, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("failed to inspect private file %s: %w", path, err)
	}
	if stat.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private file %s must not be a symbolic link", path)
	}
	if !stat.Mode().IsRegular() {
		return fmt.Errorf("private file %s must be regular", path)
	}
	return checkPrivateACL(path)
}

func checkPrivateACL(path string) error {
	current, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to determine current Windows user for private path %s: %w", path, err)
	}
	output, err := exec.Command("icacls", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to inspect ACL for private path %s: %w (%s)", path, err, strings.TrimSpace(string(output)))
	}
	return validatePrivateACLListing(path, string(output), []string{current.Username, current.Uid})
}
