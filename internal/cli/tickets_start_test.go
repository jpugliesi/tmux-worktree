package cli_test

import (
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
	showTicket := executeWithOptions(t, options, nil, "tickets", "show", "fix-auth-tokens", "--output", "json")
	if !strings.Contains(showTicket, `"workspaceId":"`+workspace.ID+`"`) {
		t.Fatalf("tickets show JSON has no workspaceId: %s", showTicket)
	}

	// workspaces show reports the link in text and JSON.
	show := executeWithOptions(t, options, nil, "workspaces", "show", "fix-auth-tokens")
	if !strings.Contains(show, "Ticket") || !strings.Contains(show, "fix-auth-tokens") {
		t.Fatalf("workspaces show has no Ticket line: %q", show)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "workspaces", "show", "fix-auth-tokens", "--output", "json")
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

	output := executeWithOptions(t, options, nil, "projects", "show", "core", "--output", "json")
	if !strings.Contains(output, `"slug":"ready-work"`) {
		t.Fatalf("ready Ticket missing from Project board: %s", output)
	}
	if !strings.Contains(output, `"slug":"started-work"`) || !strings.Contains(output, `"inFlight"`) {
		t.Fatalf("in-flight Ticket missing from Project board: %s", output)
	}
	if !strings.Contains(output, `"name":"started-work"`) {
		t.Fatalf("Workspace missing from Project board: %s", output)
	}

	claimed := executeWithOptions(t, options, nil, "tickets", "list", "--claimed", "--output", "json")
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
	var archived []string
	options.QuickCreateSwitch = func(_, _ string) error { return nil }
	options.QuickCreateArchive = func(_, oldID, _ string) error {
		archived = append(archived, oldID)
		return nil
	}

	output := executeWithOptions(t, options, nil, "tickets", "start", "fix-auth-tokens", "--as", "tester")
	if strings.Contains(output, "archiving Workspace") {
		t.Fatalf("tickets start archived the current Workspace:\n%s", output)
	}
	if !strings.Contains(output, `Workspace "current" stays active`) {
		t.Fatalf("tickets start output = %q", output)
	}
	if len(archived) != 0 {
		t.Fatalf("tickets start archived %v", archived)
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
	if _, _, err := executeCollectingInput(t, options, strings.NewReader(request), "apply", "--stdin"); err != nil {
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
	options.QuickCreateSwitch = func(_, _ string) error { return nil }
	options.QuickCreateArchive = func(_, _, _ string) error { return nil }

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
	if !strings.Contains(err.Error(), "interactive text output") {
		t.Fatalf("tickets start without a terminal = %v", err)
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
	var switched []string
	options.QuickCreateSwitch = func(_ string, session string) error {
		switched = append(switched, session)
		return nil
	}
	options.QuickCreateArchive = func(_, _, _ string) error { return nil }

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
	if len(switched) != 1 || switched[0] != workspace.TmuxSession {
		t.Fatalf("next picker switch events = %v", switched)
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
	options.QuickCreateSwitch = func(_ string, _ string) error { return nil }
	options.QuickCreateArchive = func(_, _, _ string) error { return nil }

	output := executeWithOptions(t, options, nil, "next", "fix-auth-tokens", "--as", "tester")
	if !strings.Contains(output, `Claimed ticket "fix-auth-tokens" as "tester"`) {
		t.Fatalf("next with a Ticket slug has no claim: %q", output)
	}
	workspace, err := store.NewWorkspaceStore(options.StateDir).Find("fix-auth-tokens")
	if err != nil || len(workspace.Tickets) != 1 || workspace.Tickets[0] != "fix-auth-tokens" {
		t.Fatalf("Workspace after next with a Ticket slug: %+v error=%v", workspace, err)
	}
}
