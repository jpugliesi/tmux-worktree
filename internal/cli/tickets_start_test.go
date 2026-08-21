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

func TestTicketsStartClaimsCreatesLinksAndComments(t *testing.T) {
	options, home := ticketsStartFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_PROJECT_ID", "")
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens")
	var switched []string
	options.QuickCreateSwitch = func(_ string, session string) error {
		switched = append(switched, session)
		return nil
	}

	output := executeWithOptions(t, options, nil, "tickets", "start", "fix-auth-tokens", "--as", "tester")
	if !strings.Contains(output, `Claimed ticket "fix-auth-tokens" as "tester"`) {
		t.Fatalf("tickets start output has no claim: %q", output)
	}
	if !strings.Contains(output, `Created Project "fix-auth-tokens"`) {
		t.Fatalf("tickets start output has no create: %q", output)
	}
	project, err := store.NewProjectStore(options.StateDir).Find("fix-auth-tokens")
	if err != nil || project.Status != domain.ProjectActive {
		t.Fatalf("Project after tickets start: %+v error=%v", project, err)
	}
	if project.Ticket != "fix-auth-tokens" {
		t.Fatalf("Project ticket = %q, want %q", project.Ticket, "fix-auth-tokens")
	}
	if len(switched) != 1 || switched[0] != project.TmuxSession {
		t.Fatalf("tickets start switch events = %v", switched)
	}
	content := readTicketFile(t, filepath.Join(home, "fix-auth-tokens.md"))
	if !strings.Contains(content, "tester") {
		t.Fatalf("ticket is not claimed:\n%s", content)
	}
	if !strings.Contains(content, "Started Project fix-auth-tokens.") {
		t.Fatalf("ticket has no start comment:\n%s", content)
	}

	// projects show reports the link in text and JSON.
	show := executeWithOptions(t, options, nil, "projects", "show", "fix-auth-tokens")
	if !strings.Contains(show, "Ticket: fix-auth-tokens") {
		t.Fatalf("projects show has no Ticket line: %q", show)
	}
	showJSON, _, err := executeCollectingInput(t, options, nil, "projects", "show", "fix-auth-tokens", "--output", "json")
	if err != nil || !strings.Contains(showJSON, `"ticket":"fix-auth-tokens"`) {
		t.Fatalf("projects show JSON has no ticket field: %q error=%v", showJSON, err)
	}

	// --name overrides the Project name; the link keeps the Ticket slug.
	executeWithOptions(t, options, nil, "tickets", "create", "Second thing")
	named := executeWithOptions(t, options, nil, "tickets", "start", "second-thing", "--as", "tester", "--name", "custom-app")
	if !strings.Contains(named, `Created Project "custom-app"`) {
		t.Fatalf("tickets start --name output = %q", named)
	}
	namedProject, err := store.NewProjectStore(options.StateDir).Find("custom-app")
	if err != nil || namedProject.Ticket != "second-thing" {
		t.Fatalf("Project after tickets start --name: %+v error=%v", namedProject, err)
	}
	namedContent := readTicketFile(t, filepath.Join(home, "second-thing.md"))
	if !strings.Contains(namedContent, "Started Project custom-app.") {
		t.Fatalf("ticket has no start comment for --name:\n%s", namedContent)
	}
}

func TestTicketsStartRefusesClosedLockedAndJSON(t *testing.T) {
	options, home := ticketTestOptions(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_PROJECT_ID", "")
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens")

	// A Ticket that a different claimant holds aborts before any Project
	// work.
	executeWithOptions(t, options, nil, "tickets", "claim", "fix-auth-tokens", "--as", "other")
	_, _, err := executeCollectingInput(t, options, nil, "tickets", "start", "fix-auth-tokens", "--as", "tester")
	if clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("tickets start on a claimed Ticket = %v (code %q)", err, clierr.CodeOf(err))
	}
	projects, listErr := store.NewProjectStore(options.StateDir).List()
	if listErr != nil || len(projects) != 0 {
		t.Fatalf("tickets start on a claimed Ticket made Projects: %+v error=%v", projects, listErr)
	}
	if !strings.Contains(readTicketFile(t, filepath.Join(home, "fix-auth-tokens.md")), "other") {
		t.Fatal("the locked start changed the claim")
	}

	// A closed Ticket is refused.
	executeWithOptions(t, options, nil, "tickets", "close", "fix-auth-tokens", "--as", "other")
	_, _, err = executeCollectingInput(t, options, nil, "tickets", "start", "fix-auth-tokens", "--as", "tester")
	if clierr.CodeOf(err) != clierr.PreconditionFailed || !strings.Contains(err.Error(), "the Ticket is closed") {
		t.Fatalf("tickets start on a closed Ticket = %v (code %q)", err, clierr.CodeOf(err))
	}

	// JSON output is refused before any work.
	_, _, err = executeCollectingInput(t, options, nil, "tickets", "start", "fix-auth-tokens", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), "interactive text output") {
		t.Fatalf("tickets start with JSON output = %v", err)
	}
}

func TestTicketsStartKeepsTheClaimWhenTheCreateFails(t *testing.T) {
	options, home := ticketTestOptions(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_PROJECT_ID", "")
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
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth tokens")

	_, _, err := executeCollectingInput(t, options, nil, "tickets", "start", "fix-auth-tokens", "--as", "tester")
	if err == nil || !strings.Contains(err.Error(), "no repositories") {
		t.Fatalf("tickets start with a broken template = %v", err)
	}
	content := readTicketFile(t, filepath.Join(home, "fix-auth-tokens.md"))
	if !strings.Contains(content, "tester") {
		t.Fatalf("the failed create released the claim:\n%s", content)
	}
	projects, listErr := store.NewProjectStore(options.StateDir).List()
	if listErr != nil || len(projects) != 0 {
		t.Fatalf("the failed create made Projects: %+v error=%v", projects, listErr)
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
