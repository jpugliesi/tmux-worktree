package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

type WorkspaceStore struct {
	dir string
}

func NewWorkspaceStore(stateDir string) WorkspaceStore {
	// Keep the version-one directory name. It is a private storage detail,
	// and keeping it makes the public rename safe for existing records.
	return WorkspaceStore{dir: filepath.Join(stateDir, "projects")}
}

func (s WorkspaceStore) Save(workspace domain.Workspace) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create Workspace state directory: %w", err)
	}
	data, err := json.MarshalIndent(workspace, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Workspace state: %w", err)
	}
	data = append(data, '\n')
	return WriteFileAtomic(filepath.Join(s.dir, workspace.ID+".json"), data, 0o600, "Workspace state")
}

func (s WorkspaceStore) List() ([]domain.Workspace, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.Workspace{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Workspace state directory: %w", err)
	}
	workspaces := make([]domain.Workspace, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		workspace, err := s.loadPath(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		workspaces = append(workspaces, workspace)
	}
	sort.Slice(workspaces, func(i, j int) bool { return workspaces[i].CreatedAt.Before(workspaces[j].CreatedAt) })
	return workspaces, nil
}

func (s WorkspaceStore) Find(reference string) (domain.Workspace, error) {
	if ValidateResourceName(reference) == nil {
		workspace, err := s.loadPath(filepath.Join(s.dir, reference+".json"))
		if err == nil && workspace.ID == reference {
			return workspace, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return domain.Workspace{}, err
		}
	}
	workspaces, err := s.List()
	if err != nil {
		return domain.Workspace{}, err
	}
	var match *domain.Workspace
	for index := range workspaces {
		if workspaces[index].ID == reference || workspaces[index].Name == reference {
			if match != nil && workspaces[index].Name == reference {
				return domain.Workspace{}, fmt.Errorf("Workspace name %q is ambiguous; use a Workspace ID", reference)
			}
			copy := workspaces[index]
			match = &copy
		}
	}
	if match == nil {
		return domain.Workspace{}, clierr.New(clierr.NotFound, "Workspace %q does not exist", reference)
	}
	return *match, nil
}

func (s WorkspaceStore) Delete(id string) error {
	if err := ValidateResourceName(id); err != nil {
		return fmt.Errorf("invalid Workspace ID: %w", err)
	}
	path := filepath.Join(s.dir, id+".json")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete Workspace state: %w", err)
	}
	return nil
}

func (s WorkspaceStore) loadPath(path string) (domain.Workspace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("read Workspace state: %w", err)
	}
	var workspace domain.Workspace
	if err := json.Unmarshal(data, &workspace); err != nil {
		return domain.Workspace{}, fmt.Errorf("decode Workspace state %q: %w", path, err)
	}
	if workspace.Version != domain.WorkspaceVersion {
		return domain.Workspace{}, fmt.Errorf("Workspace %q uses unsupported state version %d", workspace.Name, workspace.Version)
	}
	return workspace, nil
}
