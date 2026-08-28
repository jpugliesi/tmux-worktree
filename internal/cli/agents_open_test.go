package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

// agentsOpenFixture saves one active Workspace with a linked Codex Agent
// Session and one discovered Claude session, and returns the CLI options.
func agentsOpenFixture(t *testing.T) (cli.Options, domain.Workspace, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("TMUX_PANE", "")
	repository := filepath.Join(root, "workspace", "app")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-one", Name: "workspace-one",
		Status: domain.WorkspaceActive, Root: filepath.Dir(repository), TmuxSession: "workspace-one",
		Repositories: []domain.WorkspaceRepository{{Name: "app", Path: repository}},
		CreatedAt:    now, UpdatedAt: now,
	}
	stateDir := filepath.Join(root, "state")
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TWT_WORKSPACE_ID", workspace.ID)
	writeTestLines(t, filepath.Join(home, ".codex", "sessions", "2026", "08", "20", "rollout-session-one.jsonl"),
		`{"type":"session_meta","payload":{"id":"session-one","cwd":`+quoteJSON(t, repository)+`}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"Workspace question"}]}}
{"type":"response_item","payload":{"role":"assistant","content":[{"type":"output_text","text":"Workspace answer"}]}}
`)
	writeTestLines(t, filepath.Join(home, ".claude", "projects", "-user-code-app", "claude-one.jsonl"),
		`{"sessionId":"claude-one","cwd":`+quoteJSON(t, repository)+`,"type":"user","message":{"role":"user","content":"Claude question"}}
{"sessionId":"claude-one","cwd":`+quoteJSON(t, repository)+`,"type":"assistant","message":{"role":"assistant","content":"Claude answer"}}
`)
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}
	registration := executeWithOptions(t, options, nil,
		"agents", "register", "--workspace", workspace.ID, "--provider", "codex", "--label", "review",
		"--session", "session-one", "--", "codex", "resume", "session-one",
	)
	fields := strings.Fields(registration)
	if len(fields) < 4 {
		t.Fatalf("registration output = %q", registration)
	}
	return options, workspace, fields[3]
}

func TestAgentsOpenRefusesJSONOutput(t *testing.T) {
	options, _, agentID := agentsOpenFixture(t)
	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetArgs(forceTextOutput([]string{"agents", "open", agentID, "--output", "json"}))
	err := command.Execute()
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(err.Error(), "interactive") {
		t.Fatalf("agents open with JSON output = %v (code %q)", err, clierr.CodeOf(err))
	}
}

func TestAgentsOpenPickerListsRegisteredAndDiscoveredSessions(t *testing.T) {
	options, workspace, agentID := agentsOpenFixture(t)
	listed := executeWithOptions(t, options, nil, "agents", "list", "--workspace", workspace.ID)
	if strings.Contains(listed, "\t") || !strings.Contains(listed, "PROVIDER") || !strings.Contains(listed, agentID) {
		t.Fatalf("agents list text = %q", listed)
	}
	var pickedLines []string
	options.AgentPick = func(_ *cobra.Command, lines []string) (int, error) {
		pickedLines = append([]string(nil), lines...)
		return 0, nil
	}
	output := executeWithOptions(t, options, nil, "agents", "open", "--workspace", workspace.ID, "--dry-run")
	if len(pickedLines) != 2 {
		t.Fatalf("agents open picker lines = %v", pickedLines)
	}
	if !strings.HasPrefix(pickedLines[0], "codex\t"+agentID+"\t") {
		t.Fatalf("registered picker line = %q", pickedLines[0])
	}
	if !strings.HasPrefix(pickedLines[1], "claude\t"+agentservice.TranscriptReference("claude", "claude-one")+"\t") {
		t.Fatalf("discovered picker line = %q", pickedLines[1])
	}
	if !strings.Contains(output, "agents.open: valid") {
		t.Fatalf("agents open picker dry-run output = %q", output)
	}
}

