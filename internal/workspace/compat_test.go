package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestRetryRunsALegacyProjectInitializationStep(t *testing.T) {
	stateDir := t.TempDir()
	root := t.TempDir()
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "legacy-workspace", Name: "auth-fix",
		Status: domain.WorkspaceSetupFailed, Root: root,
		TemplateSnapshot: domain.Template{
			Version: domain.TemplateVersion, Name: "example",
			Initialize: &domain.InitializeSpec{
				Command: []string{"/bin/sh", "-c", "touch initialized"},
			},
		},
		Steps: []domain.SetupStep{{
			ID: "project_init", Kind: domain.StepKind("project_init"),
			Status: domain.StepFailed, Attempts: 1, Error: "stopped",
		}},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}

	result, err := NewService(Options{StateDir: stateDir, DataDir: t.TempDir()}).Retry(workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.WorkspaceActive {
		t.Fatalf("Retry() status = %q", result.Status)
	}
	if len(result.Steps) != 1 || result.Steps[0].ID != "workspace_init" ||
		result.Steps[0].Kind != domain.StepWorkspaceInit || result.Steps[0].Status != domain.StepSucceeded {
		t.Fatalf("Retry() step = %+v", result.Steps)
	}
	if _, err := os.Stat(filepath.Join(root, "initialized")); err != nil {
		t.Fatalf("legacy initialization did not run: %v", err)
	}
}
