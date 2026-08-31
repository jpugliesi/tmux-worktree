package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

// ticketsStartFixture builds options with the "example" template, a private
// tmux server, and a temporary Tickets home.
func ticketsStartFixture(t *testing.T) (cli.Options, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: example\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "example.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	home := filepath.Join(root, "tickets")
	options := cli.Options{
		ConfigDir:   configDir,
		StateDir:    filepath.Join(root, "state"),
		DataDir:     filepath.Join(root, "data"),
		TmuxSocket:  socket,
		TicketsHome: home,
	}
	return options, home
}

// enterCurrentWorkspaceForNext creates a Workspace and gives the test a tmux
// client in its session. The caller controls the switch and archive hooks.
func enterCurrentWorkspaceForNext(t *testing.T, options cli.Options) domain.Workspace {
	t.Helper()
	executeWithOptions(t, options, nil, "create", "current", "--template", "example", "--no-open")
	current, err := store.NewWorkspaceStore(options.StateDir).Find("current")
	if err != nil {
		t.Fatal(err)
	}
	pane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-panes", "-t", "=example-current", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", pane)
	t.Setenv("TWT_WORKSPACE_ID", "")
	attachControlClient(t, options.TmuxSocket, current.TmuxSession)
	return current
}

func TestTicketsStartClaimsCreatesLinksAndComments(t *testing.T) {
	options, home := ticketsStartFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")
	var switched []string
	options.QuickCreateSwitch = func(_ string, session string) error {
		switched = append(switched, session)
		return nil
	}

	output := executeWithOptions(t, options, nil, "tickets", "start", "fix-auth-tokens", "--as", "tester")
	if !strings.Contains(output, `Claimed ticket "fix-auth-tokens" as "tester"`) {
		t.Fatalf("tickets start output has no claim: %q", output)
	}
	if !strings.Contains(output, `Created Workspace "fix-auth-tokens"`) {
		t.Fatalf("tickets start output has no create: %q", output)
	}
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("fix-auth-tokens")
	if err != nil || workspace.Status != domain.WorkspaceActive {
		t.Fatalf("Workspace after tickets start: %+v error=%v", workspace, err)
	}
	if len(workspace.Tickets) != 1 || workspace.Tickets[0] != "fix-auth-tokens" {
		t.Fatalf("Workspace tickets = %v, want [fix-auth-tokens]", workspace.Tickets)
	}
	if len(switched) != 1 || switched[0] != workspace.TmuxSession {
		t.Fatalf("tickets start switch events = %v", switched)
	}
	content := readTicketFile(t, filepath.Join(home, "core", "fix-auth-tokens.md"))
	if !strings.Contains(content, "tester") {
		t.Fatalf("ticket is not claimed:\n%s", content)
	}
	if !strings.Contains(content, "Started Workspace fix-auth-tokens.") {
		t.Fatalf("ticket has no start comment:\n%s", content)
	}
	if !strings.Contains(content, "twt_workspace_id: "+workspace.ID) {
		t.Fatalf("ticket has no Workspace stamp:\n%s", content)
	}
	showTicket := executeWithOptions(t, options, nil, "tickets", "get", "fix-auth-tokens", "--output", "json")
	if !strings.Contains(showTicket, `"workspaceId":"`+workspace.ID+`"`) {
		t.Fatalf("tickets show JSON has no workspaceId: %s", showTicket)
	}

	// workspaces show reports the link in text and JSON.
	show := executeWithOptions(t, options, nil, "workspaces", "get", "fix-auth-tokens")
	if !strings.Contains(show, "Ticket") || !strings.Contains(show, "fix-auth-tokens") {
		t.Fatalf("workspaces show has no Ticket line: %q", show)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "workspaces", "get", "fix-auth-tokens", "--output", "json")
	if err != nil || !strings.Contains(showJSON, `"tickets":["fix-auth-tokens"]`) {
		t.Fatalf("workspaces show JSON has no tickets field: %q error=%v", showJSON, err)
	}

	// --name overrides the Workspace name; the link keeps the Ticket slug.
	executeWithOptions(t, options, nil, "tickets", "create", "Second thing", "--project", "core")
	named := executeWithOptions(t, options, nil, "tickets", "start", "second-thing", "--as", "tester", "--name", "custom-app")
	if !strings.Contains(named, `Created Workspace "custom-app"`) {
		t.Fatalf("tickets start --name output = %q", named)
	}
	namedWorkspace, err := store.NewWorkspaceStore(options.StateDir).Find("custom-app")
	if err != nil || len(namedWorkspace.Tickets) != 1 || namedWorkspace.Tickets[0] != "second-thing" {
		t.Fatalf("Workspace after tickets start --name: %+v error=%v", namedWorkspace, err)
	}
	namedContent := readTicketFile(t, filepath.Join(home, "core", "second-thing.md"))
	if !strings.Contains(namedContent, "Started Workspace custom-app.") {
		t.Fatalf("ticket has no start comment for --name:\n%s", namedContent)
	}
}