func TestAgentsOpenPickerResumesTheSelectedDiscoveredSessionWithoutAdoptingOnDryRun(t *testing.T) {
	options, workspace, _ := agentsOpenFixture(t)
	before, err := store.NewAgentStore(options.StateDir).List(workspace.ID)
	if err != nil || len(before) != 1 {
		t.Fatalf("registered Agent Sessions = %+v, %v", before, err)
	}
	options.AgentPick = func(_ *cobra.Command, lines []string) (int, error) {
		return 1, nil
	}
	output := executeWithOptions(t, options, nil, "agents", "open", "--dry-run")
	if !strings.Contains(output, "agents.open: valid") {
		t.Fatalf("discovered picker dry-run output = %q", output)
	}
	after, err := store.NewAgentStore(options.StateDir).List(workspace.ID)
	if err != nil || len(after) != 1 || after[0].ID != before[0].ID {
		t.Fatalf("dry-run picker adopted a session: %+v, %v", after, err)
	}
}

func TestAgentsOpenNumberedPickerReadsTheAgentNumber(t *testing.T) {
	options, _, agentID := agentsOpenFixture(t)
	t.Setenv("PATH", "")
	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetIn(strings.NewReader("1\n"))
	command.SetArgs(forceTextOutput([]string{"agents", "open", "--dry-run"}))
	if err := command.Execute(); err != nil {
		t.Fatalf("agents open with the numbered picker: %v", err)
	}
	if !strings.Contains(stderr.String(), "1) codex\t"+agentID) || !strings.Contains(stderr.String(), "Agent Session number: ") {
		t.Fatalf("numbered picker prompt = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "agents.open: valid") {
		t.Fatalf("numbered picker dry-run output = %q", stdout.String())
	}

	var badStdout, badStderr bytes.Buffer
	badOptions := options
	badOptions.Stdout, badOptions.Stderr = &badStdout, &badStderr
	badCommand := cli.New(badOptions)
	badCommand.SetIn(strings.NewReader("9\n"))
	badCommand.SetArgs(forceTextOutput([]string{"agents", "open", "--dry-run"}))
	err := badCommand.Execute()
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(err.Error(), "between 1 and 2") {
		t.Fatalf("numbered picker with an invalid number = %v", err)
	}
}

func TestAgentsOpenPreviewWritesTheSameMarkdownAsTranscriptShow(t *testing.T) {
	options, workspace, agentID := agentsOpenFixture(t)
	shown := executeWithOptions(t, options, nil, "agents", "transcript", "get", agentID, "--workspace", workspace.ID)
	preview := executeWithOptions(t, options, nil, "agents", "open", "--preview", agentID, "--workspace", workspace.ID)
	if preview != shown {
		t.Fatalf("open --preview markdown = %q, transcript show = %q", preview, shown)
	}
	if !strings.Contains(preview, "Workspace question") || !strings.Contains(preview, "Workspace answer") {
		t.Fatalf("preview markdown = %q", preview)
	}
}

func TestAgentsOpenPreviewWritesStructuredJSONWithoutAdopting(t *testing.T) {
	options, workspace, _ := agentsOpenFixture(t)
	before := directorySnapshot(t, options.StateDir)
	stdout, _, err := executeRaw(t, options,
		"agents", "open", "--preview", "claude-one", "--workspace", workspace.ID, "--output", "json")
	if err != nil {
		t.Fatalf("open --preview JSON: %v", err)
	}
	var value struct {
		SchemaVersion int    `json:"schemaVersion"`
		WorkspaceID   string `json:"workspaceId"`
		AgentID       string `json:"agentId"`
		Untrusted     bool   `json:"untrusted"`
		Markdown      string `json:"markdown"`
	}
	if err := json.Unmarshal([]byte(stdout), &value); err != nil {
		t.Fatalf("decode open --preview JSON: %v\n%s", err, stdout)
	}
	if value.SchemaVersion != 2 || value.WorkspaceID != workspace.ID || value.AgentID != "claude-one" || !value.Untrusted {
		t.Fatalf("open --preview JSON = %+v", value)
	}
	if !strings.Contains(value.Markdown, "Claude question") || !strings.Contains(value.Markdown, "Claude answer") {
		t.Fatalf("open --preview markdown = %q", value.Markdown)
	}
	after := directorySnapshot(t, options.StateDir)
	if len(after) != len(before) {
		t.Fatal("open --preview JSON changed the state directory")
	}
	listed, listErr := store.NewAgentStore(options.StateDir).List(workspace.ID)
	if listErr != nil || len(listed) != 1 {
		t.Fatalf("open --preview JSON adopted a session: %+v, %v", listed, listErr)
	}
}

func TestAgentsOpenPreviewDoesNotAdoptADiscoveredSession(t *testing.T) {
	options, workspace, _ := agentsOpenFixture(t)
	before := directorySnapshot(t, options.StateDir)
	preview := executeWithOptions(t, options, nil, "agents", "open", "--preview", "claude-one", "--workspace", workspace.ID)
	if !strings.Contains(preview, "Claude question") || !strings.Contains(preview, "Claude answer") {
		t.Fatalf("discovered preview markdown = %q", preview)
	}
	after := directorySnapshot(t, options.StateDir)
	if len(after) != len(before) {
		t.Fatalf("open --preview changed the state directory")
	}
	listed, err := store.NewAgentStore(options.StateDir).List(workspace.ID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("open --preview adopted a session: %+v, %v", listed, err)
	}
}

func TestAgentsOpenPreviewWritesMarkdownWithoutATerminal(t *testing.T) {
	options, workspace, agentID := agentsOpenFixture(t)
	stdout, _, err := executeRaw(t, options, "agents", "open", "--preview", agentID, "--workspace", workspace.ID)
	if err != nil {
		t.Fatalf("open --preview without --output: %v", err)
	}
	if strings.Contains(stdout, "schemaVersion") || !strings.Contains(stdout, "Workspace question") {
		t.Fatalf("open --preview without a terminal = %q", stdout)
	}
}

func TestAgentsOpenRunsTheProviderResumeCommand(t *testing.T) {
	options, workspace, agentID := agentsOpenFixture(t)
	var ran []string
	options.AgentOpenExec = func(_ string, argv []string, _ []string) error {
		ran = append([]string(nil), argv...)
		return nil
	}

	output := executeWithOptions(t, options, nil, "agents", "open", agentID)
	if strings.Join(ran, " ") != "codex resume session-one" {
		t.Fatalf("registered open command = %v, output = %q", ran, output)
	}

	ran = nil
	options.AgentPick = func(_ *cobra.Command, _ []string) (int, error) {
		return 1, nil
	}
	output = executeWithOptions(t, options, nil, "agents", "open", "--workspace", workspace.ID)
	if strings.Join(ran, " ") != "claude --resume claude-one" {
		t.Fatalf("discovered open command = %v, output = %q", ran, output)
	}
	listed, err := store.NewAgentStore(options.StateDir).List(workspace.ID)
	if err != nil || len(listed) != 2 {
		t.Fatalf("open did not adopt the discovered session: %+v, %v", listed, err)
	}
}

func TestAgentsOpenWithAgentIDResumesWithoutAPicker(t *testing.T) {
	options, _, agentID := agentsOpenFixture(t)
	called := false
	options.AgentPick = func(*cobra.Command, []string) (int, error) {
		called = true
		return 0, nil
	}
	output := executeWithOptions(t, options, nil, "agents", "open", agentID, "--dry-run")
	if called {
		t.Fatal("agents open AGENT_ID opened the picker")
	}
	if !strings.Contains(output, "agents.open: valid") {
		t.Fatalf("agents open AGENT_ID dry-run output = %q", output)
	}
}

func TestAgentsOpenWithNoAgentsReportsNotFound(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "empty-id", Name: "empty",
		Status: domain.WorkspaceActive, TmuxSession: "empty", CreatedAt: now, UpdatedAt: now,
	}
	options := cli.Options{StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data")}
	if err := store.NewWorkspaceStore(options.StateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TWT_WORKSPACE_ID", workspace.ID)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("HOME", filepath.Join(root, "home"))
	_, _, err := executeCollectingOutput(t, options, "agents", "open", "--dry-run")
	if err == nil || clierr.CodeOf(err) != clierr.NotFound || !strings.Contains(err.Error(), "no Agent Sessions") {
		t.Fatalf("agents open with no sessions = %v", err)
	}
}
