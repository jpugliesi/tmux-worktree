package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// doneTicketFixture builds the done fixture with a temporary Tickets home and
// one open Ticket "fix-auth".
func doneTicketFixture(t *testing.T) (cli.Options, string) {
	t.Helper()
	options := doneFixture(t)
	home := filepath.Join(t.TempDir(), "tickets")
	options.TicketsHome = home
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "tickets", "create", "Fix auth")
	return options, home
}

// createLinkedWorkspace creates one Workspace and links it to the Ticket slug.
func createLinkedWorkspace(t *testing.T, options cli.Options, name, slug string) domain.Workspace {
	t.Helper()
	executeWithOptions(t, options, nil, "workspaces", "create", name, "--template", "example", "--no-open")
	workspaces := store.NewWorkspaceStore(options.StateDir)
	workspace, err := workspaces.Find(name)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Tickets = []string{slug}
	if err := workspaces.Save(workspace); err != nil {
		t.Fatal(err)
	}
	return workspace
}

// ticketStatusDone reports whether the Ticket file carries the done status.
func ticketStatusDone(t *testing.T, home string) bool {
	t.Helper()
	for _, path := range []string{
		filepath.Join(home, "fix-auth.md"),
		filepath.Join(home, "closed", "fix-auth.md"),
	} {
		content, err := os.ReadFile(path)
		if err == nil {
			return strings.Contains(string(content), "status: done")
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	return false
}

func TestDoneAsksToCloseTheLinkedTicket(t *testing.T) {
	options, home := doneTicketFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	prompt := `Close Ticket "fix-auth"? [y/N] `
	hint := "Run 'twt tickets close fix-auth' when the work is complete."

	// Dry run never prompts and changes nothing.
	workspace := createLinkedWorkspace(t, options, "dry-linked", "fix-auth")
	dryOut, dryErr, err := executeCollectingInput(t, options, strings.NewReader("y\n"), "done", "dry-linked", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dryErr, "Close Ticket") || strings.Contains(dryOut, "Close Ticket") {
		t.Fatalf("done --dry-run prompted: %q %q", dryOut, dryErr)
	}
	if ticketStatusDone(t, home) {
		t.Fatal("done --dry-run closed the Ticket")
	}

	// The answer "n" keeps the Ticket open and prints the close hint.
	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("n\n"), "done", "dry-linked")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, prompt) {
		t.Fatalf("done did not prompt: %q", stderr)
	}
	if !strings.Contains(stdout, hint) {
		t.Fatalf("done without a close has no hint: %q", stdout)
	}
	if ticketStatusDone(t, home) {
		t.Fatal("the answer n closed the Ticket")
	}
	released, err := store.NewWorkspaceStore(options.StateDir).Find(workspace.ID)
	if err != nil || released.Status != domain.WorkspaceArchived || released.Materialized {
		t.Fatalf("Workspace after done: %+v, error = %v", released, err)
	}

	// The answer "y" closes the Ticket after the removal.
	createLinkedWorkspace(t, options, "yes-linked", "fix-auth")
	stdout, stderr, err = executeCollectingInput(t, options, strings.NewReader("y\n"), "done", "yes-linked")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, prompt) {
		t.Fatalf("done did not prompt: %q", stderr)
	}
	if !strings.Contains(stdout, `Closed Ticket "fix-auth"`) {
		t.Fatalf("done did not report the close: %q", stdout)
	}
	if !ticketStatusDone(t, home) {
		t.Fatal("the answer y did not close the Ticket")
	}
}

func TestDoneWithoutATerminalSkipsThePromptAndHints(t *testing.T) {
	options, home := doneTicketFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	createLinkedWorkspace(t, options, "quiet-linked", "fix-auth")

	// A nil stdin keeps os.Stdin, which is not a terminal under go test.
	stdout, stderr, err := executeCollectingInput(t, options, nil, "done", "quiet-linked")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "Close Ticket") || strings.Contains(stdout, "Close Ticket") {
		t.Fatalf("non-interactive done prompted: %q %q", stdout, stderr)
	}
	if !strings.Contains(stdout, "Run 'twt tickets close fix-auth' when the work is complete.") {
		t.Fatalf("non-interactive done has no hint: %q", stdout)
	}
	if ticketStatusDone(t, home) {
		t.Fatal("non-interactive done closed the Ticket")
	}
}

