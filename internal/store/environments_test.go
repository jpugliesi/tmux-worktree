package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestEnvironmentStoreSavesFindsListsAndDeletes(t *testing.T) {
	stateDir := t.TempDir()
	environments := NewEnvironmentStore(stateDir)
	first := testEnvironment("environment-b", time.Date(2026, time.August, 19, 10, 0, 0, 0, time.UTC))
	second := testEnvironment("environment-a", first.CreatedAt)

	if err := environments.Save(first); err != nil {
		t.Fatal(err)
	}
	if err := environments.Save(second); err != nil {
		t.Fatal(err)
	}
	first.Status = domain.EnvironmentReady
	first.UpdatedAt = first.UpdatedAt.Add(time.Minute)
	if err := environments.Save(first); err != nil {
		t.Fatal(err)
	}

	got, err := environments.Find(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.EnvironmentReady || got.TemplateDigest != first.TemplateDigest {
		t.Fatalf("Find() = %+v", got)
	}

	listed, err := environments.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ID != "environment-a" || listed[1].ID != "environment-b" {
		t.Fatalf("List() order = %+v", listed)
	}

	if err := environments.Delete(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := environments.Find(first.ID); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("Find() after Delete() error = %v", err)
	}
}

func TestEnvironmentStoreRejectsInvalidStateAndVersions(t *testing.T) {
	stateDir := t.TempDir()
	environments := NewEnvironmentStore(stateDir)
	invalid := testEnvironment("../outside", time.Now().UTC())
	if err := environments.Save(invalid); err == nil || !strings.Contains(err.Error(), "invalid Prepared Environment ID") {
		t.Fatalf("Save() invalid ID error = %v", err)
	}

	invalid = testEnvironment("bad-version", time.Now().UTC())
	invalid.Version++
	if err := environments.Save(invalid); err == nil || !strings.Contains(err.Error(), "unsupported Prepared Environment version") {
		t.Fatalf("Save() invalid version error = %v", err)
	}

	dir := filepath.Join(stateDir, "environments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "future.json"), []byte(`{"version":99,"id":"future"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := environments.List(); err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("List() future version error = %v", err)
	}
}

func TestEnvironmentStoreDoesNotChangeAClaimReservation(t *testing.T) {
	environments := NewEnvironmentStore(t.TempDir())
	environment := testEnvironment("claim-once", time.Now().UTC())
	environment.Status = domain.EnvironmentClaiming
	environment.ClaimReservation = &domain.EnvironmentClaim{
		Workspace: domain.Workspace{
			Version:       domain.WorkspaceVersion,
			ID:            "first-workspace",
			EnvironmentID: environment.ID,
		},
		ReservedAt: time.Now().UTC(),
	}
	if err := environments.Save(environment); err != nil {
		t.Fatal(err)
	}
	environment.ClaimReservation.Workspace.ID = "different-workspace"
	if err := environments.Save(environment); err == nil || !strings.Contains(err.Error(), "claim reservation cannot change") {
		t.Fatalf("Save() changed reservation error = %v", err)
	}
	got, err := environments.Find(environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClaimReservation.Workspace.ID != "first-workspace" {
		t.Fatalf("saved Workspace ID = %q", got.ClaimReservation.Workspace.ID)
	}
}

func TestEnvironmentStorePersistsACompleteClaimReservation(t *testing.T) {
	environments := NewEnvironmentStore(t.TempDir())
	environment := testEnvironment("claimed", time.Now().UTC())
	environment.Status = domain.EnvironmentClaiming
	environment.ClaimReservation = &domain.EnvironmentClaim{
		Workspace: domain.Workspace{
			Version:       domain.WorkspaceVersion,
			ID:            "workspace-id",
			EnvironmentID: environment.ID,
			Name:          "fix-auth",
			TemplateName:  environment.TemplateName,
			Status:        domain.WorkspaceInitializing,
			Root:          "/workspaces/fix-auth",
			TmuxSession:   "fix-auth-workspace-id",
			Repositories: []domain.WorkspaceRepository{{
				Name: "app", CachePath: "/cache/app", Path: "/workspaces/fix-auth/app", Branch: "fix-auth", WindowName: "app",
			}},
		},
		ReservedAt: time.Now().UTC(),
	}
	if err := environments.Save(environment); err != nil {
		t.Fatal(err)
	}
	got, err := environments.Find(environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClaimReservation == nil || got.ClaimReservation.Workspace.TmuxSession != "fix-auth-workspace-id" || len(got.ClaimReservation.Workspace.Repositories) != 1 {
		t.Fatalf("claim reservation = %+v", got.ClaimReservation)
	}
}

func TestEnvironmentStoreLoadsALegacyWorkspaceClaim(t *testing.T) {
	stateDir := t.TempDir()
	environments := NewEnvironmentStore(stateDir)
	environment := testEnvironment("legacy-claim", time.Now().UTC())
	environment.Status = domain.EnvironmentClaiming
	environment.ClaimReservation = &domain.EnvironmentClaim{
		Workspace: domain.Workspace{
			Version:       domain.WorkspaceVersion,
			ID:            "legacy-workspace",
			EnvironmentID: environment.ID,
			Name:          "auth-fix",
			TemplateName:  environment.TemplateName,
			Status:        domain.WorkspaceInitializing,
		},
		ReservedAt: time.Now().UTC(),
	}
	if err := environments.Save(environment); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "environments", environment.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(string(data), `"workspace":`, `"project":`, 1)
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := environments.Find(environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClaimReservation == nil || got.ClaimReservation.Workspace.ID != "legacy-workspace" {
		t.Fatalf("legacy claim = %+v", got.ClaimReservation)
	}
}

func TestEnvironmentStoreLoadsARecordWithTheLegacyTemplateDigest(t *testing.T) {
	stateDir := t.TempDir()
	environments := NewEnvironmentStore(stateDir)
	environment := testEnvironment("legacy-digest", time.Now().UTC())
	legacy, err := LegacyTemplateDigest(environment.TemplateSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	environment.TemplateDigest = legacy
	if err := environments.Save(environment); err != nil {
		t.Fatalf("Save() legacy digest error = %v", err)
	}
	got, err := environments.Find(environment.ID)
	if err != nil {
		t.Fatalf("Find() legacy digest error = %v", err)
	}
	if got.TemplateDigest != legacy {
		t.Fatalf("loaded digest = %q, want the legacy digest %q", got.TemplateDigest, legacy)
	}
}

func TestEnvironmentStoreRejectsAnUnknownTemplateDigest(t *testing.T) {
	environments := NewEnvironmentStore(t.TempDir())
	environment := testEnvironment("unknown-digest", time.Now().UTC())
	environment.TemplateDigest = "0000000000000000000000000000000000000000000000000000000000000000"
	if err := environments.Save(environment); err == nil || !strings.Contains(err.Error(), "Workspace Template digest") {
		t.Fatalf("Save() unknown digest error = %v", err)
	}
}

func TestEnvironmentStoreKeepsTheReadyTime(t *testing.T) {
	environments := NewEnvironmentStore(t.TempDir())
	environment := testEnvironment("ready-time", time.Now().UTC())
	readyAt := environment.CreatedAt.Add(time.Minute)
	environment.Status = domain.EnvironmentReady
	environment.ReadyAt = &readyAt
	if err := environments.Save(environment); err != nil {
		t.Fatal(err)
	}
	got, err := environments.Find(environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReadyAt == nil || !got.ReadyAt.Equal(readyAt) {
		t.Fatalf("ReadyAt = %v, want %v", got.ReadyAt, readyAt)
	}
}

func testEnvironment(id string, createdAt time.Time) domain.PreparedEnvironment {
	template := domain.Template{
		Version: domain.TemplateVersion,
		Name:    "example",
		Repositories: []domain.RepositorySpec{{
			Name:  "app",
			Clone: domain.CloneSpec{URL: "https://example.com/app.git"},
		}},
	}
	environment := domain.PreparedEnvironment{
		Version:          domain.PreparedEnvironmentVersion,
		FormatVersion:    domain.PreparationFormatVersion,
		ID:               id,
		TemplateName:     template.Name,
		TemplateSnapshot: template,
		Status:           domain.EnvironmentQueued,
		Root:             filepath.Join("/tmp", id),
		Repositories: []domain.PreparedRepository{{
			Name: "app", CachePath: filepath.Join("/tmp", id, "cache.git"),
			Path: filepath.Join("/tmp", id, "app"), BaseCommit: "base-commit",
		}},
		Steps: []domain.SetupStep{{
			ID: "environment_root", Kind: domain.StepWorkspaceRoot, Status: domain.StepSucceeded,
		}},
		QueueToken: "queue-token",
		QueuedAt:   createdAt,
		CreatedAt:  createdAt,
		UpdatedAt:  createdAt,
	}
	digest, err := EnvironmentDigest(template)
	if err != nil {
		panic(err)
	}
	environment.TemplateDigest = digest
	return environment
}
