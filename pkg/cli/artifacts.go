package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

type artifactChange struct {
	finalPath  string
	backupPath string
}

type artifactTransaction struct {
	directory string
	staging   string
	changes   []artifactChange
	finalized bool
}

func newArtifactTransaction(directory string) (*artifactTransaction, error) {
	staging, err := os.MkdirTemp(directory, ".must-gather-clean-artifacts-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary reporting folder: %w", err)
	}
	if err := os.Chmod(staging, 0700); err != nil {
		_ = os.RemoveAll(staging)
		return nil, fmt.Errorf("failed to secure temporary reporting folder: %w", err)
	}
	return &artifactTransaction{directory: directory, staging: staging}, nil
}

func (t *artifactTransaction) Stage(name string) string {
	return filepath.Join(t.staging, name)
}

// Publish makes staged artifacts visible while keeping enough information to
// restore the previous report/map if output publication fails.
func (t *artifactTransaction) Publish(includeMap bool) error {
	if t == nil || t.finalized {
		return fmt.Errorf("artifact transaction is unavailable")
	}
	names := []string{reportFileName}
	if includeMap {
		names = append(names, deobfuscationMapName)
	} else {
		// A previous successful run must not leave a map that can be mistaken
		// for the map belonging to this output.
		names = append(names, deobfuscationMapName)
	}

	for _, name := range names {
		finalPath := filepath.Join(t.directory, name)
		backupPath, err := backupArtifact(finalPath)
		if err != nil {
			_ = t.rollbackChanges()
			return err
		}
		change := artifactChange{finalPath: finalPath, backupPath: backupPath}
		t.changes = append(t.changes, change)
		if includeMap || name == reportFileName {
			stagedPath := t.Stage(name)
			if _, err := os.Stat(stagedPath); err != nil {
				_ = t.rollbackChanges()
				return fmt.Errorf("staged artifact %s is unavailable: %w", name, err)
			}
			if err := os.Rename(stagedPath, finalPath); err != nil {
				_ = t.rollbackChanges()
				return fmt.Errorf("failed to publish artifact %s: %w", name, err)
			}
		}
	}
	return nil
}

func (t *artifactTransaction) Finalize() error {
	if t == nil {
		return fmt.Errorf("artifact transaction is nil")
	}
	if t.finalized {
		return nil
	}
	var cleanupErr error
	for _, change := range t.changes {
		if change.backupPath != "" {
			if err := os.Remove(change.backupPath); err != nil && !os.IsNotExist(err) {
				cleanupErr = errorsJoin(cleanupErr, fmt.Errorf("failed to remove artifact backup: %w", err))
			}
		}
	}
	t.finalized = true
	if err := os.RemoveAll(t.staging); err != nil {
		cleanupErr = errorsJoin(cleanupErr, err)
	}
	return cleanupErr
}

func (t *artifactTransaction) Rollback() error {
	if t == nil || t.finalized {
		return nil
	}
	err := t.rollbackChanges()
	cleanupErr := os.RemoveAll(t.staging)
	if err != nil {
		return err
	}
	return cleanupErr
}

func (t *artifactTransaction) rollbackChanges() error {
	var rollbackErr error
	for i := len(t.changes) - 1; i >= 0; i-- {
		change := t.changes[i]
		if err := os.Remove(change.finalPath); err != nil && !os.IsNotExist(err) {
			rollbackErr = errorsJoin(rollbackErr, err)
		}
		if change.backupPath != "" {
			if err := os.Rename(change.backupPath, change.finalPath); err != nil {
				rollbackErr = errorsJoin(rollbackErr, err)
			}
		}
	}
	t.changes = nil
	return rollbackErr
}

func backupArtifact(path string) (string, error) {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return "", nil
	} else if err != nil {
		return "", fmt.Errorf("failed to inspect artifact %s: %w", path, err)
	}
	backup, err := os.CreateTemp(filepath.Dir(path), ".must-gather-clean-artifact-backup-*")
	if err != nil {
		return "", fmt.Errorf("failed to create artifact backup: %w", err)
	}
	backupPath := backup.Name()
	if err := backup.Close(); err != nil {
		_ = os.Remove(backupPath)
		return "", fmt.Errorf("failed to close artifact backup: %w", err)
	}
	if err := os.Remove(backupPath); err != nil {
		return "", fmt.Errorf("failed to prepare artifact backup: %w", err)
	}
	if err := os.Rename(path, backupPath); err != nil {
		return "", fmt.Errorf("failed to stage existing artifact %s: %w", path, err)
	}
	return backupPath, nil
}

func errorsJoin(first, second error) error {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return fmt.Errorf("%v; %w", first, second)
}
