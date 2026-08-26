package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

func TestProjectsCloseConfirmsOpenTickets(t *testing.T) {
	options, home := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "change-monitor")
	executeWithOptions(t, options, nil, "tickets", "create", "Open work", "--project", "change-monitor")

	_, _, err := executeCollectingInput(t, options, nil, "projects", "close", "change-monitor")
	if clierr.CodeOf(err) != clierr.PreconditionFailed || !strings.Contains(clierr.HintOf(err), "--force") {
		t.Fatalf("non-interactive projects close = %v, hint %q", err, clierr.HintOf(err))
	}

	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("n\n"),
		"projects", "close", "change-monitor")
	if err != nil {
		t.Fatalf("decline close: %v", err)
	}
	if !strings.Contains(stderr, "Set them to wontfix") || !strings.Contains(stdout, "Kept Project") {
		t.Fatalf("decline output = stdout %q, stderr %q", stdout, stderr)
	}
	if _, statErr := os.Stat(filepath.Join(home, "change-monitor", "open-work.md")); statErr != nil {
		t.Fatalf("declined close moved the Ticket: %v", statErr)
	}

	stdout, stderr, err = executeCollectingInput(t, options, strings.NewReader("y\n"),
		"projects", "close", "change-monitor")
	if err != nil {
		t.Fatalf("confirm close: %v", err)
	}
	if !strings.Contains(stderr, "Set them to wontfix") ||
		!strings.Contains(stdout, "Closed Project \"change-monitor\" and set 1 open Ticket to wontfix") {
		t.Fatalf("confirm output = stdout %q, stderr %q", stdout, stderr)
	}
	closed := filepath.Join(home, "closed", "change-monitor", "open-work.md")
	if content := readTicketFile(t, closed); !strings.Contains(content, "status: wontfix") {
		t.Fatalf("closed Ticket = %s", content)
	}
	list := executeWithOptions(t, options, nil, "projects", "list", "--output", "json")
	if !strings.Contains(list, `"projects":[]`) {
		t.Fatalf("projects list includes the closed Project: %s", list)
	}
}

func TestProjectsCloseForceDryRunAndApply(t *testing.T) {
	options, home := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "force-close")
	executeWithOptions(t, options, nil, "tickets", "create", "Forced work", "--project", "force-close")

	dry, _, err := executeCollectingInput(t, options, nil,
		"projects", "close", "force-close", "--force", "--dry-run", "--output", "json")
	if err != nil || !strings.Contains(dry, `"operation":"projects.close"`) || !strings.Contains(dry, `"status":"valid"`) {
		t.Fatalf("projects close dry run = %q, %v", dry, err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "force-close", "forced-work.md")); statErr != nil {
		t.Fatalf("dry run moved the Ticket: %v", statErr)
	}

	_, _, err = executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"projects.close","project":{"name":"force-close"}}`),
		"apply", "--stdin", "--output", "json")
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("apply projects.close without force = %v", err)
	}
	applied, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"projects.close","project":{"name":"force-close","force":true}}`),
		"apply", "--stdin", "--output", "json")
	if err != nil || !strings.Contains(applied, `"operation":"projects.close"`) || !strings.Contains(applied, `"status":"applied"`) {
		t.Fatalf("apply projects.close = %q, %v", applied, err)
	}
}

func TestProjectsCloseWithoutOpenTicketsNeedsNoForce(t *testing.T) {
	options, _ := ticketTestOptions(t)
	executeWithOptions(t, options, nil, "tickets", "init")
	executeWithOptions(t, options, nil, "projects", "create", "empty")

	output, _, err := executeCollectingInput(t, options, nil,
		"projects", "close", "empty", "--output", "json")
	if err != nil || !strings.Contains(output, `"operation":"projects.close"`) || !strings.Contains(output, `"status":"applied"`) {
		t.Fatalf("close empty Project = %q, %v", output, err)
	}
}
