package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestProjectsRenameMovesTicketsAndRetargetsWorkspaces(t *testing.T) {
	options, home := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "old-name")
	executeWithOptions(t, options, nil, "tickets", "create", "Open work", "--project", "old-name")
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-old", Name: "old-work",
		TemplateName: "example", Status: domain.WorkspaceActive, Project: "old-name",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(options.StateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}

	dry, _, err := executeCollectingInput(t, options, nil,
		"projects", "rename", "old-name", "new-name", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	result := decodeTicketMutation(t, dry)
	if result.Operation != "projects.rename" || result.Status != "valid" {
		t.Fatalf("dry-run = %#v", result)
	}
	if _, statErr := os.Stat(filepath.Join(home, "old-name", "open-work.md")); statErr != nil {
		t.Fatal("dry-run moved a Ticket")
	}

	applied, _, err := executeCollectingInput(t, options, nil,
		"projects", "rename", "old-name", "new-name")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(applied, `Renamed Project "old-name" to "new-name"`) {
		t.Fatalf("apply text = %q", applied)
	}
	if _, statErr := os.Stat(filepath.Join(home, "old-name")); !os.IsNotExist(statErr) {
		t.Fatalf("old Project after rename = %v", statErr)
	}
	got, _, err := executeCollectingInput(t, options, nil,
		"tickets", "get", "open-work", "--output", "json")
	if err != nil || !strings.Contains(got, `"project":"new-name"`) {
		t.Fatalf("ticket after rename = %q, %v", got, err)
	}
	workspaceJSON, _, err := executeCollectingInput(t, options, nil,
		"workspaces", "get", "old-work", "--output", "json")
	if err != nil || !strings.Contains(workspaceJSON, `"project":"new-name"`) {
		t.Fatalf("workspace after rename = %q, %v", workspaceJSON, err)
	}
}

func TestProjectsRenameApplyJSON(t *testing.T) {
	options, home := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "old-name")

	applied, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"projects.rename","project":{"name":"old-name","newName":"new-name"}}`),
		"apply", "-", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	result := decodeTicketMutation(t, applied)
	if result.Operation != "projects.rename" || result.Status != "applied" || result.Name != "new-name" {
		t.Fatalf("apply projects.rename = %#v\n%s", result, applied)
	}
	if _, statErr := os.Stat(filepath.Join(home, "new-name", "index.md")); statErr != nil {
		t.Fatal(statErr)
	}
}

func TestProjectsRenameRejectsAnExistingName(t *testing.T) {
	options, _ := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "old-name")
	executeWithOptions(t, options, nil, "projects", "create", "taken")

	_, _, err := executeCollectingInput(t, options, nil, "projects", "rename", "old-name", "taken")
	if clierr.CodeOf(err) != clierr.AlreadyExists {
		t.Fatalf("rename onto existing = %v, want already_exists", err)
	}
}
