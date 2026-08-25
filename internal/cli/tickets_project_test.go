package cli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestTicketsListUsesTWTProject(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"change-monitor", "core"} {
		if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", name); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "monitor work", "--project", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "core work", "--project", "core"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TWT_PROJECT", "change-monitor")
	stdout, _, err := executeCollectingInput(t, options, nil, "tickets", "list", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"slug":"monitor-work"`) || strings.Contains(stdout, `"slug":"core-work"`) {
		t.Fatalf("TWT_PROJECT list = %s", stdout)
	}
}

func TestTicketsListProjectFlagBeatsEnv(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"change-monitor", "core"} {
		if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", name); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "monitor work", "--project", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "core work", "--project", "core"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TWT_PROJECT", "change-monitor")
	stdout, _, err := executeCollectingInput(t, options, nil,
		"tickets", "list", "--project", "core", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"slug":"core-work"`) || strings.Contains(stdout, `"slug":"monitor-work"`) {
		t.Fatalf("--project did not beat TWT_PROJECT: %s", stdout)
	}
}

func TestTicketsListAllProjectsBeatsEnv(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"change-monitor", "core"} {
		if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", name); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "monitor work", "--project", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "core work", "--project", "core"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TWT_PROJECT", "change-monitor")
	stdout, _, err := executeCollectingInput(t, options, nil,
		"tickets", "list", "--all-projects", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"slug":"monitor-work"`) || !strings.Contains(stdout, `"slug":"core-work"`) {
		t.Fatalf("--all-projects did not beat TWT_PROJECT: %s", stdout)
	}
}

func TestTicketsListRejectsProjectAndAllProjects(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, nil,
		"tickets", "list", "--project", "change-monitor", "--all-projects")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("--project with --all-projects = %v (code %q)", err, clierr.CodeOf(err))
	}
}

func TestTicketsListUsesWorkspaceProject(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"change-monitor", "core"} {
		if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", name); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "monitor work", "--project", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "core work", "--project", "core"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.NewWorkspaceStore(options.StateDir).Save(domain.Workspace{
		Version:   domain.WorkspaceVersion,
		ID:        "ws-core",
		Name:      "ws-core",
		Status:    domain.WorkspaceActive,
		Project:   "core",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TWT_WORKSPACE_ID", "ws-core")
	stdout, _, err := executeCollectingInput(t, options, nil, "tickets", "list", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"slug":"core-work"`) || strings.Contains(stdout, `"slug":"monitor-work"`) {
		t.Fatalf("Workspace Project list = %s", stdout)
	}

	textOut, _, err := executeCollectingInput(t, options, nil, "tickets", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(textOut, "PROJECT") || strings.Contains(textOut, "monitor-work") {
		t.Fatalf("Workspace text list = %s", textOut)
	}
	if !strings.Contains(textOut, "core-work") {
		t.Fatalf("Workspace text list missed the scoped Ticket:\n%s", textOut)
	}
}

func TestTicketsQueueUsesTWTProject(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "core"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "Ready work", "--project", "core", "--status", "ready-for-agent"); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TWT_PROJECT", "core")
	stdout, _, err := executeCollectingInput(t, options, nil, "tickets", "queue", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"project":"core"`) || !strings.Contains(stdout, `"slug":"ready-work"`) {
		t.Fatalf("queue from TWT_PROJECT = %s", stdout)
	}
}

func TestTicketsQueueRequiresAProjectWhenUnscoped(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, nil, "tickets", "queue", "--output", "json")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("unscoped queue = %v (code %q)", err, clierr.CodeOf(err))
	}
}
