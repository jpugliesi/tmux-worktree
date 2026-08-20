package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

const (
	snapshotMarkerName      = ".twt2-snapshot.json"
	snapshotTemporaryPrefix = ".twt2-snapshot-"
	snapshotLatestName      = "latest.md"
	snapshotAgentsDirName   = "agents"
)

// snapshotAgentID matches the hexadecimal Agent Session ID that twt2 makes.
// One Agent Session snapshot file uses this name and the ".md" suffix.
var snapshotAgentID = regexp.MustCompile(`^[0-9a-f]{4,64}$`)

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

// SnapshotPaths gives the files that one Transcript Snapshot write makes.
// Agent is the private file of one Agent Session. Latest is a plain copy of
// the most recent Transcript Snapshot of the Project.
type SnapshotPaths struct {
	Agent  string
	Latest string
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

// AgentPath gives the private Transcript Snapshot file of one Agent Session.
func (s SnapshotStore) AgentPath(projectID, agentID string) (string, error) {
	directory, err := s.ProjectDir(projectID)
	if err != nil {
		return "", err
	}
	if !snapshotAgentID.MatchString(agentID) {
		return "", fmt.Errorf("invalid Agent Session ID %q", agentID)
	}
	return filepath.Join(directory, snapshotAgentsDirName, agentID+".md"), nil
}

// Save writes the Transcript Snapshot of one Agent Session. It also writes
// latest.md as a plain copy of this most recent snapshot.
func (s SnapshotStore) Save(projectID, agentID, markdown string) (SnapshotPaths, error) {
	if markdown == "" {
		return SnapshotPaths{}, fmt.Errorf("Transcript Snapshot is empty")
	}
	directory, err := s.ProjectDir(projectID)
	if err != nil {
		return SnapshotPaths{}, err
	}
	agentPath, err := s.AgentPath(projectID, agentID)
	if err != nil {
		return SnapshotPaths{}, err
	}
	if err := s.ensureRoots(); err != nil {
		return SnapshotPaths{}, err
	}
	initialize := false
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return SnapshotPaths{}, fmt.Errorf("create Transcript Snapshot directory: %w", err)
		}
		initialize = true
	} else if err != nil {
		return SnapshotPaths{}, fmt.Errorf("inspect Transcript Snapshot directory: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return SnapshotPaths{}, fmt.Errorf("Transcript Snapshot directory %q is not a safe directory", directory)
	} else {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return SnapshotPaths{}, fmt.Errorf("read Transcript Snapshot directory: %w", err)
		}
		initialize = len(entries) == 0
		if !initialize {
			if _, err := s.ValidateProject(projectID, false); err != nil {
				return SnapshotPaths{}, err
			}
		}
	}
	if initialize {
		marker, err := json.Marshal(snapshotMarker{Version: 1, Owner: "twt2", ProjectID: projectID})
		if err != nil {
			return SnapshotPaths{}, fmt.Errorf("encode Transcript Snapshot ownership marker: %w", err)
		}
		if err := writeSnapshotFile(s.root, filepath.Join(directory, snapshotMarkerName), append(marker, '\n')); err != nil {
			return SnapshotPaths{}, err
		}
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return SnapshotPaths{}, fmt.Errorf("protect Transcript Snapshot directory: %w", err)
	}
	if err := s.ensureAgentsDir(filepath.Dir(agentPath)); err != nil {
		return SnapshotPaths{}, err
	}
	if err := writeSnapshotFile(s.root, agentPath, []byte(markdown)); err != nil {
		return SnapshotPaths{}, err
	}
	latestPath := filepath.Join(directory, snapshotLatestName)
	if err := writeSnapshotFile(s.root, latestPath, []byte(markdown)); err != nil {
		return SnapshotPaths{}, err
	}
	return SnapshotPaths{Agent: agentPath, Latest: latestPath}, nil
}

func (s SnapshotStore) ensureAgentsDir(directory string) error {
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create Agent Transcript Snapshot directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("inspect Agent Transcript Snapshot directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("Agent Transcript Snapshot directory %q is not a safe directory", directory)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("protect Agent Transcript Snapshot directory: %w", err)
	}
	return nil
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
		return false, clierr.New(clierr.UnsafeState, "Transcript Snapshot directory %q is not a safe directory", directory)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return false, fmt.Errorf("read Transcript Snapshot directory: %w", err)
	}
	if len(entries) == 0 && allowEmpty {
		return true, nil
	}
	markerFound := false
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(directory, name)
		entryInfo, err := os.Lstat(path)
		if err != nil {
			return false, fmt.Errorf("inspect Transcript Snapshot item %q: %w", name, err)
		}
		switch name {
		case snapshotMarkerName, snapshotLatestName:
			if entryInfo.Mode()&os.ModeSymlink != 0 || !entryInfo.Mode().IsRegular() {
				return false, clierr.New(clierr.UnsafeState, "Transcript Snapshot item %q is not a safe regular file", name)
			}
			markerFound = markerFound || name == snapshotMarkerName
		case snapshotAgentsDirName:
			if entryInfo.Mode()&os.ModeSymlink != 0 || !entryInfo.IsDir() {
				return false, clierr.New(clierr.UnsafeState, "Transcript Snapshot item %q is not a safe directory", name)
			}
			if err := validateSnapshotAgents(path); err != nil {
				return false, err
			}
		default:
			return false, clierr.New(clierr.UnsafeState, "Transcript Snapshot directory %q contains unexpected item %q", directory, name)
		}
	}
	if !markerFound {
		return false, clierr.New(clierr.UnsafeState, "Transcript Snapshot directory %q has no ownership marker", directory)
	}
	data, err := os.ReadFile(filepath.Join(directory, snapshotMarkerName))
	if err != nil {
		return false, fmt.Errorf("read Transcript Snapshot ownership marker: %w", err)
	}
	var marker snapshotMarker
	if json.Unmarshal(data, &marker) != nil || marker.Version != 1 || marker.Owner != "twt2" || marker.ProjectID != projectID {
		return false, clierr.New(clierr.UnsafeState, "Transcript Snapshot directory %q has a conflicting ownership marker", directory)
	}
	return true, nil
}

// validateSnapshotAgents accepts only Agent Session snapshot files. Each name
// must be a hexadecimal Agent Session ID with the ".md" suffix, and each item
// must be a regular file.
func validateSnapshotAgents(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read Agent Transcript Snapshot directory: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") || !snapshotAgentID.MatchString(strings.TrimSuffix(name, ".md")) {
			return clierr.New(clierr.UnsafeState, "Agent Transcript Snapshot directory %q contains unexpected item %q", directory, name)
		}
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil {
			return fmt.Errorf("inspect Agent Transcript Snapshot item %q: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return clierr.New(clierr.UnsafeState, "Agent Transcript Snapshot item %q is not a safe regular file", name)
		}
	}
	return nil
}

// snapshotAgentNames lists the validated Agent Session snapshot file names of
// one Project directory.
func snapshotAgentNames(directory string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(directory, snapshotAgentsDirName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Agent Transcript Snapshot directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, filepath.Join(snapshotAgentsDirName, entry.Name()))
	}
	return names, nil
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
		agentNames, err := snapshotAgentNames(directory)
		if err != nil {
			return nil, err
		}
		bytes := int64(0)
		for _, name := range append([]string{snapshotMarkerName, snapshotLatestName}, agentNames...) {
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
	agentNames, err := snapshotAgentNames(directory)
	if err != nil {
		return err
	}
	names := append(agentNames, snapshotAgentsDirName, snapshotLatestName, snapshotMarkerName)
	for _, name := range names {
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