func TestTicketsStartWithAgentDetachedWritesOneJSONResult(t *testing.T) {
	options, home := ticketsStartFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	fakeBin := buildFakePlanningProvider(t, "codex")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	writeTwtConfigFile(t, options.ConfigDir, `ticketAgent:
  provider: codex
  effort: xlarge
  instructions: |
    Read CONTEXT.md first.
`)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")
	switches := 0
	options.QuickCreateSwitch = func(_, _ string) error {
		switches++
		return nil
	}

	stdout, stderr, err := executeCollectingInput(t, options, nil,
		"tickets", "start", "fix-auth-tokens", "--as", "tester", "--with-agent", "-d", "--output", "json")
	if err != nil {
		t.Fatalf("detached start: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		SchemaVersion int    `json:"schemaVersion"`
		Operation     string `json:"operation"`
		Status        string `json:"status"`
		ID            string `json:"id"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("detached JSON is not one value: %v\n%s", err, stdout)
	}
	if result.SchemaVersion != 2 || result.Operation != "tickets.start" || result.Status != "applied" || result.ID == "" {
		t.Fatalf("detached result = %+v", result)
	}
	if switches != 0 {
		t.Fatalf("detached start switched %d times", switches)
	}
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find(result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.TemplateSnapshot.Agents) != 1 {
		t.Fatalf("snapshot agents = %+v", workspace.TemplateSnapshot.Agents)
	}
	declared := workspace.TemplateSnapshot.Agents[0]
	if declared.Label != "ticket-plan" || declared.Provider != "codex" || len(declared.Resume) == 0 {
		t.Fatalf("generated declaration = %+v", declared)
	}
	prompt := declared.Start[len(declared.Start)-1]
	if !strings.HasPrefix(prompt, "Read CONTEXT.md first.\n\n") || !strings.Contains(prompt, "twt tickets get fix-auth-tokens --output json") {
		t.Fatalf("generated prompt = %q", prompt)
	}
	if strings.Contains(strings.Join(declared.Resume, " "), "fix-auth-tokens") {
		t.Fatalf("resume command repeats the Ticket prompt: %v", declared.Resume)
	}
	sessions, err := store.NewAgentStore(options.StateDir).List(workspace.ID)
	if err != nil || len(sessions) != 1 || sessions[0].Label != "ticket-plan" || !sessions[0].PreferProviderResume {
		t.Fatalf("Agent Sessions = %+v, error=%v", sessions, err)
	}
	content := readTicketFile(t, filepath.Join(home, "core", "fix-auth-tokens.md"))
	for _, want := range []string{"claimed_by: tester", "Started Workspace fix-auth-tokens.", "twt_workspace_id: " + workspace.ID} {
		if !strings.Contains(content, want) {
			t.Fatalf("Ticket does not contain %q:\n%s", want, content)
		}
	}
}

func TestTicketsStartDetachedJSONDryRunChangesNoState(t *testing.T) {
	options, home := ticketsStartFixture(t)
	fakeBin := buildFakePlanningProvider(t, "codex")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")

	stdout, stderr, err := executeCollectingInput(t, options, nil,
		"tickets", "start", "fix-auth-tokens", "--as", "tester", "--with-agent", "--detached", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("detached dry-run: %v\nstderr: %s", err, stderr)
	}
	var result struct {
		Operation string `json:"operation"`
		Status    string `json:"status"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.Operation != "tickets.start" || result.Status != "valid" {
		t.Fatalf("dry-run result = %+v, error=%v\n%s", result, err, stdout)
	}
	if _, err := store.NewWorkspaceStore(options.StateDir).Find("fix-auth-tokens"); err == nil {
		t.Fatal("dry-run created a Workspace")
	}
	content := readTicketFile(t, filepath.Join(home, "core", "fix-auth-tokens.md"))
	if strings.Contains(content, "claimed_by: tester") || strings.Contains(content, "Started Workspace") {
		t.Fatalf("dry-run changed the Ticket:\n%s", content)
	}
}

func TestTicketsStartAgentValidationRunsBeforeTheClaim(t *testing.T) {
	options, home := ticketsStartFixture(t)
	writeTwtConfigFile(t, options.ConfigDir, "ticketAgent:\n  provider: grok\n  effort: large\n")
	t.Setenv("PATH", t.TempDir())
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")

	_, _, err := executeCollectingInput(t, options, nil,
		"tickets", "start", "fix-auth-tokens", "--as", "tester", "--with-agent", "--detached", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), "grok") {
		t.Fatalf("missing provider error = %v", err)
	}
	content := readTicketFile(t, filepath.Join(home, "core", "fix-auth-tokens.md"))
	if strings.Contains(content, "claimed_by: tester") {
		t.Fatalf("provider validation failure claimed the Ticket:\n%s", content)
	}
}

func TestTicketsStartAgentFailureKeepsAClaimedRetryableWorkspace(t *testing.T) {
	options, home := ticketsStartFixture(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")

	_, _, err := executeCollectingInput(t, options, nil,
		"tickets", "start", "fix-auth-tokens", "--as", "tester", "--with-agent", "--detached", "--output", "json")
	if err == nil {
		t.Fatal("a provider that exits immediately did not fail Workspace setup")
	}
	workspace, findErr := store.NewWorkspaceStore(options.StateDir).Find("fix-auth-tokens")
	if findErr != nil || workspace.Status != domain.WorkspaceSetupFailed {
		t.Fatalf("failed Workspace = %+v, error=%v", workspace, findErr)
	}
	content := readTicketFile(t, filepath.Join(home, "core", "fix-auth-tokens.md"))
	if !strings.Contains(content, "tester") {
		t.Fatalf("Agent failure did not keep the Ticket claim:\n%s", content)
	}
	if strings.Contains(content, "Started Workspace") || strings.Contains(content, "twt_workspace_id: "+workspace.ID) {
		t.Fatalf("Agent failure recorded a successful start:\n%s", content)
	}
}

func buildFakePlanningProvider(t *testing.T, name string) string {
	t.Helper()
	directory := t.TempDir()
	source := filepath.Join(directory, "main.go")
	if err := os.WriteFile(source, []byte("package main\nimport \"time\"\nfunc main() { time.Sleep(time.Hour) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "build", "-o", filepath.Join(directory, name), source)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fake planning provider: %v\n%s", err, output)
	}
	return directory
}

func TestProjectsShowReportsTheCoordinatorBoard(t *testing.T) {
	options, _ := ticketsStartFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	options.QuickCreateSwitch = func(_, _ string) error { return nil }
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Ready work", "--project", "core", "--status", "ready-for-agent")
	executeWithOptions(t, options, nil, "tickets", "create", "Started work", "--project", "core", "--status", "ready-for-agent")
	executeWithOptions(t, options, nil, "tickets", "start", "started-work", "--as", "tester")

	output := executeWithOptions(t, options, nil, "projects", "get", "core", "--output", "json")
	if !strings.Contains(output, `"slug":"ready-work"`) {
		t.Fatalf("ready Ticket missing from Project board: %s", output)
	}
	if !strings.Contains(output, `"slug":"started-work"`) || !strings.Contains(output, `"inFlight"`) {
		t.Fatalf("in-flight Ticket missing from Project board: %s", output)
	}
	if !strings.Contains(output, `"name":"started-work"`) {
		t.Fatalf("Workspace missing from Project board: %s", output)
	}

	claimed := executeWithOptions(t, options, nil, "tickets", "list", "--project", "core", "--claimed", "--output", "json")
	if !strings.Contains(claimed, `"slug":"started-work"`) || strings.Contains(claimed, `"slug":"ready-work"`) {
		t.Fatalf("--claimed list = %s", claimed)
	}
}

func TestTicketsStartKeepsTheCurrentWorkspace(t *testing.T) {
	options, _ := ticketsStartFixture(t)
	current := enterCurrentWorkspaceForNext(t, options)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")
	options.QuickCreateSwitch = func(_, _ string) error { return nil }

	output := executeWithOptions(t, options, nil, "tickets", "start", "fix-auth-tokens", "--as", "tester")
	if strings.Contains(output, "archiving Workspace") {
		t.Fatalf("tickets start archived the current Workspace:\n%s", output)
	}
	if !strings.Contains(output, `Workspace "current" stays active`) {
		t.Fatalf("tickets start output = %q", output)
	}
	old, err := store.NewWorkspaceStore(options.StateDir).Find(current.ID)
	if err != nil || old.Status != domain.WorkspaceActive {
		t.Fatalf("current Workspace after tickets start: status=%q error=%v", old.Status, err)
	}
}

func TestTicketsStartCreatesOneWorkspaceForManyTickets(t *testing.T) {
	options, home := ticketsStartFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth", "--project", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Add auth tests", "--project", "core")
	options.QuickCreateSwitch = func(_, _ string) error { return nil }

	executeWithOptions(t, options, nil, "tickets", "start", "fix-auth", "add-auth-tests", "--as", "tester")
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Project != "core" {
		t.Fatalf("Workspace Project = %q", workspace.Project)
	}
	if got := strings.Join(workspace.Tickets, ","); got != "fix-auth,add-auth-tests" {
		t.Fatalf("Workspace Tickets = %q", got)
	}
	for _, slug := range workspace.Tickets {
		content := readTicketFile(t, filepath.Join(home, "core", slug+".md"))
		if !strings.Contains(content, "claimed_by: tester") || !strings.Contains(content, "Started Workspace fix-auth.") {
			t.Fatalf("Ticket %q was not started:\n%s", slug, content)
		}
	}
}

func TestWorkspacesCreateLinksRepeatedTicketFlags(t *testing.T) {
	options, _ := ticketsStartFixture(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth", "--project", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Add auth tests", "--project", "core")

	executeWithOptions(t, options, nil, "workspaces", "create", "auth-work", "--template", "example", "--no-open",
		"--ticket", "fix-auth", "--ticket", "add-auth-tests")
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("auth-work")
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Project != "core" || strings.Join(workspace.Tickets, ",") != "fix-auth,add-auth-tests" {
		t.Fatalf("Workspace links = Project %q, Tickets %v", workspace.Project, workspace.Tickets)
	}
}

func TestApplyWorkspacesCreateLinksTickets(t *testing.T) {
	options, _ := ticketsStartFixture(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth", "--project", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Add auth tests", "--project", "core")

	request := `{"operation":"workspaces.create","workspace":{"name":"auth-work","template":"example","tickets":["fix-auth","add-auth-tests"]}}`
	if _, _, err := executeCollectingInput(t, options, strings.NewReader(request), "apply", "-"); err != nil {
		t.Fatal(err)
	}
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("auth-work")
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Project != "core" || strings.Join(workspace.Tickets, ",") != "fix-auth,add-auth-tests" {
		t.Fatalf("Workspace links = Project %q, Tickets %v", workspace.Project, workspace.Tickets)
	}
}

func TestNextAcceptsManyTicketSlugs(t *testing.T) {
	options, _ := ticketsStartFixture(t)
	enterCurrentWorkspaceForNext(t, options)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth", "--project", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Add auth tests", "--project", "core")

	executeWithOptions(t, options, nil, "next", "fix-auth", "add-auth-tests", "--as", "tester")
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(workspace.Tickets, ",") != "fix-auth,add-auth-tests" {
		t.Fatalf("Workspace Tickets = %v", workspace.Tickets)
	}
}

