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

func TestProjectsRemovePlansThenApplies(t *testing.T) {
	options, home := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "change-monitor")
	executeWithOptions(t, options, nil, "tickets", "create", "Open work", "--project", "change-monitor")

	plan, _, err := executeCollectingInput(t, options, nil, "projects", "remove", "change-monitor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, `Removal plan for Project "change-monitor"`) {
		t.Fatalf("plan text = %q", plan)
	}
	if !strings.Contains(plan, "Tickets: open-work") {
		t.Fatalf("plan omitted tickets: %q", plan)
	}
	if !strings.Contains(plan, "Run again with --apply") {
		t.Fatalf("plan omitted apply hint: %q", plan)
	}
	if _, statErr := os.Stat(filepath.Join(home, "change-monitor", "open-work.md")); statErr != nil {
		t.Fatal("plan removed a Ticket")
	}

	_, _, err = executeCollectingInput(t, options, nil,
		"projects", "remove", "change-monitor", "--apply", "--dry-run")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("apply with dry-run = %v", err)
	}

	applied, _, err := executeCollectingInput(t, options, nil,
		"projects", "remove", "change-monitor", "--apply")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(applied, `Removed Project "change-monitor"`) {
		t.Fatalf("apply text = %q", applied)
	}
	if _, statErr := os.Stat(filepath.Join(home, "change-monitor")); !os.IsNotExist(statErr) {
		t.Fatalf("apply left the Project: %v", statErr)
	}
	stdout, _, err := executeCollectingInput(t, options, nil, "projects", "create", "change-monitor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `Created Project "change-monitor"`) {
		t.Fatalf("recreate = %q", stdout)
	}
}

func TestProjectsRemoveApplyJSONAndClosedProject(t *testing.T) {
	options, home := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "force-close")
	executeWithOptions(t, options, nil, "projects", "close", "force-close")

	planJSON, _, err := executeCollectingInput(t, options, nil,
		"projects", "remove", "force-close", "--output", "json")
	if err != nil || !strings.Contains(planJSON, `"applied":false`) {
		t.Fatalf("plan json = %q, %v", planJSON, err)
	}
	applied, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"projects.remove","project":{"name":"force-close","apply":true}}`),
		"apply", "-", "--output", "json")
	if err != nil || !strings.Contains(applied, `"applied":true`) {
		t.Fatalf("apply projects.remove = %q, %v", applied, err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "force-close")); !os.IsNotExist(statErr) {
		t.Fatalf("closed Project after remove = %v", statErr)
	}
}

func TestProjectsRemoveBlocksALinkedWorkspace(t *testing.T) {
	options, home := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-core", Name: "core-work",
		TemplateName: "example", Status: domain.WorkspaceArchived, Project: "core",
		CreatedAt: now, UpdatedAt: now, ArchivedAt: &now,
	}
	if err := store.NewWorkspaceStore(options.StateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}

	plan, _, err := executeCollectingInput(t, options, nil, "projects", "remove", "core")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "Blocked:") || !strings.Contains(plan, "core-work") {
		t.Fatalf("blocked plan = %q", plan)
	}

	_, _, err = executeCollectingInput(t, options, nil, "projects", "remove", "core", "--apply")
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("apply with linked Workspace = %v", err)
	}
	if !strings.Contains(clierr.HintOf(err), "workspaces remove") {
		t.Fatalf("blocker hint = %q", clierr.HintOf(err))
	}
	if _, statErr := os.Stat(filepath.Join(home, "core", "index.md")); statErr != nil {
		t.Fatal("blocked apply deleted the Project")
	}
}
