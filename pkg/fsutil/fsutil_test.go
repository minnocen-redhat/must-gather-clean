package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExistingEmptyDir(t *testing.T) {
	testDir, err := os.MkdirTemp(os.TempDir(), "test-dir-*")
	require.NoError(t, err)
	defer func() {
		_ = os.RemoveAll(testDir)
	}()

	err = ensureOutputPath(testDir, false, testDir)
	require.NoError(t, err)
}

func TestEnsurePrivatePathUsesOwnerOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows permissions are validated through the ACL API")
	}
	root := t.TempDir()
	filePath := filepath.Join(root, "private-file")
	directoryPath := filepath.Join(root, "private-directory")
	require.NoError(t, os.WriteFile(filePath, []byte("secret"), 0644))
	require.NoError(t, os.Mkdir(directoryPath, 0755))

	require.NoError(t, EnsurePrivatePath(filePath))
	require.NoError(t, EnsurePrivatePath(directoryPath))
	require.NoError(t, CheckPrivateFile(filePath))
	require.NoError(t, CheckPrivatePath(directoryPath))

	fileInfo, err := os.Stat(filePath)
	require.NoError(t, err)
	directoryInfo, err := os.Stat(directoryPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), fileInfo.Mode().Perm())
	assert.Equal(t, os.FileMode(0700), directoryInfo.Mode().Perm())
}

func TestEnsureOutputPathNonEmptyDir(t *testing.T) {
	testDir, err := os.MkdirTemp(os.TempDir(), "test-dir-*")
	require.NoError(t, err)
	defer func() {
		_ = os.RemoveAll(testDir)
	}()

	err = os.WriteFile(filepath.Join(testDir, "nonempty"), []byte("nonempty"), 0644)
	require.NoError(t, err)

	err = ensureOutputPath(testDir, false, testDir)
	require.Error(t, err)
	require.Equal(t, fmt.Errorf("output directory %s is not empty", testDir), err)
}

func TestEnsureOutputPathInvalidLocation(t *testing.T) {
	file, err := os.CreateTemp("", "temp-file")
	require.NoError(t, err)
	_, err = file.Write([]byte("test-contents"))
	require.NoError(t, err)
	err = file.Close()
	require.NoError(t, err)

	err = ensureOutputPath(file.Name(), false, file.Name())
	require.Error(t, err)
	require.Equal(t, fmt.Errorf("output destination must be a directory: '%s'", file.Name()), err)
}

