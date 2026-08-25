package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestContextDirectoryUsesAnAdoptedRepositoryThenFallsBackToTheSession(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	outside := filepath.Join(root, "outside")
	for _, directory := range []string{first, second, outside} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-adopted", Name: "dev-env",
		Status: domain.WorkspaceActive, Adopted: true, Root: first,
		Repositories: []domain.WorkspaceRepository{
			{Name: "first", Path: first},
			{Name: "second", Path: second},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	stateDir := filepath.Join(root, "state")
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}
	t.Setenv("TWT_WORKSPACE_ID", "")
	t.Setenv("TMUX_PANE", "")

	fromSecond := executeWithOptions(t, options, nil, "context", "--directory", second, "--output", "json")
	if !strings.Contains(fromSecond, `"name":"dev-env"`) || !strings.Contains(fromSecond, `"repositoryName":"second"`) {
		t.Fatalf("context from a second adopted repository = %s", fromSecond)
	}

	t.Setenv("TWT_WORKSPACE_ID", workspace.ID)
	fromOutside := executeWithOptions(t, options, nil, "context", "--directory", outside, "--output", "json")
	if !strings.Contains(fromOutside, `"name":"dev-env"`) {
		t.Fatalf("context outside a repository did not use the session Workspace = %s", fromOutside)
	}
}

func TestContextShowsTheTmuxSessionName(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-session", Name: "cm-comment",
		Status: domain.WorkspaceActive, Root: root, TmuxSession: "everysphere-cm-comment",
		CreatedAt: now, UpdatedAt: now,
	}
	stateDir := filepath.Join(root, "state")
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TWT_WORKSPACE_ID", workspace.ID)
	t.Setenv("TMUX_PANE", "")
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}

	text := executeWithOptions(t, options, nil, "context")
	if !strings.Contains(text, "Tmux session") || !strings.Contains(text, "everysphere-cm-comment") {
		t.Fatalf("context text missing tmux session: %s", text)
	}

	jsonOutput := executeWithOptions(t, options, nil, "context", "--output", "json")
	if !strings.Contains(jsonOutput, `"tmuxSession":"everysphere-cm-comment"`) {
		t.Fatalf("context JSON missing tmux session: %s", jsonOutput)
	}
}

func TestContextListsLinkedTicketsAndReadyWork(t *testing.T) {
	options, _ := ticketsStartFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	options.QuickCreateSwitch = func(_, _ string) error { return nil }
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Ready work", "--project", "core", "--status", "ready-for-agent")
	executeWithOptions(t, options, nil, "tickets", "create", "Started work", "--project", "core", "--status", "ready-for-agent")
	executeWithOptions(t, options, nil, "tickets", "start", "started-work", "--as", "tester")
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("started-work")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TWT_WORKSPACE_ID", workspace.ID)

	output := executeWithOptions(t, options, nil, "context", "--output", "json")
	if !strings.Contains(output, `"slug":"started-work"`) {
		t.Fatalf("context missing linked Ticket: %s", output)
	}
	if !strings.Contains(output, `"slug":"ready-work"`) {
		t.Fatalf("context missing ready Ticket: %s", output)
	}
}

func TestContextAcceptsTheLegacyProjectEnvironmentID(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-legacy", Name: "legacy",
		Status: domain.WorkspaceActive, Root: filepath.Join(root, "workspace"),
		CreatedAt: now, UpdatedAt: now,
	}
	stateDir := filepath.Join(root, "state")
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TWT_WORKSPACE_ID", "")
	t.Setenv("TWT_PROJECT_ID", workspace.ID)
	t.Setenv("TMUX_PANE", "")
	output := executeWithOptions(t, cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}, nil, "context", "--output", "json")
	if !strings.Contains(output, `"name":"legacy"`) {
		t.Fatalf("context did not use TWT_PROJECT_ID: %s", output)
	}
}
