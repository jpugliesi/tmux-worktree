package workspace

import (
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestRenameUpdatesTheWorkspaceAndStoredSessionName(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Now().UTC().Add(-time.Hour)
	want := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-id", Name: "old-name",
		TemplateName: "template", Status: domain.WorkspaceActive, Root: "/tmp/old-name",
		TmuxSession: "template-old-name", CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(stateDir).Save(want); err != nil {
		t.Fatal(err)
	}
	service := NewService(Options{StateDir: stateDir, TmuxSocket: "twt-rename-unit"})

	got, err := service.Rename(want.ID, "new-name")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "new-name" || got.ID != want.ID || got.Root != want.Root || got.TmuxSession != "template-new-name" {
		t.Fatalf("Rename() = %+v", got)
	}
	if _, err := service.Find("old-name"); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("Find(old-name) = %v", err)
	}
}

func TestRenameLeavesAnEmptyTmuxSessionEmpty(t *testing.T) {
	stateDir := t.TempDir()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "archived-id", Name: "old-name",
		TemplateName: "template", Status: domain.WorkspaceArchived, TmuxSession: "",
	}
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	got, err := NewService(Options{StateDir: stateDir, TmuxSocket: "twt-rename-unit"}).Rename(workspace.ID, "new-name")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "new-name" || got.TmuxSession != "" {
		t.Fatalf("Rename() = %+v", got)
	}
}

func TestRenameAlignsStoredSessionWhenDisplayNameIsUnchanged(t *testing.T) {
	stateDir := t.TempDir()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "stale-session-id", Name: "new-name",
		TemplateName: "template", Status: domain.WorkspaceActive, TmuxSession: "template-old-name",
	}
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	got, err := NewService(Options{StateDir: stateDir, TmuxSocket: "twt-rename-unit"}).Rename(workspace.ID, "new-name")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "new-name" || got.TmuxSession != "template-new-name" {
		t.Fatalf("Rename() = %+v", got)
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
