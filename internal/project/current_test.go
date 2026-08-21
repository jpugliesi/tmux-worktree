package project

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestCurrentFindsOwnedProjectWithoutListingOthers(t *testing.T) {
	stateDir := t.TempDir()
	root := t.TempDir()
	now := time.Now().UTC()
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "project-owned", Name: "owned",
		Status: domain.ProjectActive, Root: root,
		Repositories: []domain.ProjectRepository{{Name: "app", Path: filepath.Join(root, "app")}},
		CreatedAt:    now, UpdatedAt: now,
	}
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.NewProjectStore(stateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".twt-owned.json"), []byte("{\"owner\":\"twt\",\"projectId\":\"project-owned\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "projects", "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := NewService(Options{StateDir: stateDir, DataDir: t.TempDir()}).Current(filepath.Join(root, "app"), "", "")
	if err != nil || got.ID != project.ID {
		t.Fatalf("Current() = %+v, %v", got, err)
	}
}

func TestCurrentFindsAnAdoptedProjectFromASecondRepository(t *testing.T) {
	stateDir := t.TempDir()
	first := t.TempDir()
	second := t.TempDir()
	now := time.Now().UTC()
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "project-adopted", Name: "dev-env",
		Status: domain.ProjectActive, Adopted: true, Root: first,
		Repositories: []domain.ProjectRepository{
			{Name: "first", Path: first},
			{Name: "second", Path: second},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewProjectStore(stateDir).Save(project); err != nil {
		t.Fatal(err)
	}

	got, err := NewService(Options{StateDir: stateDir, DataDir: t.TempDir()}).FindByDirectory(filepath.Join(second, "src"))
	if err != nil || got.ID != project.ID {
		t.Fatalf("FindByDirectory() in a second repository = %+v, %v", got, err)
	}
}
