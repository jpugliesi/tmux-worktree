package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceStoreLoadsTheVersionOneTicketField(t *testing.T) {
	stateDir := t.TempDir()
	directory := filepath.Join(stateDir, "projects")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "version": 1,
  "id": "legacy-workspace",
  "name": "auth-fix",
  "templateName": "example",
  "status": "archived",
  "ticket": "fix-auth",
  "root": "/tmp/auth-fix",
  "tmuxSession": "auth-fix",
  "repositories": [],
  "steps": [],
  "createdAt": "2026-08-20T12:00:00Z",
  "updatedAt": "2026-08-21T12:00:00Z"
}
`
	if err := os.WriteFile(filepath.Join(directory, "legacy-workspace.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	workspace, err := NewWorkspaceStore(stateDir).Find("legacy-workspace")
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Tickets) != 1 || workspace.Tickets[0] != "fix-auth" {
		t.Fatalf("legacy Workspace Tickets = %v", workspace.Tickets)
	}
}

func TestWorkspaceStoreNormalizesVersionOneSetupStepsOnRead(t *testing.T) {
	stateDir := t.TempDir()
	directory := filepath.Join(stateDir, "projects")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "version": 1,
  "id": "legacy-workspace",
  "name": "auth-fix",
  "templateName": "example",
  "status": "setup_failed",
  "root": "/tmp/auth-fix",
  "tmuxSession": "auth-fix",
  "repositories": [],
  "steps": [
    {
      "id": "project_root",
      "kind": "project_root",
      "status": "succeeded",
      "attempts": 1,
      "startedAt": "2026-08-20T12:01:00Z",
      "finishedAt": "2026-08-20T12:02:00Z"
    },
    {
      "id": "project_init",
      "kind": "project_init",
      "status": "failed",
      "attempts": 2,
      "error": "initialization stopped",
      "startedAt": "2026-08-20T12:03:00Z",
      "finishedAt": "2026-08-20T12:04:00Z"
    },
    {
      "id": "repository_init:app",
      "kind": "repository_init",
      "repository": "app",
      "status": "succeeded",
      "attempts": 1
    }
  ],
  "createdAt": "2026-08-20T12:00:00Z",
  "updatedAt": "2026-08-21T12:00:00Z"
}
`
	path := filepath.Join(directory, "legacy-workspace.json")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	workspace, err := NewWorkspaceStore(stateDir).Find("legacy-workspace")
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Steps[0].ID != "workspace_root" || workspace.Steps[0].Kind != "workspace_root" {
		t.Fatalf("legacy root step = %+v", workspace.Steps[0])
	}
	if workspace.Steps[1].ID != "workspace_init" || workspace.Steps[1].Kind != "workspace_init" {
		t.Fatalf("legacy initialization step = %+v", workspace.Steps[1])
	}
	if workspace.Steps[1].Status != "failed" || workspace.Steps[1].Attempts != 2 || workspace.Steps[1].Error != "initialization stopped" {
		t.Fatalf("legacy initialization metadata = %+v", workspace.Steps[1])
	}
	wantStarted := time.Date(2026, time.August, 20, 12, 3, 0, 0, time.UTC)
	wantFinished := time.Date(2026, time.August, 20, 12, 4, 0, 0, time.UTC)
	if workspace.Steps[1].StartedAt == nil || !workspace.Steps[1].StartedAt.Equal(wantStarted) ||
		workspace.Steps[1].FinishedAt == nil || !workspace.Steps[1].FinishedAt.Equal(wantFinished) {
		t.Fatalf("legacy initialization timestamps = %+v", workspace.Steps[1])
	}
	if workspace.Steps[2].ID != "repository_init:app" || workspace.Steps[2].Kind != "repository_init" {
		t.Fatalf("current repository step changed = %+v", workspace.Steps[2])
	}
	unchanged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != legacy {
		t.Fatal("loading the legacy Workspace changed its state file")
	}

	if err := NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	rewritten, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rewritten), `"project_root"`) || strings.Contains(string(rewritten), `"project_init"`) {
		t.Fatalf("saved Workspace keeps legacy setup-step names:\n%s", rewritten)
	}
	if !strings.Contains(string(rewritten), `"workspace_root"`) || !strings.Contains(string(rewritten), `"workspace_init"`) {
		t.Fatalf("saved Workspace has no current setup-step names:\n%s", rewritten)
	}
}

func TestWorkspaceStoreRejectsConflictingVersionOneAndCurrentTicketFields(t *testing.T) {
	stateDir := t.TempDir()
	directory := filepath.Join(stateDir, "projects")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "version": 1,
  "id": "conflicting-workspace",
  "name": "auth-fix",
  "status": "archived",
  "ticket": "fix-auth",
  "tickets": ["fix-billing"],
  "repositories": [],
  "steps": [],
  "createdAt": "2026-08-20T12:00:00Z",
  "updatedAt": "2026-08-21T12:00:00Z"
}
`
	if err := os.WriteFile(filepath.Join(directory, "conflicting-workspace.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := NewWorkspaceStore(stateDir).Find("conflicting-workspace")
	if err == nil || !strings.Contains(err.Error(), "conflicting ticket and tickets values") {
		t.Fatalf("Find() conflict error = %v", err)
	}
}
