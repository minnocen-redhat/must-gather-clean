//go:build !windows
// +build !windows

package fsutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// EnsurePrivatePath applies owner-only permissions to a file or directory.
// It is used for temporary response data that must not be readable by other
// users.
func EnsurePrivatePath(path string) error {
	stat, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("failed to stat private path %s: %w", path, err)
	}
	mode := os.FileMode(0600)
	if stat.IsDir() {
		mode = 0700
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("failed to secure private path %s: %w", path, err)
	}
	return nil
}

// CheckPrivatePath verifies that an existing private directory is owned by the
// current user and has no group/other permission bits. It deliberately does
// not change the directory: callers must reject an unsafe user-supplied path.
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
	return checkPrivateOwnership(path, stat, 0700)
}

// CheckPrivateFile verifies owner-only permissions on an existing private
// regular file without changing it.
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
	return checkPrivateOwnership(path, stat, 0600)
}

func checkPrivateOwnership(path string, stat os.FileInfo, expected os.FileMode) error {
	if stat.Mode().Perm() != expected {
		return fmt.Errorf("private path %s must have owner-only permissions (%04o), got %04o", path, expected, stat.Mode().Perm())
	}
	fileStat, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("private path %s has unsupported ownership metadata", path)
	}
	if int(fileStat.Uid) != os.Getuid() {
		return fmt.Errorf("private path %s must be owned by the current user (uid %d, owner %d)", path, os.Getuid(), fileStat.Uid)
	}
	return nil
}

func chown(path string, stat fs.FileInfo) error {
	uid := stat.Sys().(*syscall.Stat_t).Uid
	gid := stat.Sys().(*syscall.Stat_t).Gid
	err := os.Chown(path, int(uid), int(gid))
	if err != nil {
		// Permission denied is expected for non-root users
		// The file is still created with correct permissions for the current user
		if errors.Is(err, syscall.EPERM) || errors.Is(err, os.ErrPermission) {
			return nil
		}
		return fmt.Errorf("failed to chown '%s' back to owner (%d, %d): %w", path, uid, gid, err)
	}
	return nil
}
