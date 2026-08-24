package store

import (
	"os"
	"path/filepath"
	"testing"
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
