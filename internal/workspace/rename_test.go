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

func TestRenameUpdatesThePreparedEnvironmentAssignmentName(t *testing.T) {
	stateDir := t.TempDir()
	saveClaimedWorkspace(t, stateDir, "learn")
	service := NewService(Options{StateDir: stateDir, TmuxSocket: "twt-rename-unit"})

	got, err := service.Rename("learn", "agent-sdk-delete")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "agent-sdk-delete" {
		t.Fatalf("Rename() name = %q", got.Name)
	}
	environment, err := store.NewEnvironmentStore(stateDir).Find("environment-id")
	if err != nil {
		t.Fatal(err)
	}
	if environment.Assignment == nil || environment.Assignment.Workspace.Name != "agent-sdk-delete" {
		t.Fatalf("assignment Workspace = %+v", environment.Assignment)
	}
	if environment.Assignment.Workspace.TmuxSession != "example-agent-sdk-delete" {
		t.Fatalf("assignment session = %q", environment.Assignment.Workspace.TmuxSession)
	}
	if err := service.requireWorkspaceNameAvailable("learn"); err != nil {
		t.Fatalf("old name still reserved: %v", err)
	}
}

func TestRenameHealsAStalePreparedEnvironmentAssignmentName(t *testing.T) {
	stateDir := t.TempDir()
	workspace, _ := saveClaimedWorkspace(t, stateDir, "learn")
	workspace.Name = "agent-sdk-delete"
	workspace.TmuxSession = "example-agent-sdk-delete"
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	service := NewService(Options{StateDir: stateDir, TmuxSocket: "twt-rename-unit"})
	if _, err := service.Rename("agent-sdk-delete", "agent-sdk-delete"); err != nil {
		t.Fatal(err)
	}
	environment, err := store.NewEnvironmentStore(stateDir).Find("environment-id")
	if err != nil {
		t.Fatal(err)
	}
	if environment.Assignment == nil || environment.Assignment.Workspace.Name != "agent-sdk-delete" {
		t.Fatalf("assignment Workspace = %+v", environment.Assignment)
	}
}

func TestRequireWorkspaceNameAvailableUsesTheLiveWorkspaceName(t *testing.T) {
	stateDir := t.TempDir()
	workspace, _ := saveClaimedWorkspace(t, stateDir, "learn")
	workspace.Name = "agent-sdk-delete"
	workspace.TmuxSession = "example-agent-sdk-delete"
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	service := NewService(Options{StateDir: stateDir, TmuxSocket: "twt-rename-unit"})
	if err := service.requireWorkspaceNameAvailable("learn"); err != nil {
		t.Fatalf("stale assignment still reserves learn: %v", err)
	}
	if err := service.requireWorkspaceNameAvailable("agent-sdk-delete"); err == nil {
		t.Fatal("live Workspace name is not reserved")
	}
}

func saveClaimedWorkspace(t *testing.T, stateDir, name string) (domain.Workspace, domain.PreparedEnvironment) {
	t.Helper()
	now := time.Now().UTC()
	template := domain.Template{
		Version: domain.TemplateVersion,
		Name:    "example",
		Repositories: []domain.RepositorySpec{{
			Name:  "app",
			Clone: domain.CloneSpec{URL: "https://example.com/app.git"},
		}},
	}
	digest, err := store.EnvironmentDigest(template)
	if err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-id", Name: name,
		TemplateName: template.Name, TemplateSnapshot: template,
		EnvironmentID: "environment-id", Status: domain.WorkspaceActive,
		Root: "/tmp/" + name, TmuxSession: "example-" + name,
		CreatedAt: now, UpdatedAt: now,
	}
	environment := domain.PreparedEnvironment{
		Version: domain.PreparedEnvironmentVersion, FormatVersion: domain.PreparationFormatVersion,
		ID: "environment-id", TemplateName: template.Name, TemplateDigest: digest,
		TemplateSnapshot: template, Status: domain.EnvironmentClaimed,
		Root: workspace.Root, QueueToken: "queue-token", QueuedAt: now, Generation: 1,
		Repositories: []domain.PreparedRepository{{
			Name: "app", CachePath: "/tmp/cache.git",
			Path: workspace.Root + "/app", BaseCommit: "base-commit",
		}},
		Steps: []domain.SetupStep{{
			ID: "environment_root", Kind: domain.StepWorkspaceRoot, Status: domain.StepSucceeded,
		}},
		Assignment: &domain.EnvironmentAssignment{
			Generation: 1, Kind: domain.EnvironmentAssignmentClaim, Phase: domain.EnvironmentAssignmentActive,
			Workspace: workspace, ReservedAt: now,
		},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	if err := store.NewEnvironmentStore(stateDir).Save(environment); err != nil {
		t.Fatal(err)
	}
	return workspace, environment
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
