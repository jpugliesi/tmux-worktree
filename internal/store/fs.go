package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path with a temporary file and a rename, so
// a reader sees the complete old content or the complete new content. It
// syncs the file and its directory. label names the content in error
// messages.
func WriteFileAtomic(path string, data []byte, perm os.FileMode, label string) error {
	return writeFileAtomicIn(filepath.Dir(path), ".twt-write-*", path, data, perm, label)
}

// WriteFileExclusiveAtomic writes a new file without replacing an existing
// destination. A hard link publishes a complete temporary file and gives the
// operation the O_EXCL property that os.Rename does not provide.
func WriteFileExclusiveAtomic(path string, data []byte, perm os.FileMode, label string) error {
	directory := filepath.Dir(path)
	temporaryPath, err := writeTemporaryFile(directory, ".twt-write-*", data, perm, label)
	if err != nil {
		return err
	}
	defer os.Remove(temporaryPath)
	if err := os.Link(temporaryPath, path); err != nil {
		return fmt.Errorf("save new %s: %w", label, err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		rollbackErr := os.Remove(path)
		if rollbackErr != nil {
			return fmt.Errorf("remove temporary %s: %w; remove rollback destination %q: %v", label, err, path, rollbackErr)
		}
		return fmt.Errorf("remove temporary %s: %w", label, err)
	}
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("sync %s directory: %w", label, err)
	}
	return nil
}

// writeFileAtomicIn is the core of WriteFileAtomic. The Transcript Snapshot
// store passes its own temporary directory and name pattern, because its
// cleanup scan finds orphan temporary files by that pattern.
func writeFileAtomicIn(temporaryDirectory, pattern, path string, data []byte, perm os.FileMode, label string) error {
	temporaryPath, err := writeTemporaryFile(temporaryDirectory, pattern, data, perm, label)
	if err != nil {
		return err
	}
	defer os.Remove(temporaryPath)
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("save %s: %w", label, err)
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync %s directory: %w", label, err)
	}
	return nil
}

func writeTemporaryFile(directory, pattern string, data []byte, perm os.FileMode, label string) (string, error) {
	temporary, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", fmt.Errorf("create temporary %s: %w", label, err)
	}
	temporaryPath := temporary.Name()
	if err := temporary.Chmod(perm); err != nil {
		temporary.Close()
		os.Remove(temporaryPath)
		return "", fmt.Errorf("set %s permissions: %w", label, err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		os.Remove(temporaryPath)
		return "", fmt.Errorf("write %s: %w", label, err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		os.Remove(temporaryPath)
		return "", fmt.Errorf("sync %s: %w", label, err)
	}
	if err := temporary.Close(); err != nil {
		os.Remove(temporaryPath)
		return "", fmt.Errorf("close %s: %w", label, err)
	}
	return temporaryPath, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

// DirectoryBytes returns the total size of the regular files under root. A
// directory that does not exist has size zero.
func DirectoryBytes(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("measure %q: %w", root, err)
	}
	return total, nil
}
