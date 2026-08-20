package project

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestRetryRestoresProjectFromDurableEnvironmentClaim(t *testing.T) {
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
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "project-id", Name: "fix-auth",
		TemplateName: template.Name, TemplateSnapshot: template,
		EnvironmentID: "environment-id", Status: domain.ProjectInitializing,
		Root: filepath.Join(dataDir, "projects", "environment-id"), TmuxSession: "fix-auth",
		CreatedAt: now, UpdatedAt: now,
	}
	environment := domain.PreparedEnvironment{
		Version: domain.PreparedEnvironmentVersion, FormatVersion: domain.PreparationFormatVersion,
		ID: "environment-id", TemplateName: template.Name, TemplateDigest: digest,
		TemplateSnapshot: template, Status: domain.EnvironmentClaiming,
		Root: project.Root, QueueToken: "queue-token", QueuedAt: now,
		Repositories: []domain.PreparedRepository{{
			Name: "app", CachePath: filepath.Join(dataDir, "cache.git"),
			Path: filepath.Join(project.Root, "app"), BaseCommit: "base-commit",
		}},
		Steps: []domain.SetupStep{{
			ID: "environment_root", Kind: domain.StepProjectRoot, Status: domain.StepSucceeded,
		}},
		ClaimReservation: &domain.EnvironmentClaim{Project: project, ReservedAt: now},
		CreatedAt:        now, UpdatedAt: now,
	}
	if err := store.NewEnvironmentStore(stateDir).Save(environment); err != nil {
		t.Fatal(err)
	}

	service := NewService(Options{StateDir: stateDir, DataDir: dataDir})
	if _, err := service.Retry(project.Name); err == nil || !strings.Contains(err.Error(), "ownership marker") {
		t.Fatalf("Retry() error = %v, want claim continuation after Project recovery", err)
	}
	restored, err := store.NewProjectStore(stateDir).Find(project.ID)
	if err != nil {
		t.Fatalf("Project was not restored from the claim journal: %v", err)
	}
	if restored.ID != project.ID || restored.Name != project.Name || restored.EnvironmentID != project.EnvironmentID || restored.Root != project.Root {
		t.Fatalf("restored Project = %+v, want claim Project %+v", restored, project)
	}
}
