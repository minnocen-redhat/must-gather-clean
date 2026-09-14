package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtifactTransactionFinalizeCleansPublishedArtifacts(t *testing.T) {
	directory := t.TempDir()
	oldReport := filepath.Join(directory, reportFileName)
	require.NoError(t, os.WriteFile(oldReport, []byte("old\n"), 0600))

	transaction, err := newArtifactTransaction(directory)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(transaction.Stage(reportFileName), []byte("new\n"), 0600))
	require.NoError(t, transaction.Publish(false))
	require.Len(t, transaction.changes, 1)
	backupPath := transaction.changes[0].backupPath
	require.NotEmpty(t, backupPath)

	require.NoError(t, transaction.Finalize())
	assert.Equal(t, "new\n", string(mustReadFile(t, oldReport)))
	assert.True(t, transaction.finalized)
	assert.NoDirExists(t, transaction.staging)
	assert.NoFileExists(t, backupPath)
	assert.NoError(t, transaction.Rollback())
}

func TestArtifactTransactionRetriesCleanupWithoutRollingBackPublishedArtifacts(t *testing.T) {
	directory := t.TempDir()
	finalPath := filepath.Join(directory, reportFileName)
	require.NoError(t, os.WriteFile(finalPath, []byte("published\n"), 0600))
	backupPath := filepath.Join(directory, "backup")
	require.NoError(t, os.Mkdir(backupPath, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(backupPath, "keep-me"), []byte("backup\n"), 0600))
	stagingPath := filepath.Join(directory, "staging")
	require.NoError(t, os.Mkdir(stagingPath, 0700))

	transaction := &artifactTransaction{
		staging: stagingPath,
		changes: []artifactChange{{finalPath: finalPath, backupPath: backupPath}},
	}

	require.Error(t, transaction.Finalize())
	assert.True(t, transaction.publicationCommitted)
	assert.False(t, transaction.finalized)
	assert.Equal(t, "published\n", string(mustReadFile(t, finalPath)))
	assert.NoError(t, transaction.Rollback())
	assert.Equal(t, "published\n", string(mustReadFile(t, finalPath)))

	require.NoError(t, os.RemoveAll(backupPath))
	transaction.staging = filepath.Join(directory, "retry-staging")
	require.NoError(t, os.Mkdir(transaction.staging, 0700))
	require.NoError(t, transaction.Finalize())
	assert.True(t, transaction.finalized)
	assert.NoDirExists(t, transaction.staging)
}

func TestArtifactTransactionDoesNotOverwriteRunScopedMap(t *testing.T) {
	directory := t.TempDir()
	mapName := deobfuscationMapNameForRun("0123456789abcdef0123456789abcdef")
	mapPath := filepath.Join(directory, mapName)
	require.NoError(t, os.WriteFile(mapPath, []byte("previous map\n"), 0600))

	transaction, err := newArtifactTransaction(directory)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(transaction.Stage(reportFileName), []byte("new report\n"), 0600))
	require.NoError(t, os.WriteFile(transaction.Stage(mapName), []byte("new map\n"), 0600))

	err = transaction.Publish(true, mapName)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "without overwrite")
	assert.Equal(t, "previous map\n", string(mustReadFile(t, mapPath)))
	assert.NoFileExists(t, filepath.Join(directory, reportFileName))
	assert.NoError(t, transaction.Rollback())
}
