package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// OutputTransaction keeps a cleaning run's output out of the final location
// until every processing step has completed successfully.
type OutputTransaction struct {
	FinalPath     string
	StagingPath   string
	originalExist bool
	allowReplace  bool
	committed     bool
}

func BeginOutputTransaction(inputPath, outputPath string, allowReplace bool) (*OutputTransaction, error) {
	if outputPath == "" {
		return nil, fmt.Errorf("output folder must not be empty")
	}
	inputAbsolute, err := filepath.Abs(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve input folder: %w", err)
	}
	inputInfo, err := os.Stat(inputAbsolute)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("input folder does not exist: %w", err)
		}
		return nil, fmt.Errorf("failed to stat input folder: %w", err)
	}
	if !inputInfo.IsDir() {
		return nil, fmt.Errorf("input path is not a directory: %s", inputPath)
	}

	finalPath, err := filepath.Abs(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve output folder: %w", err)
	}
	if finalPath == inputAbsolute {
		return nil, fmt.Errorf("input and output folders must be different")
	}
	resolvedInput, err := ResolvePathForComparison(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve input folder for comparison: %w", err)
	}
	resolvedOutput, err := ResolvePathForComparison(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve output folder for comparison: %w", err)
	}
	if IsPathWithin(resolvedInput, resolvedOutput) || IsPathWithin(resolvedOutput, resolvedInput) {
		return nil, fmt.Errorf("input and output folders must not overlap: %s and %s", inputPath, outputPath)
	}

	parent := filepath.Dir(finalPath)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output parent folder: %w", err)
	}

	originalExist := false
	if info, lstatErr := os.Lstat(finalPath); lstatErr == nil {
		if IsSymbolicLink(info) {
			return nil, fmt.Errorf("output destination must not be a symbolic link: %q", outputPath)
		}
	} else if !os.IsNotExist(lstatErr) {
		return nil, fmt.Errorf("failed to inspect output folder: %w", lstatErr)
	}
	if info, statErr := os.Stat(finalPath); statErr == nil {
		originalExist = true
		if !info.IsDir() {
			return nil, fmt.Errorf("output destination must be a directory: %q", outputPath)
		}
		entries, readErr := os.ReadDir(finalPath)
		if readErr != nil {
			return nil, fmt.Errorf("failed to get contents of output directory %q: %w", outputPath, readErr)
		}
		if len(entries) > 0 && !allowReplace {
			return nil, fmt.Errorf("output directory %s is not empty", outputPath)
		}
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("failed to stat output folder: %w", statErr)
	}

	stagingPath, err := os.MkdirTemp(parent, ".must-gather-clean-output-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary output folder: %w", err)
	}
	if err := os.Chmod(stagingPath, inputInfo.Mode().Perm()); err != nil {
		_ = os.RemoveAll(stagingPath)
		return nil, fmt.Errorf("failed to set temporary output folder permissions: %w", err)
	}
	if err := chown(stagingPath, inputInfo); err != nil {
		_ = os.RemoveAll(stagingPath)
		return nil, fmt.Errorf("failed to set temporary output folder ownership: %w", err)
	}

	return &OutputTransaction{
		FinalPath:     finalPath,
		StagingPath:   stagingPath,
		originalExist: originalExist,
		allowReplace:  allowReplace,
	}, nil
}

func (t *OutputTransaction) Commit() error {
	if t == nil {
		return fmt.Errorf("output transaction is nil")
	}
	if t.committed {
		return fmt.Errorf("output transaction is already committed")
	}

	currentInfo, err := os.Stat(t.FinalPath)
	currentExist := err == nil
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat output folder before commit: %w", err)
	}
	if currentExist && !t.originalExist {
		return fmt.Errorf("output folder was created while the cleaning run was in progress")
	}
	if currentExist && !currentInfo.IsDir() {
		return fmt.Errorf("output destination is no longer a directory: %s", t.FinalPath)
	}
	if currentExist && !t.allowReplace {
		entries, readErr := os.ReadDir(t.FinalPath)
		if readErr != nil {
			return fmt.Errorf("failed to inspect output folder before commit: %w", readErr)
		}
		if len(entries) > 0 {
			return fmt.Errorf("output directory %s became non-empty while the cleaning run was in progress", t.FinalPath)
		}
	}

	backupPath := ""
	if currentExist {
		backupPath, err = os.MkdirTemp(filepath.Dir(t.FinalPath), ".must-gather-clean-backup-*")
		if err != nil {
			return fmt.Errorf("failed to create output backup: %w", err)
		}
		if err := os.Remove(backupPath); err != nil {
			_ = os.RemoveAll(backupPath)
			return fmt.Errorf("failed to prepare output backup: %w", err)
		}
		if err := os.Rename(t.FinalPath, backupPath); err != nil {
			_ = os.RemoveAll(backupPath)
			return fmt.Errorf("failed to stage existing output folder: %w", err)
		}
	}

	if err := os.Rename(t.StagingPath, t.FinalPath); err != nil {
		if backupPath != "" {
			if restoreErr := os.Rename(backupPath, t.FinalPath); restoreErr != nil {
				return fmt.Errorf("failed to publish output folder: %w; failed to restore previous output: %v", err, restoreErr)
			}
		}
		return fmt.Errorf("failed to publish output folder: %w", err)
	}
	if backupPath != "" {
		if err := os.RemoveAll(backupPath); err != nil {
			if restoreErr := rollbackPublishedOutput(t.FinalPath, backupPath); restoreErr != nil {
				return fmt.Errorf("failed to remove output backup: %w; failed to restore previous output: %v", err, restoreErr)
			}
			return fmt.Errorf("failed to remove output backup: %w", err)
		}
	}
	t.committed = true
	return nil
}

func rollbackPublishedOutput(finalPath, backupPath string) error {
	if err := os.RemoveAll(finalPath); err != nil {
		return fmt.Errorf("failed to remove newly published output: %w", err)
	}
	if err := os.Rename(backupPath, finalPath); err != nil {
		return fmt.Errorf("failed to restore previous output: %w", err)
	}
	return nil
}

func (t *OutputTransaction) Cleanup() error {
	if t == nil || t.committed || t.StagingPath == "" {
		return nil
	}
	if err := os.RemoveAll(t.StagingPath); err != nil {
		return fmt.Errorf("failed to remove temporary output folder: %w", err)
	}
	return nil
}