func TestTicketsStartRefusesClosedLockedAndJSON(t *testing.T) {
	options, home := ticketTestOptions(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")

	// A Ticket that a different claimant holds aborts before any Workspace
	// work.
	executeWithOptions(t, options, nil, "tickets", "claim", "fix-auth-tokens", "--as", "other")
	_, _, err := executeCollectingInput(t, options, nil, "tickets", "start", "fix-auth-tokens", "--as", "tester")
	if clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("tickets start on a claimed Ticket = %v (code %q)", err, clierr.CodeOf(err))
	}
	workspaces, listErr := store.NewWorkspaceStore(options.StateDir).List()
	if listErr != nil || len(workspaces) != 0 {
		t.Fatalf("tickets start on a claimed Ticket made Workspaces: %+v error=%v", workspaces, listErr)
	}
	if !strings.Contains(readTicketFile(t, filepath.Join(home, "core", "fix-auth-tokens.md")), "other") {
		t.Fatal("the locked start changed the claim")
	}

	// A closed Ticket is refused.
	executeWithOptions(t, options, nil, "tickets", "close", "fix-auth-tokens", "--as", "other")
	_, _, err = executeCollectingInput(t, options, nil, "tickets", "start", "fix-auth-tokens", "--as", "tester")
	if clierr.CodeOf(err) != clierr.PreconditionFailed || !strings.Contains(err.Error(), "is closed") {
		t.Fatalf("tickets start on a closed Ticket = %v (code %q)", err, clierr.CodeOf(err))
	}

	// JSON output is refused before any work.
	_, _, err = executeCollectingInput(t, options, nil, "tickets", "start", "fix-auth-tokens", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), "interactive text output") {
		t.Fatalf("tickets start with JSON output = %v", err)
	}
	_, _, err = executeCollectingInput(t, options, strings.NewReader("1\n"), "tickets", "start", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), "interactive text output") {
		t.Fatalf("tickets start picker with JSON output = %v", err)
	}
}

