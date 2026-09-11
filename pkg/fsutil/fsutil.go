package fsutil

import (
	"fmt"
	"io/fs"
	"k8s.io/klog/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func IsSymbolicLink(fileInfo fs.FileInfo) bool {
	return (fileInfo.Mode() & fs.ModeSymlink) == fs.ModeSymlink
}

func Relink(readPath string, writePath string, readPathStat os.FileInfo) error {
	src, err := os.Readlink(readPath)
	if err != nil {
		return fmt.Errorf("failed to read link in %s: %w", readPath, err)
	}

	// we try to link once, if it fails on a link error we will try by shelling out, otherwise place a file with the original linkage
	err = os.Symlink(src, writePath)
	if err == nil {
		return nil
	} else {
		klog.V(1).Infof("could not link '%s' to '%s', trying shell instead. Error was: %v", src, writePath, err)
	}

	cmd := exec.Command("cp", "--preserve=links", "--no-dereference", src, writePath)
	err = cmd.Run()
	if err == nil {
		return nil
	} else if ee, ok := err.(*exec.ExitError); ok {
		klog.V(1).Infof("could not link '%s' to '%s' via shell, writing file instead. Error was: %v", src, writePath, ee)
	}

	err = os.WriteFile(writePath, []byte(src), readPathStat.Mode())
	if err != nil {
		return fmt.Errorf("failed to link from %s to %s: %w", readPath, writePath, err)
	}

	err = chown(writePath, readPathStat)
	if err != nil {
		return err
	}

	return nil
}

func EnsureInputOutputPath(inputPath string, outputPath string, deleteOutputFolder bool) error {
	_, err := os.Stat(inputPath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("input folder does not exist: %w", err)
		}
		return fmt.Errorf("failed to stat input folder: %w", err)
	}

	err = ensureOutputPath(outputPath, deleteOutputFolder, inputPath)
	if err != nil {
		return fmt.Errorf("failed to ensure output folder: %w", err)
	}

	return nil
}

func CreateNonConflictingFile(outputFilePath string, inputFileInfo os.FileInfo) (*os.File, error) {
	// A path collision means that two input files would be represented by the
	// same cleaned path. Appending a suffix loses the original path identity and
	// cannot be represented in the deobfuscation map, so fail instead.
	outputOsFile, err := os.OpenFile(outputFilePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, inputFileInfo.Mode())
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("output path collision at %s", outputFilePath)
		}
		return nil, fmt.Errorf("failed to create and open '%s': %w", outputFilePath, err)
	}

	err = chown(outputFilePath, inputFileInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to chown after opening '%s': %w", outputFilePath, err)
	}

	return outputOsFile, nil
}

// ResolvePathForComparison resolves all existing symlink components while
// preserving the non-existent suffix. It is used for safety checks before a
// path is created, so lexical filepath.Rel comparisons cannot be bypassed by
// aliases or symlinks.
func ResolvePathForComparison(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	current := filepath.Clean(abs)
	var missing []string
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("could not resolve path %s", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// IsPathWithin reports whether child is the same as parent or lies below it.
// Both paths must already be resolved with ResolvePathForComparison.
func IsPathWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

// MkdirAllWithChown is a modified os.MkdirAll that creates perms according to the input folder hierarchy, from bottom to top
func MkdirAllWithChown(pathToCreate string, existingInputPath string) error {
	// short-cut in case the pathToCreate already exists
	_, err := os.Lstat(pathToCreate)
	if err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	// ensure the parent exists recursively
	parentPath := filepath.Dir(pathToCreate)
	_, err = os.Lstat(parentPath)
	if err != nil {
		if os.IsNotExist(err) {
			inputParentPath := filepath.Dir(existingInputPath)
			err = MkdirAllWithChown(parentPath, inputParentPath)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	perm, err := os.Lstat(existingInputPath)
	if err != nil {
		return err
	}
	err = os.Mkdir(pathToCreate, perm.Mode())
	if err != nil {
		// might've been created by another goroutine in the meantime -- okay to proceed
		if !os.IsExist(err) {
			return err
		}
	}

	err = chown(pathToCreate, perm)
	if err != nil {
		return fmt.Errorf("failed to create directory %s: %w", pathToCreate, err)
	}
	return nil
}

func ensureOutputPath(path string, deleteIfExists bool, inputFolderPath string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return MkdirAllWithChown(path, inputFolderPath)
		}
		return err
	}

	if !info.IsDir() {
		return fmt.Errorf("output destination must be a directory: '%s'", path)
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("failed to get contents of output directory '%s': %w", path, err)
	}

	if len(entries) != 0 {
		if deleteIfExists {
			err = os.RemoveAll(path)
			if err != nil {
				return fmt.Errorf("error while deleting the output path '%s': %w", path, err)
			}
		} else {
			return fmt.Errorf("output directory %s is not empty", path)
		}
	}

	return MkdirAllWithChown(path, inputFolderPath)
}
