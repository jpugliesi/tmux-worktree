package cli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestWorkspacesSetProjectOnAnEmptyWorkspace(t *testing.T) {
	options, _ := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "change-monitor")
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-empty", Name: "empty-work",
		TemplateName: "example", Status: domain.WorkspaceActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(options.StateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}

	dry, _, err := executeCollectingInput(t, options, nil,
		"workspaces", "set", "empty-work", "--project", "change-monitor", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	result := decodeTicketMutation(t, dry)
	if result.Operation != "workspaces.set" || result.Status != "valid" {
		t.Fatalf("dry-run = %#v", result)
	}

	applied, _, err := executeCollectingInput(t, options, nil,
		"workspaces", "set", "empty-work", "--project", "change-monitor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(applied, `Set Project "change-monitor" on Workspace "empty-work"`) {
		t.Fatalf("apply text = %q", applied)
	}
	got, _, err := executeCollectingInput(t, options, nil,
		"workspaces", "get", "empty-work", "--output", "json")
	if err != nil || !strings.Contains(got, `"project":"change-monitor"`) {
		t.Fatalf("workspace after set = %q, %v", got, err)
	}
}

func TestWorkspacesSetProjectRequiresMatchingTickets(t *testing.T) {
	options, _ := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "alpha")
	executeWithOptions(t, options, nil, "projects", "create", "beta")
	executeWithOptions(t, options, nil, "tickets", "create", "Alpha work", "--project", "alpha")
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-alpha", Name: "alpha-work",
		TemplateName: "example", Status: domain.WorkspaceActive, Project: "alpha",
		Tickets: []string{"alpha-work"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(options.StateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCollectingInput(t, options, nil,
		"workspaces", "set", "alpha-work", "--project", "beta")
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("mismatched Tickets = %v, want precondition_failed", err)
	}
	same, _, err := executeCollectingInput(t, options, nil,
		"workspaces", "set", "alpha-work", "--project", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(same, `Set Project "alpha" on Workspace "alpha-work"`) {
		t.Fatalf("matching set = %q", same)
	}
}

func TestWorkspacesSetProjectRejectsAClosedProject(t *testing.T) {
	options, _ := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "closed-proj")
	executeWithOptions(t, options, nil, "projects", "close", "closed-proj")
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-empty", Name: "empty-work",
		TemplateName: "example", Status: domain.WorkspaceActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(options.StateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCollectingInput(t, options, nil,
		"workspaces", "set", "empty-work", "--project", "closed-proj")
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("closed Project = %v, want precondition_failed", err)
	}
}

func TestWorkspacesSetApplyJSON(t *testing.T) {
	options, _ := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "change-monitor")
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-empty", Name: "empty-work",
		TemplateName: "example", Status: domain.WorkspaceActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(options.StateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}

	applied, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"workspaces.set","workspace":{"reference":"empty-work","project":"change-monitor"}}`),
		"apply", "-", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	result := decodeTicketMutation(t, applied)
	if result.Operation != "workspaces.set" || result.Status != "applied" || result.Name != "empty-work" {
		t.Fatalf("apply workspaces.set = %#v\n%s", result, applied)
	}
}
