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
	return writeFileAtomicIn(filepath.Dir(path), ".twt2-write-*", path, data, perm, label)
}

// writeFileAtomicIn is the core of WriteFileAtomic. The Transcript Snapshot
// store passes its own temporary directory and name pattern, because its
// cleanup scan finds orphan temporary files by that pattern.
func writeFileAtomicIn(temporaryDirectory, pattern, path string, data []byte, perm os.FileMode, label string) error {
	temporary, err := os.CreateTemp(temporaryDirectory, pattern)
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", label, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(perm); err != nil {
		temporary.Close()
		return fmt.Errorf("set %s permissions: %w", label, err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write %s: %w", label, err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync %s: %w", label, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close %s: %w", label, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("save %s: %w", label, err)
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync %s directory: %w", label, err)
	}
	return nil
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
