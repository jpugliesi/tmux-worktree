package store

import (
	"bytes"
	"encoding/json"
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

func TestEnvironmentStoreChangesAnAssignmentForANewGeneration(t *testing.T) {
	environments := NewEnvironmentStore(t.TempDir())
	environment := testEnvironment("claim-once", time.Now().UTC())
	environment.Status = domain.EnvironmentClaiming
	environment.Assignment = &domain.EnvironmentAssignment{
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
	environment.Assignment.Generation = 2
	environment.Assignment.Workspace.ID = "different-workspace"
	environment.Assignment.Workspace.EnvironmentID = environment.ID
	if err := environments.Save(environment); err != nil {
		t.Fatalf("Save() changed assignment: %v", err)
	}
	got, err := environments.Find(environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Assignment.Workspace.ID != "different-workspace" || got.Assignment.Generation != 2 {
		t.Fatalf("saved Workspace ID = %q", got.Assignment.Workspace.ID)
	}
}

func TestEnvironmentStorePersistsACompleteClaimReservation(t *testing.T) {
	environments := NewEnvironmentStore(t.TempDir())
	environment := testEnvironment("claimed", time.Now().UTC())
	environment.Status = domain.EnvironmentClaiming
	environment.Assignment = &domain.EnvironmentAssignment{
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
	if got.Assignment == nil || got.Assignment.Workspace.TmuxSession != "fix-auth-workspace-id" || len(got.Assignment.Workspace.Repositories) != 1 {
		t.Fatalf("claim reservation = %+v", got.Assignment)
	}
}

func TestEnvironmentStoreLoadsALegacyWorkspaceClaim(t *testing.T) {
	stateDir := t.TempDir()
	environments := NewEnvironmentStore(stateDir)
	environment := testEnvironment("legacy-claim", time.Now().UTC())
	environment.Status = domain.EnvironmentClaiming
	environment.Assignment = &domain.EnvironmentAssignment{
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
	if got.Assignment == nil || got.Assignment.Workspace.ID != "legacy-workspace" {
		t.Fatalf("legacy claim = %+v", got.Assignment)
	}
}

func TestEnvironmentStoreNormalizesAllVersionOneSetupStepsOnRead(t *testing.T) {
	stateDir := t.TempDir()
	environments := NewEnvironmentStore(stateDir)
	environment := testEnvironment("legacy-steps", time.Now().UTC())
	environment.Steps = []domain.SetupStep{{
		ID: "environment_root", Kind: domain.StepKind("project_root"),
		Status: domain.StepSucceeded, Attempts: 1,
	}}
	environment.Status = domain.EnvironmentClaiming
	environment.Assignment = &domain.EnvironmentAssignment{
		Workspace: domain.Workspace{
			Version: domain.WorkspaceVersion, ID: "legacy-workspace",
			EnvironmentID: environment.ID, Name: "auth-fix",
			TemplateName: environment.TemplateName, Status: domain.WorkspaceSetupFailed,
			Steps: []domain.SetupStep{
				{ID: "project_root", Kind: domain.StepKind("project_root"), Status: domain.StepSucceeded, Attempts: 1},
				{ID: "project_init", Kind: domain.StepKind("project_init"), Status: domain.StepFailed, Attempts: 2, Error: "stopped"},
			},
		},
		ReservedAt: time.Now().UTC(),
	}
	raw, err := json.MarshalIndent(environment, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	legacyKey := []byte(`"workspace":`)
	if bytes.Count(raw, legacyKey) != 1 {
		t.Fatalf("current fixture has %d Workspace claim keys", bytes.Count(raw, legacyKey))
	}
	raw = bytes.Replace(raw, legacyKey, []byte(`"project":`), 1)
	raw = bytes.Replace(raw, []byte(`"version": 2`), []byte(`"version": 1`), 1)
	raw = bytes.Replace(raw, []byte(`"assignment":`), []byte(`"claimReservation":`), 1)
	raw = append(raw, '\n')
	directory := filepath.Join(stateDir, "environments")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, environment.ID+".json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := environments.Find(environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Steps[0].ID != "environment_root" || got.Steps[0].Kind != domain.StepWorkspaceRoot {
		t.Fatalf("legacy Environment root step = %+v", got.Steps[0])
	}
	claimSteps := got.Assignment.Workspace.Steps
	if claimSteps[0].ID != "workspace_root" || claimSteps[0].Kind != domain.StepWorkspaceRoot {
		t.Fatalf("legacy claim root step = %+v", claimSteps[0])
	}
	if claimSteps[1].ID != "workspace_init" || claimSteps[1].Kind != domain.StepWorkspaceInit ||
		claimSteps[1].Status != domain.StepFailed || claimSteps[1].Attempts != 2 || claimSteps[1].Error != "stopped" {
		t.Fatalf("legacy claim initialization step = %+v", claimSteps[1])
	}
	unchanged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != string(raw) {
		t.Fatal("loading the legacy Prepared Environment changed its state file")
	}

	if err := environments.Save(got); err != nil {
		t.Fatal(err)
	}
	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rewritten), `"project_root"`) || strings.Contains(string(rewritten), `"project_init"`) {
		t.Fatalf("saved Prepared Environment keeps legacy setup-step names:\n%s", rewritten)
	}
	if strings.Contains(string(rewritten), `"project":`) || !strings.Contains(string(rewritten), `"workspace":`) {
		t.Fatalf("saved Prepared Environment keeps the legacy claim key:\n%s", rewritten)
	}
	if !strings.Contains(string(rewritten), `"version": 3`) || !strings.Contains(string(rewritten), `"assignment":`) || strings.Contains(string(rewritten), `"claimReservation":`) {
		t.Fatalf("saved Prepared Environment has no current assignment:\n%s", rewritten)
	}
}

func TestEnvironmentStoreLoadsVersionTwoState(t *testing.T) {
	stateDir := t.TempDir()
	environments := NewEnvironmentStore(stateDir)
	environment := testEnvironment("version-two", time.Now().UTC())
	if err := environments.Save(environment); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "environments", environment.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := bytes.Replace(data, []byte(`"version": 3`), []byte(`"version": 2`), 1)
	if bytes.Equal(legacy, data) {
		t.Fatal("current Prepared Environment state does not use version 3")
	}
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := environments.Find(environment.ID)
	if err != nil {
		t.Fatalf("Find() version-two state: %v", err)
	}
	if got.Version != domain.PreparedEnvironmentVersion {
		t.Fatalf("loaded version = %d, want %d", got.Version, domain.PreparedEnvironmentVersion)
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
