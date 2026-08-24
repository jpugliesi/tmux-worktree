package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAgentStoreLoadsTheLegacyProjectID(t *testing.T) {
	stateDir := t.TempDir()
	directory := filepath.Join(stateDir, "agents")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "version": 1,
  "id": "agent-one",
  "projectId": "workspace-one",
  "provider": "codex",
  "label": "review",
  "createdAt": "2026-08-20T12:00:00Z",
  "updatedAt": "2026-08-20T12:00:00Z"
}
`
	if err := os.WriteFile(filepath.Join(directory, "agent-one.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	agent, err := NewAgentStore(stateDir).Find("agent-one")
	if err != nil {
		t.Fatal(err)
	}
	if agent.WorkspaceID != "workspace-one" {
		t.Fatalf("legacy Agent Session WorkspaceID = %q", agent.WorkspaceID)
	}
}