func TestDoneWithManyTicketsDoesNotCloseThem(t *testing.T) {
	options, home := doneTicketFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "tickets", "create", "Add auth tests")
	workspace := createLinkedWorkspace(t, options, "many-linked", "fix-auth")
	workspace.Tickets = []string{"fix-auth", "add-auth-tests"}
	if err := store.NewWorkspaceStore(options.StateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("y\n"), "done", "many-linked")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "Close Ticket") {
		t.Fatalf("done prompted for many Tickets: %q", stderr)
	}
	for _, slug := range workspace.Tickets {
		if !strings.Contains(stdout, "twt tickets close "+slug) {
			t.Fatalf("done has no close command for %q: %q", slug, stdout)
		}
		if strings.Contains(readTicketFile(t, filepath.Join(home, slug+".md")), "status: done") {
			t.Fatalf("done closed Ticket %q", slug)
		}
	}
}

func TestDoneRejectsTheRemovedKeepFlag(t *testing.T) {
	options, home := doneTicketFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	workspace := createLinkedWorkspace(t, options, "keep-linked", "fix-auth")

	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("y\n"), "done", "keep-linked", "--keep")
	if err == nil || !strings.Contains(err.Error(), "unknown flag: --keep") {
		t.Fatalf("done --keep error = %v", err)
	}
	if strings.Contains(stderr, "Close Ticket") || strings.Contains(stdout, "Close Ticket") {
		t.Fatalf("rejected done --keep prompted: %q %q", stdout, stderr)
	}
	if ticketStatusDone(t, home) {
		t.Fatal("done --keep closed the Ticket")
	}
	unchanged, err := store.NewWorkspaceStore(options.StateDir).Find(workspace.ID)
	if err != nil || unchanged.Status != domain.WorkspaceActive {
		t.Fatalf("Workspace after rejected done --keep: status=%q error=%v", unchanged.Status, err)
	}
}

func TestDoneWarnsWhenTheTicketCloseIsLocked(t *testing.T) {
	options, home := doneTicketFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "tickets", "claim", "fix-auth", "--as", "someone-else")
	workspace := createLinkedWorkspace(t, options, "locked-linked", "fix-auth")

	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("y\n"), "done", "locked-linked")
	if err != nil {
		t.Fatalf("done failed on a locked Ticket close: %v", err)
	}
	if !strings.Contains(stderr, `Close Ticket "fix-auth"? [y/N] `) {
		t.Fatalf("done did not prompt: %q", stderr)
	}
	if !strings.Contains(stderr, "could not close Ticket") || !strings.Contains(stderr, "twt tickets close fix-auth") {
		t.Fatalf("done has no close warning: %q", stderr)
	}
	if ticketStatusDone(t, home) {
		t.Fatal("done closed a Ticket that a different claimant holds")
	}
	if !strings.Contains(stdout, "Finished Workspace \"locked-linked\"") {
		t.Fatalf("done did not finish the Workspace: %q", stdout)
	}
	released, err := store.NewWorkspaceStore(options.StateDir).Find(workspace.ID)
	if err != nil || released.Status != domain.WorkspaceArchived || released.Materialized {
		t.Fatalf("Workspace after done: %+v, error = %v", released, err)
	}
}

func TestDoneFromItsSessionClosesTheConfirmedTicket(t *testing.T) {
	options, home := doneTicketFixture(t)
	t.Setenv("TWT_WORKSPACE_ID", "")
	createLinkedWorkspace(t, options, "worker-src", "fix-auth")
	executeWithOptions(t, options, nil, "workspaces", "create", "worker-dest", "--template", "example", "--no-open")
	source, err := store.NewWorkspaceStore(options.StateDir).Find("worker-src")
	if err != nil {
		t.Fatal(err)
	}
	sourcePane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-panes", "-t", "="+source.TmuxSession, "-F", "#{pane_id}")
	attachControlClient(t, options.TmuxSocket, source.TmuxSession)
	t.Setenv("TMUX_PANE", sourcePane)
	if _, _, err := executeCollectingInput(t, options, strings.NewReader("y\n"), "done", source.ID); err != nil {
		t.Fatalf("done from source session: %v", err)
	}
	released, err := store.NewWorkspaceStore(options.StateDir).Find(source.ID)
	if err != nil || released.Status != domain.WorkspaceArchived || released.Materialized {
		t.Fatalf("done Workspace = %+v, error = %v", released, err)
	}
	if !ticketStatusDone(t, home) {
		t.Fatal("done did not close the confirmed Ticket")
	}
}
