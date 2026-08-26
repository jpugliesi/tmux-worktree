package workspace

import (
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestRenameChangesOnlyTheWorkspaceName(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Now().UTC().Add(-time.Hour)
	want := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-id", Name: "old-name",
		Status: domain.WorkspaceActive, Root: "/tmp/old-name", TmuxSession: "template-old-name",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(stateDir).Save(want); err != nil {
		t.Fatal(err)
	}
	service := NewService(Options{StateDir: stateDir})

	got, err := service.Rename(want.ID, "new-name")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "new-name" || got.ID != want.ID || got.Root != want.Root || got.TmuxSession != want.TmuxSession {
		t.Fatalf("Rename() = %+v", got)
	}
	if _, err := service.Find("old-name"); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("Find(old-name) = %v", err)
	}
}

func TestValidateRenameRejectsAnExistingName(t *testing.T) {
	stateDir := t.TempDir()
	workspaceStore := store.NewWorkspaceStore(stateDir)
	for _, workspace := range []domain.Workspace{
		{Version: domain.WorkspaceVersion, ID: "one-id", Name: "one", Status: domain.WorkspaceActive},
		{Version: domain.WorkspaceVersion, ID: "two-id", Name: "two", Status: domain.WorkspaceActive},
	} {
		if err := workspaceStore.Save(workspace); err != nil {
			t.Fatal(err)
		}
	}
	err := NewService(Options{StateDir: stateDir}).ValidateRename("one", "two")
	if clierr.CodeOf(err) != clierr.AlreadyExists {
		t.Fatalf("ValidateRename() = %v", err)
	}
}
