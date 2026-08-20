package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	snapshotMarkerName      = ".twt2-snapshot.json"
	snapshotTemporaryPrefix = ".twt2-snapshot-"
)

type SnapshotStore struct {
	stateDir string
	root     string
}

type SnapshotInfo struct {
	ProjectID string
	Directory string
	Bytes     int64
}

type SnapshotTemporaryFile struct {
	Path  string
	Bytes int64
}

type snapshotMarker struct {
	Version   int    `json:"version"`
	Owner     string `json:"owner"`
	ProjectID string `json:"projectId"`
}

func NewSnapshotStore(stateDir string) SnapshotStore {
	return SnapshotStore{stateDir: stateDir, root: filepath.Join(stateDir, "snapshots", "projects")}
}

func (s SnapshotStore) ProjectDir(projectID string) (string, error) {
	if err := ValidateResourceName(projectID); err != nil {
		return "", fmt.Errorf("invalid Project ID: %w", err)
	}
	return filepath.Join(s.root, projectID), nil
}

func (s SnapshotStore) Save(projectID, markdown string) error {
	if markdown == "" {
		return fmt.Errorf("Transcript Snapshot is empty")
	}
	directory, err := s.ProjectDir(projectID)
	if err != nil {
		return err
	}
	if err := s.ensureRoots(); err != nil {
		return err
	}
	initialize := false
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return fmt.Errorf("create Transcript Snapshot directory: %w", err)
		}
		initialize = true
	} else if err != nil {
		return fmt.Errorf("inspect Transcript Snapshot directory: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("Transcript Snapshot directory %q is not a safe directory", directory)
	} else {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return fmt.Errorf("read Transcript Snapshot directory: %w", err)
		}
		initialize = len(entries) == 0
		if !initialize {
			if _, err := s.ValidateProject(projectID, false); err != nil {
				return err
			}
		}
	}
	if initialize {
		marker, err := json.Marshal(snapshotMarker{Version: 1, Owner: "twt2", ProjectID: projectID})
		if err != nil {
			return fmt.Errorf("encode Transcript Snapshot ownership marker: %w", err)
		}
		if err := writeSnapshotFile(s.root, filepath.Join(directory, snapshotMarkerName), append(marker, '\n')); err != nil {
			return err
		}
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("protect Transcript Snapshot directory: %w", err)
	}
	return writeSnapshotFile(s.root, filepath.Join(directory, "latest.md"), []byte(markdown))
}

func (s SnapshotStore) ValidateProject(projectID string, allowEmpty bool) (bool, error) {
	directory, err := s.ProjectDir(projectID)
	if err != nil {
		return false, err
	}
	rootsExist, err := s.validateRoots()
	if err != nil || !rootsExist {
		return false, err
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect Transcript Snapshot directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("Transcript Snapshot directory %q is not a safe directory", directory)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false, fmt.Errorf("read Transcript Snapshot directory: %w", err)
	}
	if len(entries) == 0 && allowEmpty {
		return true, nil
	}
	allowed := map[string]bool{snapshotMarkerName: true, "latest.md": true}
	markerFound := false
	for _, entry := range entries {
		if !allowed[entry.Name()] {
			return false, fmt.Errorf("Transcript Snapshot directory %q contains unexpected item %q", directory, entry.Name())
		}
		entryInfo, err := os.Lstat(filepath.Join(directory, entry.Name()))
		if err != nil {
			return false, fmt.Errorf("inspect Transcript Snapshot item %q: %w", entry.Name(), err)
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 || !entryInfo.Mode().IsRegular() {
			return false, fmt.Errorf("Transcript Snapshot item %q is not a safe regular file", entry.Name())
		}
		markerFound = markerFound || entry.Name() == snapshotMarkerName
	}
	if !markerFound {
		return false, fmt.Errorf("Transcript Snapshot directory %q has no ownership marker", directory)
	}
	data, err := os.ReadFile(filepath.Join(directory, snapshotMarkerName))
	if err != nil {
		return false, fmt.Errorf("read Transcript Snapshot ownership marker: %w", err)
	}
	var marker snapshotMarker
	if json.Unmarshal(data, &marker) != nil || marker.Version != 1 || marker.Owner != "twt2" || marker.ProjectID != projectID {
		return false, fmt.Errorf("Transcript Snapshot directory %q has a conflicting ownership marker", directory)
	}
	return true, nil
}

func (s SnapshotStore) List() ([]SnapshotInfo, error) {
	rootsExist, err := s.validateRoots()
	if err != nil || !rootsExist {
		return []SnapshotInfo{}, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("list Transcript Snapshots: %w", err)
	}
	result := []SnapshotInfo{}
	for _, entry := range entries {
		if ValidateResourceName(entry.Name()) != nil {
			continue
		}
		exists, err := s.ValidateProject(entry.Name(), false)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		directory, err := s.ProjectDir(entry.Name())
		if err != nil {
			return nil, err
		}
		bytes := int64(0)
		for _, name := range []string{snapshotMarkerName, "latest.md"} {
			info, err := os.Lstat(filepath.Join(directory, name))
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("measure Transcript Snapshot: %w", err)
			}
			bytes += info.Size()
		}
		result = append(result, SnapshotInfo{ProjectID: entry.Name(), Directory: directory, Bytes: bytes})
	}
	return result, nil
}