func TestTicketsStartRequiresUniqueTicketsFromOneProject(t *testing.T) {
	options, home := ticketTestOptions(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "tickets", "init")
	for _, project := range []string{"core", "docs"} {
		executeWithOptions(t, options, nil, "projects", "create", project)
	}
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth", "--project", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Write guide", "--project", "docs")

	_, _, err := executeCollectingInput(t, options, nil, "tickets", "start", "fix-auth", "write-guide", "--as", "tester")
	if clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(err.Error(), "one Project") {
		t.Fatalf("cross-Project start = %v", err)
	}
	_, _, err = executeCollectingInput(t, options, nil, "tickets", "start", "fix-auth", "fix-auth", "--as", "tester")
	if clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate start = %v", err)
	}
	if strings.Contains(readTicketFile(t, filepath.Join(home, "core", "fix-auth.md")), "claimed_by: tester") {
		t.Fatal("an invalid multi-Ticket start claimed a Ticket")
	}
}

func TestTicketsStartKeepsTheClaimWhenTheCreateFails(t *testing.T) {
	options, home := ticketTestOptions(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	// The only template has no repositories, so the create validation fails
	// after the claim.
	if err := os.MkdirAll(filepath.Join(options.ConfigDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.ConfigDir, "templates", "empty.yaml"),
		[]byte("version: 1\nname: empty\nrepositories: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")

	_, _, err := executeCollectingInput(t, options, nil, "tickets", "start", "fix-auth-tokens", "--as", "tester")
	if err == nil || !strings.Contains(err.Error(), "no repositories") {
		t.Fatalf("tickets start with a broken template = %v", err)
	}
	content := readTicketFile(t, filepath.Join(home, "core", "fix-auth-tokens.md"))
	if !strings.Contains(content, "tester") {
		t.Fatalf("the failed create released the claim:\n%s", content)
	}
	workspaces, listErr := store.NewWorkspaceStore(options.StateDir).List()
	if listErr != nil || len(workspaces) != 0 {
		t.Fatalf("the failed create made Workspaces: %+v error=%v", workspaces, listErr)
	}
}

func TestTicketsStartPickerClaimsTheSelectedTicket(t *testing.T) {
	options, home := ticketsStartFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Ungrouped work")
	executeWithOptions(t, options, nil, "tickets", "create", "Second thing", "--project", "core")
	executeWithOptions(t, options, nil, "tickets", "close", "second-thing", "--as", "tester")
	var pickedLines []string
	options.TicketPick = func(_ *cobra.Command, lines []string) (int, error) {
		pickedLines = append([]string(nil), lines...)
		return 0, nil
	}
	var switched []string
	options.QuickCreateSwitch = func(_ string, session string) error {
		switched = append(switched, session)
		return nil
	}

	output := executeWithOptions(t, options, nil, "tickets", "start", "--as", "tester")
	if len(pickedLines) != 1 {
		t.Fatalf("tickets start picker lines = %v", pickedLines)
	}
	if !strings.HasPrefix(pickedLines[0], "fix-auth-tokens\t") {
		t.Fatalf("tickets start picker line = %q", pickedLines[0])
	}
	if !strings.Contains(output, `Claimed ticket "fix-auth-tokens" as "tester"`) {
		t.Fatalf("tickets start picker output has no claim: %q", output)
	}
	if !strings.Contains(output, `Created Workspace "fix-auth-tokens"`) {
		t.Fatalf("tickets start picker output has no create: %q", output)
	}
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("fix-auth-tokens")
	if err != nil || len(workspace.Tickets) != 1 || workspace.Tickets[0] != "fix-auth-tokens" {
		t.Fatalf("Workspace after tickets start picker: %+v error=%v", workspace, err)
	}
	if len(switched) != 1 || switched[0] != workspace.TmuxSession {
		t.Fatalf("tickets start picker switch events = %v", switched)
	}
	content := readTicketFile(t, filepath.Join(home, "core", "fix-auth-tokens.md"))
	if !strings.Contains(content, "tester") {
		t.Fatalf("picked ticket is not claimed:\n%s", content)
	}
}

func TestTicketsStartNumberedPickerReadsTheTicketNumber(t *testing.T) {
	options, _ := ticketsStartFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	t.Setenv("PATH", "")
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")
	output := executeWithOptions(t, options, strings.NewReader("1\n"), "tickets", "start", "--as", "tester", "--dry-run")
	if !strings.Contains(output, "tickets.claim: valid") || !strings.Contains(output, "workspaces.next: valid") {
		t.Fatalf("tickets start numbered picker dry-run output = %q", output)
	}
	if _, err := store.NewWorkspaceStore(options.StateDir).Find("fix-auth-tokens"); err == nil {
		t.Fatal("tickets start numbered picker dry-run created a Workspace")
	}
}

func TestTicketsStartWithoutTicketNeedsATerminal(t *testing.T) {
	options, _ := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")
	_, _, err := executeRaw(t, options, "tickets", "start")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("tickets start without TICKET = %v (code %q)", err, clierr.CodeOf(err))
	}
	if !strings.Contains(err.Error(), "missing Ticket") {
		t.Fatalf("tickets start without a terminal = %v", err)
	}
	_, _, err = executeRaw(t, options, "tickets", "start", "--output", "json")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(err.Error(), "interactive text output") {
		t.Fatalf("tickets start --output json without TICKET = %v", err)
	}
}

func TestTicketsStartWithoutTicketRefusesWhenNoneAreStartable(t *testing.T) {
	options, _ := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	_, _, err := executeCollectingInput(t, options, strings.NewReader("1\n"), "tickets", "start", "--as", "tester")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(err.Error(), "no startable Tickets") {
		t.Fatalf("tickets start with no Tickets = %v", err)
	}
	executeWithOptions(t, options, nil, "tickets", "create", "Ungrouped work")
	_, _, err = executeCollectingInput(t, options, strings.NewReader("1\n"), "tickets", "start", "--as", "tester")
	if err == nil || !strings.Contains(err.Error(), "no startable Tickets") {
		t.Fatalf("tickets start with only ungrouped Tickets = %v", err)
	}
}

func TestTicketsStartSchemaMarksTicketOptional(t *testing.T) {
	output, err := execute(t, t.TempDir(), "schema")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"path":"twt tickets start"`) {
		t.Fatal("schema does not contain twt tickets start")
	}
	if !strings.Contains(output, `"name":"ticket","type":"array[string]","required":false`) {
		t.Fatalf("tickets start schema still requires ticket:\n%s", output)
	}
}

func TestTicketsStartCompletesTicketSlugs(t *testing.T) {
	options, _ := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens")
	candidates := completeArgs(t, options, "tickets", "start", "")
	if len(candidates) != 1 || candidates[0] != "fix-auth-tokens" {
		t.Fatalf("tickets start completion = %q", candidates)
	}
}

func TestNextCompletesTicketSlugs(t *testing.T) {
	options, _ := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens")
	candidates := completeArgs(t, options, "next", "")
	if len(candidates) != 1 || candidates[0] != "fix-auth-tokens" {
		t.Fatalf("next completion = %q", candidates)
	}
}

func TestNextPickerClaimsTheSelectedTicket(t *testing.T) {
	options, home := ticketsStartFixture(t)
	enterCurrentWorkspaceForNext(t, options)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Second thing", "--project", "core")
	executeWithOptions(t, options, nil, "tickets", "close", "second-thing", "--as", "tester")
	var pickedLines []string
	options.TicketPick = func(_ *cobra.Command, lines []string) (int, error) {
		pickedLines = append([]string(nil), lines...)
		return 0, nil
	}
	output := executeWithOptions(t, options, nil, "next", "--as", "tester")
	if len(pickedLines) != 1 {
		t.Fatalf("next picker lines = %v", pickedLines)
	}
	if !strings.HasPrefix(pickedLines[0], "fix-auth-tokens\t") {
		t.Fatalf("next picker line = %q", pickedLines[0])
	}
	if !strings.Contains(output, `Claimed ticket "fix-auth-tokens" as "tester"`) {
		t.Fatalf("next picker output has no claim: %q", output)
	}
	if !strings.Contains(output, `Created Workspace "fix-auth-tokens"`) {
		t.Fatalf("next picker output has no create: %q", output)
	}
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("fix-auth-tokens")
	if err != nil || len(workspace.Tickets) != 1 || workspace.Tickets[0] != "fix-auth-tokens" {
		t.Fatalf("Workspace after next picker: %+v error=%v", workspace, err)
	}
	clientSession := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-clients", "-F", "#{session_name}")
	if clientSession != workspace.TmuxSession {
		t.Fatalf("next picker client session = %q, want %q", clientSession, workspace.TmuxSession)
	}
	content := readTicketFile(t, filepath.Join(home, "core", "fix-auth-tokens.md"))
	if !strings.Contains(content, "tester") {
		t.Fatalf("picked ticket is not claimed:\n%s", content)
	}
}

func TestNextNumberedPickerReadsTheTicketNumber(t *testing.T) {
	options, _ := ticketsStartFixture(t)
	t.Setenv("TMUX_PANE", "")
	executeWithOptions(t, options, nil, "create", "current", "--template", "example", "--no-open")
	current, err := store.NewWorkspaceStore(options.StateDir).Find("current")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TWT_WORKSPACE_ID", current.ID)
	t.Setenv("PATH", "")
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")
	output := executeWithOptions(t, options, strings.NewReader("1\n"), "next", "--as", "tester", "--dry-run")
	if !strings.Contains(output, "tickets.claim: valid") || !strings.Contains(output, "workspaces.next: valid") {
		t.Fatalf("next numbered picker dry-run output = %q", output)
	}
	if _, err := store.NewWorkspaceStore(options.StateDir).Find("fix-auth-tokens"); err == nil {
		t.Fatal("next numbered picker dry-run created a Workspace")
	}
}

func TestNextWithATicketSlugClaimsTheTicket(t *testing.T) {
	options, _ := ticketsStartFixture(t)
	enterCurrentWorkspaceForNext(t, options)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")

	output := executeWithOptions(t, options, nil, "next", "fix-auth-tokens", "--as", "tester")
	if !strings.Contains(output, `Claimed ticket "fix-auth-tokens" as "tester"`) {
		t.Fatalf("next with a Ticket slug has no claim: %q", output)
	}
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("fix-auth-tokens")
	if err != nil || len(workspace.Tickets) != 1 || workspace.Tickets[0] != "fix-auth-tokens" {
		t.Fatalf("Workspace after next with a Ticket slug: %+v error=%v", workspace, err)
	}
}

// The start picker offers the current Project only, like the tickets list
// default. --all-projects widens the picker to every Project.
func TestTicketsStartPickerScopesToTheCurrentProject(t *testing.T) {
	options, _ := ticketsStartFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "projects", "create", "other")
	executeWithOptions(t, options, nil, "tickets", "create", "Core work", "--project", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Other work", "--project", "other")
	t.Setenv("TWT_PROJECT", "core")
	var pickedLines []string
	options.TicketPick = func(_ *cobra.Command, lines []string) (int, error) {
		pickedLines = append([]string(nil), lines...)
		return 0, nil
	}
	options.QuickCreateSwitch = func(_, _ string) error { return nil }

	executeWithOptions(t, options, nil, "tickets", "start", "--as", "tester")
	if len(pickedLines) != 1 || !strings.HasPrefix(pickedLines[0], "core-work\t") {
		t.Fatalf("scoped tickets start picker lines = %v", pickedLines)
	}

	executeWithOptions(t, options, nil, "tickets", "close", "core-work", "--as", "tester")
	pickedLines = nil
	executeWithOptions(t, options, nil, "tickets", "start", "--all-projects", "--as", "tester")
	if len(pickedLines) != 1 || !strings.HasPrefix(pickedLines[0], "other-work\t") {
		t.Fatalf("--all-projects tickets start picker lines = %v", pickedLines)
	}
}

// The no-argument reads follow the current context: projects get shows the
// current Project, and workspaces get shows the current Workspace.
func TestContextDefaultsForProjectsGetAndWorkspacesGet(t *testing.T) {
	options, _ := ticketsStartFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core")
	executeWithOptions(t, options, nil, "tickets", "create", "Some work", "--project", "core")
	options.QuickCreateSwitch = func(_, _ string) error { return nil }
	executeWithOptions(t, options, nil, "tickets", "start", "some-work", "--as", "tester")

	t.Setenv("TWT_PROJECT", "core")
	board := executeWithOptions(t, options, nil, "projects", "get", "--output", "json")
	if !strings.Contains(board, `"name":"core"`) {
		t.Fatalf("projects get without NAME = %s", board)
	}

	workspaceStore := store.NewWorkspaceStore(options.StateDir)
	workspace, err := workspaceStore.Find("some-work")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TWT_WORKSPACE_ID", workspace.ID)
	current := executeWithOptions(t, options, nil, "workspaces", "get", "--output", "json")
	if !strings.Contains(current, `"name":"some-work"`) {
		t.Fatalf("workspaces get without WORKSPACE = %s", current)
	}

	t.Setenv("TWT_PROJECT", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	var stderr bytes.Buffer
	failing := options
	failing.Stdout, failing.Stderr = &bytes.Buffer{}, &stderr
	command := cli.New(failing)
	command.SetArgs(forceTextOutput([]string{"projects", "get"}))
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "no Project is in scope") {
		t.Fatalf("projects get without a scope error = %v", err)
	}
}

func TestTicketsStartUsesTheProjectTemplateOutsideAWorkspace(t *testing.T) {
	options, _ := ticketsStartFixture(t)
	writeNamedTemplate(t, options.ConfigDir, "zeta")
	if err := store.SaveLastTemplate(options.StateDir, "example"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	options.PickTemplate = func(_ *cobra.Command, _ []string) (int, error) {
		t.Fatal("project Template still opened the picker")
		return 0, nil
	}
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "core", "--template", "zeta")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens", "--project", "core")
	_, stderr, err := executeCollectingInput(t, options, nil,
		"tickets", "start", "fix-auth-tokens", "--as", "tester", "--detached", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "Template: zeta (project)") {
		t.Fatalf("tickets start stderr = %q", stderr)
	}
	if strings.Contains(stderr, "last used") || strings.Contains(stderr, "selected") {
		t.Fatalf("tickets start ignored the Project Template: %q", stderr)
	}
}
