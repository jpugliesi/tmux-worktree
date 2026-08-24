package workspace

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// TestCurrentForQuickCreateUsesTheDirectoryChain checks that quick create
// finds the current Workspace from a plain shell inside a worktree, with no
// tmux pane.
func TestCurrentForQuickCreateUsesTheDirectoryChain(t *testing.T) {
	stateDir := t.TempDir()
	dataDir := t.TempDir()
	now := time.Now().UTC()
	root := filepath.Join(dataDir, "projects", "fix-auth-workspace-")
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-current-id", Name: "fix-auth",
		TemplateName: "example", Status: domain.WorkspaceActive, Root: root,
		Repositories: []domain.WorkspaceRepository{{Name: "app", Path: filepath.Join(root, "app")}},
		CreatedAt:    now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir})

	found, err := service.CurrentForQuickCreate(filepath.Join(root, "app"), "", "")
	if err != nil || found.ID != workspace.ID {
		t.Fatalf("CurrentForQuickCreate from a worktree = %q, error = %v", found.ID, err)
	}

	found, err = service.CurrentForQuickCreate(t.TempDir(), workspace.ID, "")
	if err != nil || found.ID != workspace.ID {
		t.Fatalf("CurrentForQuickCreate from the Workspace ID = %q, error = %v", found.ID, err)
	}

	if _, err = service.CurrentForQuickCreate(t.TempDir(), "", ""); !errors.Is(err, ErrNotInWorkspace) {
		t.Fatalf("CurrentForQuickCreate outside a Workspace = %v, want ErrNotInWorkspace", err)
	}

	archived := workspace
	archived.ID = "workspace-archived-id"
	archived.Name = "old"
	archived.Status = domain.WorkspaceArchived
	archived.Root = filepath.Join(dataDir, "projects", "old-workspace-")
	if err := store.NewWorkspaceStore(stateDir).Save(archived); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CurrentForQuickCreate(archived.Root, "", ""); err == nil || errors.Is(err, ErrNotInWorkspace) {
		t.Fatalf("CurrentForQuickCreate in an archived Workspace = %v, want a status error", err)
	}
}
