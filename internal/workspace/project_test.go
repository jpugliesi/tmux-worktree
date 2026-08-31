package workspace

import (
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestSetProjectUpdatesOneWorkspace(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Now().UTC().Add(-time.Hour)
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-id", Name: "fix-auth",
		TemplateName: "template", Status: domain.WorkspaceActive, Project: "old",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	service := NewService(Options{StateDir: stateDir})

	if err := service.ValidateSetProject(workspace.ID, "new"); err != nil {
		t.Fatal(err)
	}
	got, err := service.SetProject(workspace.ID, "new")
	if err != nil {
		t.Fatal(err)
	}
	if got.Project != "new" || got.ID != workspace.ID || got.Name != workspace.Name {
		t.Fatalf("SetProject() = %+v", got)
	}
	same, err := service.SetProject(workspace.ID, "new")
	if err != nil || same.Project != "new" {
		t.Fatalf("idempotent SetProject() = %+v, %v", same, err)
	}
}

func TestSetProjectRejectsAnEmptyName(t *testing.T) {
	stateDir := t.TempDir()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-id", Name: "fix-auth",
		Status: domain.WorkspaceActive,
	}
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(Options{StateDir: stateDir}).SetProject(workspace.ID, "  "); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("empty Project = %v, want invalid_usage", err)
	}
}

func TestRetargetProjectRewritesMatchingWorkspaces(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Now().UTC()
	for _, workspace := range []domain.Workspace{
		{Version: domain.WorkspaceVersion, ID: "one", Name: "one", Status: domain.WorkspaceActive, Project: "old", CreatedAt: now, UpdatedAt: now},
		{Version: domain.WorkspaceVersion, ID: "two", Name: "two", Status: domain.WorkspaceArchived, Project: "old", CreatedAt: now, UpdatedAt: now},
		{Version: domain.WorkspaceVersion, ID: "keep", Name: "keep", Status: domain.WorkspaceActive, Project: "other", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(Options{StateDir: stateDir})
	if err := service.RetargetProject("old", "new"); err != nil {
		t.Fatal(err)
	}
	one, err := service.Find("one")
	if err != nil || one.Project != "new" {
		t.Fatalf("one = %+v, %v", one, err)
	}
	two, err := service.Find("two")
	if err != nil || two.Project != "new" {
		t.Fatalf("two = %+v, %v", two, err)
	}
	keep, err := service.Find("keep")
	if err != nil || keep.Project != "other" {
		t.Fatalf("keep = %+v, %v", keep, err)
	}
}
