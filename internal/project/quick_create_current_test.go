package project

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// TestCurrentForQuickCreateUsesTheDirectoryChain checks that quick create
// finds the current Project from a plain shell inside a worktree, with no
// tmux pane.
func TestCurrentForQuickCreateUsesTheDirectoryChain(t *testing.T) {
	stateDir := t.TempDir()
	dataDir := t.TempDir()
	now := time.Now().UTC()
	root := filepath.Join(dataDir, "projects", "fix-auth-project-")
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "project-current-id", Name: "fix-auth",
		TemplateName: "example", Status: domain.ProjectActive, Root: root,
		Repositories: []domain.ProjectRepository{{Name: "app", Path: filepath.Join(root, "app")}},
		CreatedAt:    now, UpdatedAt: now,
	}
	if err := store.NewProjectStore(stateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir})

	found, err := service.CurrentForQuickCreate(filepath.Join(root, "app"), "", "")
	if err != nil || found.ID != project.ID {
		t.Fatalf("CurrentForQuickCreate from a worktree = %q, error = %v", found.ID, err)
	}

	found, err = service.CurrentForQuickCreate(t.TempDir(), project.ID, "")
	if err != nil || found.ID != project.ID {
		t.Fatalf("CurrentForQuickCreate from the Project ID = %q, error = %v", found.ID, err)
	}

	if _, err = service.CurrentForQuickCreate(t.TempDir(), "", ""); !errors.Is(err, ErrNotInProject) {
		t.Fatalf("CurrentForQuickCreate outside a Project = %v, want ErrNotInProject", err)
	}

	archived := project
	archived.ID = "project-archived-id"
	archived.Name = "old"
	archived.Status = domain.ProjectArchived
	archived.Root = filepath.Join(dataDir, "projects", "old-project-")
	if err := store.NewProjectStore(stateDir).Save(archived); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CurrentForQuickCreate(archived.Root, "", ""); err == nil || errors.Is(err, ErrNotInProject) {
		t.Fatalf("CurrentForQuickCreate in an archived Project = %v, want a status error", err)
	}
}