func TestEnsureOutputPathCreateIfRequired(t *testing.T) {
	testDir, err := os.MkdirTemp("", "test-dir-*")
	require.NoError(t, err)
	defer func(path string) {
		_ = os.RemoveAll(path)
	}(testDir)

	outputDir := filepath.Join(testDir, "nonexistent")
	err = ensureOutputPath(outputDir, false, testDir)
	require.NoError(t, err)
	info, err := os.Stat(outputDir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func TestEnsureOutputPathDeletesIfRequired(t *testing.T) {
	testDir, err := os.MkdirTemp(os.TempDir(), "test-dir-*")
	require.NoError(t, err)
	defer func() {
		_ = os.RemoveAll(testDir)
	}()

	secondTestDir, err := os.MkdirTemp(os.TempDir(), "test-dir-*")
	require.NoError(t, err)
	defer func() {
		_ = os.RemoveAll(secondTestDir)
	}()

	toBeDeletedFile := filepath.Join(testDir, "nonempty")
	err = os.WriteFile(toBeDeletedFile, []byte("nonempty"), 0664)
	require.NoError(t, err)

	err = ensureOutputPath(testDir, true, secondTestDir)
	require.NoError(t, err)

	info, err := os.Stat(testDir)
	require.NoError(t, err)
	require.True(t, info.IsDir())

	_, err = os.Stat(toBeDeletedFile)
	require.Error(t, err)
	require.Truef(t, os.IsNotExist(err), "file %s exists even though it shouldn't: %s", toBeDeletedFile, err)
}

func TestMkdirRecursively(t *testing.T) {
	testDir, err := os.MkdirTemp(os.TempDir(), "test-dir-*")
	require.NoError(t, err)
	defer func() {
		_ = os.RemoveAll(testDir)
	}()
	initialInfo, err := os.Stat(testDir)
	require.NoError(t, err)

	// this creates a parallel folder structure with testDir permissions
	inputFolder := filepath.Join(testDir, "b", "a", "a")
	require.NoError(t, os.MkdirAll(inputFolder, initialInfo.Mode()))

	outputFolder := filepath.Join(testDir, "a", "a", "a")
	require.NoError(t, MkdirAllWithChown(outputFolder, inputFolder))

	expectedInfo, err := os.Stat(inputFolder)
	require.NoError(t, err)
	actualInfo, err := os.Stat(outputFolder)
	require.NoError(t, err)
	assert.Equal(t, expectedInfo.Mode(), actualInfo.Mode())
}

func TestSymlinkDetection(t *testing.T) {
	tmpInputDir, err := os.MkdirTemp(os.TempDir(), "test-dir-*")
	require.NoError(t, err)
	defer func() {
		_ = os.RemoveAll(tmpInputDir)
	}()

	textFile := filepath.Join(tmpInputDir, "link.txt")
	err = os.WriteFile(textFile, []byte("text"), 0644)
	require.NoError(t, err)

	linkPath := filepath.Join(tmpInputDir, "link")
	err = os.Symlink(textFile, linkPath)
	require.NoError(t, err)

	info, err := os.Lstat(linkPath)
	require.NoError(t, err)
	assert.Truef(t, IsSymbolicLink(info), "%s should be a symbolic link", info.Name())

	info, err = os.Lstat(textFile)
	require.NoError(t, err)
	assert.Falsef(t, IsSymbolicLink(info), "%s should not be a symbolic link", info.Name())
}

func TestOutputTransactionPublishesOnlyOnCommit(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	output := filepath.Join(root, "output")
	require.NoError(t, os.Mkdir(input, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(input, "source"), []byte("source"), 0600))
	require.NoError(t, os.Mkdir(output, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(output, "old"), []byte("old"), 0600))

	transaction, err := BeginOutputTransaction(input, output, true)
	require.NoError(t, err)
	defer func() { _ = transaction.Cleanup() }()
	require.NoError(t, os.WriteFile(filepath.Join(transaction.StagingPath, "new"), []byte("new"), 0600))
	require.NoError(t, transaction.Commit())

	assert.FileExists(t, filepath.Join(output, "new"))
	assert.NoFileExists(t, filepath.Join(output, "old"))
	backupPaths, err := filepath.Glob(filepath.Join(root, ".must-gather-clean-backup-*"))
	require.NoError(t, err)
	assert.Empty(t, backupPaths)
}

func TestOutputTransactionCleanupLeavesExistingOutput(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	output := filepath.Join(root, "output")
	require.NoError(t, os.Mkdir(input, 0755))
	require.NoError(t, os.Mkdir(output, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(output, "old"), []byte("old"), 0600))

	transaction, err := BeginOutputTransaction(input, output, true)
	require.NoError(t, err)
	require.NoError(t, transaction.Cleanup())

	assert.FileExists(t, filepath.Join(output, "old"))
	assert.NoDirExists(t, transaction.StagingPath)
}

func TestOutputTransactionRejectsOverlappingPaths(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	require.NoError(t, os.MkdirAll(filepath.Join(input, "nested"), 0755))

	for _, output := range []string{
		filepath.Join(input, "nested", "output"),
		root,
	} {
		_, err := BeginOutputTransaction(input, output, true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must not overlap")
	}
}

func TestOutputTransactionRejectsSymlinkAliases(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	alias := filepath.Join(root, "input-alias")
	require.NoError(t, os.Mkdir(input, 0755))
	require.NoError(t, os.Symlink(input, alias))

	_, err := BeginOutputTransaction(input, alias, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not overlap")
}

func TestOutputTransactionRejectsSymlinkOutput(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "input")
	target := filepath.Join(root, "target")
	output := filepath.Join(root, "output")
	require.NoError(t, os.Mkdir(input, 0755))
	require.NoError(t, os.Mkdir(target, 0755))
	require.NoError(t, os.Symlink(target, output))

	_, err := BeginOutputTransaction(input, output, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be a symbolic link")
	assert.DirExists(t, target)
}
