package cli_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// createLinkedProject creates one Project and links it to the Ticket slug.
func createLinkedProject(t *testing.T, options cli.Options, name, slug string) domain.Project {
	t.Helper()
	executeWithOptions(t, options, nil, "projects", "create", name, "--template", "example", "--no-open")
	projects := store.NewProjectStore(options.StateDir)
	project, err := projects.Find(name)
	if err != nil {
		t.Fatal(err)
	}
	project.Ticket = slug
	if err := projects.Save(project); err != nil {
		t.Fatal(err)
	}
	return project
}

// ticketStatusDone reports whether the Ticket file carries the done status.
func ticketStatusDone(t *testing.T, home string) bool {
	t.Helper()
	return strings.Contains(readTicketFile(t, filepath.Join(home, "fix-auth.md")), "status: done")
}

func TestDoneAsksToCloseTheLinkedTicket(t *testing.T) {
	options, home := doneTicketFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_PROJECT_ID", "")
	prompt := `Close Ticket "fix-auth"? [y/N] `
	hint := "Run 'twt tickets close fix-auth' when the work is complete."

	// Dry run never prompts and changes nothing.
	project := createLinkedProject(t, options, "dry-linked", "fix-auth")
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
	if _, err := store.NewProjectStore(options.StateDir).Find(project.ID); err == nil {
		t.Fatal("done kept the Project record")
	}

	// The answer "y" closes the Ticket after the removal.
	createLinkedProject(t, options, "yes-linked", "fix-auth")
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
	t.Setenv("TWT_PROJECT_ID", "")
	createLinkedProject(t, options, "quiet-linked", "fix-auth")

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

func TestDoneKeepNeverPromptsForTheTicket(t *testing.T) {
	options, home := doneTicketFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_PROJECT_ID", "")
	project := createLinkedProject(t, options, "keep-linked", "fix-auth")

	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("y\n"), "done", "keep-linked", "--keep")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "Close Ticket") || strings.Contains(stdout, "Close Ticket") {
		t.Fatalf("done --keep prompted: %q %q", stdout, stderr)
	}
	if !strings.Contains(stdout, "Run 'twt tickets close fix-auth' when the work is complete.") {
		t.Fatalf("done --keep has no hint: %q", stdout)
	}
	if ticketStatusDone(t, home) {
		t.Fatal("done --keep closed the Ticket")
	}
	archived, err := store.NewProjectStore(options.StateDir).Find(project.ID)
	if err != nil || archived.Status != domain.ProjectArchived {
		t.Fatalf("Project after done --keep: status=%q error=%v", archived.Status, err)
	}
}

func TestDoneWarnsWhenTheTicketCloseIsLocked(t *testing.T) {
	options, home := doneTicketFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT_PROJECT_ID", "")
	executeWithOptions(t, options, nil, "tickets", "claim", "fix-auth", "--as", "someone-else")
	project := createLinkedProject(t, options, "locked-linked", "fix-auth")

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
	if !strings.Contains(stdout, "Removed Project \"locked-linked\"") {
		t.Fatalf("done did not remove the Project: %q", stdout)
	}
	if _, err := store.NewProjectStore(options.StateDir).Find(project.ID); err == nil {
		t.Fatal("done kept the Project record")
	}
}

func TestDoneWorkerClosesTheConfirmedTicket(t *testing.T) {
	options, home := doneTicketFixture(t)
	t.Setenv("TWT_PROJECT_ID", "")
	createLinkedProject(t, options, "worker-src", "fix-auth")
	executeWithOptions(t, options, nil, "projects", "create", "worker-dest", "--template", "example", "--no-open")
	source, err := store.NewProjectStore(options.StateDir).Find("worker-src")
	if err != nil {
		t.Fatal(err)
	}
	helperPane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "=example-worker-dest", "-n", "done-helper", "--", "sleep", "60")
	t.Setenv("TMUX_PANE", helperPane)

	channel := "twt-done-ticket-worker-test"
	signalResult := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		signalResult <- exec.Command("tmux", "-L", options.TmuxSocket, "wait-for", "-S", channel).Run()
	}()
	if err := cli.RunDoneWorker(options, []string{source.ID, "keep=false", "force=false", "-", "fix-auth", "tester", channel, "no-client"}); err != nil {
		t.Fatalf("run done worker: %v", err)
	}
	if err := <-signalResult; err != nil {
		t.Fatalf("signal done worker: %v", err)
	}
	if _, err := store.NewProjectStore(options.StateDir).Find(source.ID); err == nil {
		t.Fatal("done worker kept the Project record")
	}
	if !ticketStatusDone(t, home) {
		t.Fatal("done worker did not close the confirmed Ticket")
	}
}
