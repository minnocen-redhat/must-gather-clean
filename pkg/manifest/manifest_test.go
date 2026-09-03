package manifest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManifestWriteRead(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte("config: {}\n"), 0600))

	created, err := New(configPath, "run-id")
	require.NoError(t, err)
	directory := t.TempDir()
	require.NoError(t, created.Write(directory))

	loaded, err := Read(directory)
	require.NoError(t, err)
	assert.Equal(t, CurrentVersion, loaded.Version)
	assert.Equal(t, StatusCompleted, loaded.Status)
	assert.Equal(t, "run-id", loaded.DeobfuscationMapRunID)
	assert.Len(t, loaded.ConfigSHA256, 64)
}

func TestReadMissingManifest(t *testing.T) {
	_, err := Read(t.TempDir())
	assert.ErrorIs(t, err, os.ErrNotExist)
}
