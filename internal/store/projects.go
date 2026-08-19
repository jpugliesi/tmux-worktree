package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

type ProjectStore struct {
	dir string
}

func NewProjectStore(stateDir string) ProjectStore {
	return ProjectStore{dir: filepath.Join(stateDir, "projects")}
}

func (s ProjectStore) Save(project domain.Project) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create Project state directory: %w", err)
	}
	data, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Project state: %w", err)
	}
	data = append(data, '\n')
	path := filepath.Join(s.dir, project.ID+".json")
	temporary, err := os.CreateTemp(s.dir, ".twt2-project-*")
	if err != nil {
		return fmt.Errorf("create temporary Project state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("set Project state permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write Project state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync Project state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Project state: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("save Project state: %w", err)
	}
	return nil
}

func (s ProjectStore) List() ([]domain.Project, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.Project{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Project state directory: %w", err)
	}
	projects := make([]domain.Project, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		project, err := s.loadPath(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].CreatedAt.Before(projects[j].CreatedAt) })
	return projects, nil
}

func (s ProjectStore) Find(reference string) (domain.Project, error) {
	projects, err := s.List()
	if err != nil {
		return domain.Project{}, err
	}
	var match *domain.Project
	for index := range projects {
		if projects[index].ID == reference || projects[index].Name == reference {
			if match != nil && projects[index].Name == reference {
				return domain.Project{}, fmt.Errorf("Project name %q is ambiguous; use a Project ID", reference)
			}
			copy := projects[index]
			match = &copy
		}
	}
	if match == nil {
		return domain.Project{}, fmt.Errorf("Project %q does not exist", reference)
	}
	return *match, nil
}

func (s ProjectStore) Delete(id string) error {
	if err := ValidateResourceName(id); err != nil {
		return fmt.Errorf("invalid Project ID: %w", err)
	}
	path := filepath.Join(s.dir, id+".json")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete Project state: %w", err)
	}
	return nil
}

func (s ProjectStore) loadPath(path string) (domain.Project, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.Project{}, fmt.Errorf("read Project state: %w", err)
	}
	var project domain.Project
	if err := json.Unmarshal(data, &project); err != nil {
		return domain.Project{}, fmt.Errorf("decode Project state %q: %w", path, err)
	}
	if project.Version != domain.ProjectVersion {
		return domain.Project{}, fmt.Errorf("Project %q uses unsupported state version %d", project.Name, project.Version)
	}
	return project, nil
}
