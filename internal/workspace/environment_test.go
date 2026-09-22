package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestValidateEnvironmentClaimMarkerAcceptsLegacyProjectID(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".twt-owned.json"), []byte(`{"owner":"twt","projectId":"workspace-one","environmentId":"environment-one"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := domain.PreparedEnvironment{ID: "environment-one"}
	workspace := domain.Workspace{ID: "workspace-one", Root: root}
	if err := validateEnvironmentClaimMarker(environment, workspace); err != nil {
		t.Fatalf("legacy claim marker: %v", err)
	}
}

// A claim whose Prepared Environment has no ownership marker cannot use that
// environment. Retry restores the Workspace record from the durable claim,
// gives the environment up as failed, and creates the Workspace again. The
// fixture origin does not exist, so the replacement stops at the clone.
func TestRetryAbandonsADurableClaimWithoutAnOwnershipMarker(t *testing.T) {
	stateDir := t.TempDir()
	dataDir := t.TempDir()
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
		Version: domain.WorkspaceVersion, ID: "workspace-id", Name: "fix-auth",
		TemplateName: template.Name, TemplateSnapshot: template,
		EnvironmentID: "environment-id", Status: domain.WorkspaceInitializing,
		Root: filepath.Join(dataDir, "projects", "environment-id"), TmuxSession: "fix-auth",
		CreatedAt: now, UpdatedAt: now,
	}
	environment := domain.PreparedEnvironment{
		Version: domain.PreparedEnvironmentVersion, FormatVersion: domain.PreparationFormatVersion,
		ID: "environment-id", TemplateName: template.Name, TemplateDigest: digest,
		TemplateSnapshot: template, Status: domain.EnvironmentClaiming,
		Root: workspace.Root, QueueToken: "queue-token", QueuedAt: now,
		Repositories: []domain.PreparedRepository{{
			Name: "app", CachePath: filepath.Join(dataDir, "cache.git"),
			Path: filepath.Join(workspace.Root, "app"), BaseCommit: "base-commit",
		}},
		Steps: []domain.SetupStep{{
			ID: "environment_root", Kind: domain.StepWorkspaceRoot, Status: domain.StepSucceeded,
		}},
		Assignment: &domain.EnvironmentAssignment{Workspace: workspace, ReservedAt: now},
		CreatedAt:  now, UpdatedAt: now,
	}
	if err := store.NewEnvironmentStore(stateDir).Save(environment); err != nil {
		t.Fatal(err)
	}

	service := NewService(Options{StateDir: stateDir, DataDir: dataDir})
	_, err = service.Retry(workspace.Name)
	if err == nil || strings.Contains(err.Error(), "ownership marker") || !strings.Contains(err.Error(), "clone repository") {
		t.Fatalf("Retry() error = %v, want a replacement create that stops at the clone", err)
	}
	// The replacement create removes the failed environment. Its root does
	// not exist, so only the record goes away.
	if abandoned, err := store.NewEnvironmentStore(stateDir).Find(environment.ID); err == nil {
		t.Fatalf("abandoned Prepared Environment remains: %+v", abandoned)
	}
	if _, err := store.NewWorkspaceStore(stateDir).Find(workspace.ID); err == nil {
		t.Fatalf("the reserved Workspace record %s remains after the abandoned claim", workspace.ID)
	}
}