func (s SnapshotStore) DeleteProject(projectID string, allowEmpty bool) error {
	exists, err := s.ValidateProject(projectID, allowEmpty)
	if err != nil || !exists {
		return err
	}
	directory, err := s.ProjectDir(projectID)
	if err != nil {
		return err
	}
	for _, name := range []string{"latest.md", snapshotMarkerName} {
		if err := os.Remove(filepath.Join(directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove Transcript Snapshot item %q: %w", name, err)
		}
	}
	if err := os.Remove(directory); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove Transcript Snapshot directory: %w", err)
	}
	return nil
}

func (s SnapshotStore) ListTemporaryFiles() ([]SnapshotTemporaryFile, error) {
	rootsExist, err := s.validateRoots()
	if err != nil || !rootsExist {
		return []SnapshotTemporaryFile{}, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("list temporary Transcript Snapshots: %w", err)
	}
	result := []SnapshotTemporaryFile{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), snapshotTemporaryPrefix) {
			continue
		}
		path := filepath.Join(s.root, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect temporary Transcript Snapshot %q: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("temporary Transcript Snapshot %q is not a safe regular file", path)
		}
		result = append(result, SnapshotTemporaryFile{Path: path, Bytes: info.Size()})
	}
	return result, nil
}

func (s SnapshotStore) DeleteTemporaryFile(path string) error {
	if filepath.Dir(filepath.Clean(path)) != filepath.Clean(s.root) || !strings.HasPrefix(filepath.Base(path), snapshotTemporaryPrefix) {
		return fmt.Errorf("temporary Transcript Snapshot path %q is outside the owned Snapshot root", path)
	}
	rootsExist, err := s.validateRoots()
	if err != nil {
		return err
	}
	if !rootsExist {
		return nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect temporary Transcript Snapshot %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("temporary Transcript Snapshot %q is not a safe regular file", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove temporary Transcript Snapshot %q: %w", path, err)
	}
	return nil
}

func writeSnapshotFile(temporaryDirectory, path string, data []byte) error {
	temporary, err := os.CreateTemp(temporaryDirectory, snapshotTemporaryPrefix+"*")
	if err != nil {
		return fmt.Errorf("create temporary Transcript Snapshot: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect temporary Transcript Snapshot: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write Transcript Snapshot: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync Transcript Snapshot: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Transcript Snapshot: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("save Transcript Snapshot: %w", err)
	}
	return nil
}

func (s SnapshotStore) ensureRoots() error {
	if err := os.MkdirAll(s.stateDir, 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	for _, directory := range []string{filepath.Join(s.stateDir, "snapshots"), s.root} {
		if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create Transcript Snapshot state directory: %w", err)
		}
		info, err := os.Lstat(directory)
		if err != nil {
			return fmt.Errorf("inspect Transcript Snapshot state directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("Transcript Snapshot state directory %q is not a safe directory", directory)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return fmt.Errorf("protect Transcript Snapshot state directory: %w", err)
		}
	}
	return nil
}

func (s SnapshotStore) validateRoots() (bool, error) {
	for _, directory := range []string{filepath.Join(s.stateDir, "snapshots"), s.root} {
		info, err := os.Lstat(directory)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("inspect Transcript Snapshot state directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return false, fmt.Errorf("Transcript Snapshot state directory %q is not a safe directory", directory)
		}
	}
	return true, nil
}
