package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestWorkspaceFindByIDDoesNotReadOtherWorkspaceFiles(t *testing.T) {
	stateDir := t.TempDir()
	workspaces := NewWorkspaceStore(stateDir)
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-one", Name: "alpha",
		Status: domain.WorkspaceActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := workspaces.Save(workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "projects", "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := workspaces.Find("workspace-one")
	if err != nil {
		t.Fatalf("Find() by ID = %v", err)
	}
	if got.ID != workspace.ID || got.Name != workspace.Name {
		t.Fatalf("Find() = %+v", got)
	}

	if _, err := workspaces.List(); err == nil {
		t.Fatal("List() succeeded with a broken sibling file")
	}
}
