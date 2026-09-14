package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/openshift/must-gather-clean/pkg/fsutil"
)

type artifactChange struct {
	finalPath  string
	backupPath string
}

type artifactTransaction struct {
	directory string
	staging   string
	changes   []artifactChange
	// publicationCommitted means the output transaction was committed and the
	// published artifacts must no longer be rolled back. Cleanup can still be
	// retried until finalized becomes true.
	publicationCommitted bool
	finalized            bool
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
	if err := fsutil.EnsurePrivatePath(staging); err != nil {
		_ = os.RemoveAll(staging)
		return nil, err
	}
	return &artifactTransaction{directory: directory, staging: staging}, nil
}

func (t *artifactTransaction) Stage(name string) string {
	return filepath.Join(t.staging, name)
}

// Publish makes staged artifacts visible while keeping enough information to
// restore the previous report if output publication fails. A run-scoped map
// is published without replacing an existing file, so a previous run's
// recovery artifact cannot be lost.
func (t *artifactTransaction) Publish(includeMap bool, mapNames ...string) error {
	if t == nil || t.finalized || t.publicationCommitted {
		return fmt.Errorf("artifact transaction is unavailable")
	}
	names := []string{reportFileName}
	mapName := ""
	if includeMap {
		if len(mapNames) != 1 || mapNames[0] == "" {
			return fmt.Errorf("run-scoped deobfuscation map name is required")
		}
		mapName = mapNames[0]
		names = append(names, mapName)
	}

	for _, name := range names {
		stagedPath := t.Stage(name)
		if mapName == name {
			change := artifactChange{finalPath: filepath.Join(t.directory, name)}
			if err := publishArtifactWithoutOverwrite(stagedPath, change.finalPath); err != nil {
				return t.publishErrorWithRollback(err)
			}
			t.changes = append(t.changes, change)
			if err := fsutil.EnsurePrivatePath(change.finalPath); err != nil {
				return t.publishErrorWithRollback(fmt.Errorf("failed to secure published artifact %s: %w", name, err))
			}
			continue
		}

		finalPath := filepath.Join(t.directory, name)
		backupPath, err := backupArtifact(finalPath)
		if err != nil {
			return t.publishErrorWithRollback(err)
		}
		change := artifactChange{finalPath: finalPath, backupPath: backupPath}
		t.changes = append(t.changes, change)
		if _, err := os.Stat(stagedPath); err != nil {
			return t.publishErrorWithRollback(fmt.Errorf("staged artifact %s is unavailable: %w", name, err))
		}
		if err := os.Rename(stagedPath, finalPath); err != nil {
			return t.publishErrorWithRollback(fmt.Errorf("failed to publish artifact %s: %w", name, err))
		}
		if err := fsutil.EnsurePrivatePath(finalPath); err != nil {
			return t.publishErrorWithRollback(fmt.Errorf("failed to secure published artifact %s: %w", name, err))
		}
	}
	return nil
}

func (t *artifactTransaction) publishErrorWithRollback(publicationErr error) error {
	if rollbackErr := t.rollbackChanges(); rollbackErr != nil {
		return errorsJoin(publicationErr, fmt.Errorf("failed to roll back artifact publication: %w", rollbackErr))
	}
	return publicationErr
}

// publishArtifactWithoutOverwrite creates the final file with O_EXCL before
// copying the staged map. This is portable across the supported platforms and
// fails rather than replacing a previous run's map, even if another process
// creates the path after a prior check.
func publishArtifactWithoutOverwrite(stagedPath, finalPath string) error {
	input, err := os.Open(stagedPath)
	if err != nil {
		return fmt.Errorf("staged artifact %s is unavailable: %w", filepath.Base(stagedPath), err)
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(finalPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("failed to publish artifact %s without overwrite: %w", finalPath, err)
	}
	if err := fsutil.EnsurePrivatePath(finalPath); err != nil {
		_ = output.Close()
		_ = os.Remove(finalPath)
		return fmt.Errorf("failed to secure artifact %s before publishing: %w", finalPath, err)
	}
	copyErr := func() error {
		if _, err := io.Copy(output, input); err != nil {
			return fmt.Errorf("failed to copy staged artifact %s: %w", finalPath, err)
		}
		if err := output.Sync(); err != nil {
			return fmt.Errorf("failed to sync staged artifact %s: %w", finalPath, err)
		}
		if err := output.Close(); err != nil {
			return fmt.Errorf("failed to close staged artifact %s: %w", finalPath, err)
		}
		return nil
	}()
	if copyErr != nil {
		_ = output.Close()
		_ = os.Remove(finalPath)
		return copyErr
	}
	if err := os.Remove(stagedPath); err != nil {
		_ = os.Remove(finalPath)
		return fmt.Errorf("failed to finalize artifact %s: %w", finalPath, err)
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
	// Finalize is called only after the output transaction has committed. From
	// this point on, cleanup failures must not make Rollback remove a valid
	// published output.
	t.publicationCommitted = true
	var cleanupErr error
	for _, change := range t.changes {
		if change.backupPath != "" {
			if err := os.Remove(change.backupPath); err != nil && !os.IsNotExist(err) {
				cleanupErr = errorsJoin(cleanupErr, fmt.Errorf("failed to remove artifact backup: %w", err))
			}
		}
	}
	if err := os.RemoveAll(t.staging); err != nil {
		cleanupErr = errorsJoin(cleanupErr, err)
	}
	if cleanupErr == nil {
		t.finalized = true
		t.changes = nil
	}
	return cleanupErr
}

func (t *artifactTransaction) Rollback() error {
	if t == nil || t.finalized || t.publicationCommitted {
		return nil
	}
	err := t.rollbackChanges()
	cleanupErr := os.RemoveAll(t.staging)
	return errorsJoin(err, cleanupErr)
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
			} else if err := fsutil.EnsurePrivatePath(change.finalPath); err != nil {
				rollbackErr = errorsJoin(rollbackErr, fmt.Errorf("failed to secure restored artifact %s: %w", change.finalPath, err))
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
