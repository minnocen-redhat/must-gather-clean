//go:build windows
// +build windows

package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePrivatePathUsesOwnerOnlyDACL(t *testing.T) {
	root := t.TempDir()
	privateDir := filepath.Join(root, "private")
	if err := os.Mkdir(privateDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivatePath(privateDir); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivatePath(privateDir); err != nil {
		t.Fatal(err)
	}

	privateFile := filepath.Join(privateDir, "response.txt")
	if err := os.WriteFile(privateFile, []byte("response"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivatePath(privateFile); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(privateFile); err != nil {
		t.Fatal(err)
	}
}
