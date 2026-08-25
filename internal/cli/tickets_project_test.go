package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestTicketsUseSavesShowsAndUnsetsCurrentProject(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "change-monitor"); err != nil {
		t.Fatal(err)
	}

	dryJSON, _, err := executeCollectingInput(t, options, nil,
		"tickets", "use", "change-monitor", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	preview := decodeTicketMutation(t, dryJSON)
	if preview.Operation != "tickets.use" || preview.Status != "valid" || preview.ID != "change-monitor" {
		t.Fatalf("use dry-run envelope = %+v\n%s", preview, dryJSON)
	}
	if name, err := store.LoadCurrentProject(options.StateDir); err != nil || name != "" {
		t.Fatalf("dry-run use wrote %q, error=%v", name, err)
	}

	jsonOut, _, err := executeCollectingInput(t, options, nil,
		"tickets", "use", "change-monitor", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	applied := decodeTicketMutation(t, jsonOut)
	if applied.Operation != "tickets.use" || applied.Status != "applied" || applied.ID != "change-monitor" {
		t.Fatalf("use envelope = %+v\n%s", applied, jsonOut)
	}

	showOut, _, err := executeCollectingInput(t, options, nil, "tickets", "use", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var shown struct {
		Project string `json:"project"`
	}
	if err := json.Unmarshal([]byte(showOut), &shown); err != nil {
		t.Fatalf("decode use show: %v\n%s", err, showOut)
	}
	if shown.Project != "change-monitor" {
		t.Fatalf("use show = %s", showOut)
	}

	textOut, _, err := executeCollectingInput(t, options, nil, "tickets", "use", "--unset")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textOut, "Cleared the saved current Project") {
		t.Fatalf("use --unset = %q", textOut)
	}
	if name, err := store.LoadCurrentProject(options.StateDir); err != nil || name != "" {
		t.Fatalf("use --unset left %q, error=%v", name, err)
	}
}

func TestTicketsUseRejectsAMissingProject(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, nil, "tickets", "use", "missing")
	if err == nil || clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("use missing Project = %v (code %q)", err, clierr.CodeOf(err))
	}
}

func TestTicketsListRequiresAProjectWhenUnscoped(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, nil, "tickets", "list", "--output", "json")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("unscoped JSON list = %v (code %q)", err, clierr.CodeOf(err))
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "--project") || !strings.Contains(hint, "--all-projects") {
		t.Fatalf("unscoped list hint = %q", hint)
	}
}

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
}

func TestTicketsListUsesSavedProjectOnlyForInteractiveText(t *testing.T) {
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
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "use", "change-monitor"); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCollectingInput(t, options, nil, "tickets", "list")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("non-interactive text list used the saved Project: %v", err)
	}

	textOut, _, err := executeCollectingInput(t, options, strings.NewReader(""), "tickets", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textOut, "monitor-work") || strings.Contains(textOut, "core-work") {
		t.Fatalf("interactive text list = %s", textOut)
	}
	if strings.Contains(textOut, "PROJECT") {
		t.Fatalf("saved Project list still has a PROJECT column:\n%s", textOut)
	}

	_, _, err = executeCollectingInput(t, options, strings.NewReader(""), "tickets", "list", "--output", "json")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("interactive JSON list used the saved Project: %v", err)
	}
}

func TestTicketsCreateDoesNotSaveCurrentProject(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "monitor work", "--project", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	if name, err := store.LoadCurrentProject(options.StateDir); err != nil || name != "" {
		t.Fatalf("tickets create wrote current Project %q, error=%v", name, err)
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
