package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/prstate"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// TestProjectsShowRendersTheBoard covers the sectioned board: one ticket per
// derived state, a seeded local dispatch session, fetched PR states, and the
// store freshness footer.
func TestProjectsShowRendersTheBoard(t *testing.T) {
	options, _ := ticketTestOptions(t)
	options.PRResolvers = []prstate.Resolver{&fakePRResolver{
		host: "origin.cursor.com",
		states: map[string]prstate.PRState{
			"https://origin.cursor.com/acme/api/pull/9": {State: prstate.StateMerged, Checks: prstate.ChecksPass},
		},
	}}
	run := func(stdin string, args ...string) {
		t.Helper()
		var input *strings.Reader
		if stdin != "" {
			input = strings.NewReader(stdin)
		}
		if out, errOut, err := executeCollectingInput(t, options, input, args...); err != nil {
			t.Fatalf("%v: %v\n%s%s", args, err, out, errOut)
		}
	}
	run("", "tickets", "init")
	run("", "projects", "create", "core")
	run("", "tickets", "create", "Waiting task", "--slug", "waiting-task", "--project", "core", "--status", "ready-for-agent")
	run("", "tickets", "claim", "waiting-task", "--as", "agent-a")
	run("Which schema version?", "tickets", "ask", "waiting-task", "--as", "agent-a", "-")
	run("", "tickets", "create", "Progress task", "--slug", "progress-task", "--project", "core", "--status", "ready-for-agent")
	run("", "tickets", "claim", "progress-task", "--as", "agent-b")
	run("", "tickets", "create", "Review task", "--slug", "review-task", "--project", "core", "--status", "ready-for-human")
	run("", "tickets", "pr", "add", "review-task", "--pr", "https://origin.cursor.com/acme/api/pull/9")
	run("", "tickets", "create", "Ready task", "--slug", "ready-task", "--project", "core", "--status", "ready-for-agent")
	run("", "tickets", "create", "Blocked task", "--slug", "blocked-task", "--project", "core",
		"--status", "ready-for-agent", "--blocked-by", "ready-task")
	run("", "tickets", "create", "Done task", "--slug", "done-task", "--project", "core", "--status", "done")

	now := time.Now().UTC()
	if err := (store.NewLocalDispatchSessionStore(options.StateDir)).Save(domain.LocalDispatchSession{
		Version: domain.LocalDispatchSessionVersion, ID: "ld-board-test-01",
		TicketSlug: "progress-task", Project: "core", TemplateName: "demo",
		Mode: domain.DispatchModeAgent, Provider: "cursor",
		Status: domain.LocalDispatchRunning, Claimant: "agent-b",
		PromptSnapshot: "work", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	stamp := now.Add(-3*time.Minute).Format(time.RFC3339) + "\n"
	if err := os.WriteFile(filepath.Join(options.StateDir, "tickets-sync-reconciled"), []byte(stamp), 0o644); err != nil {
		t.Fatal(err)
	}

	board, _, err := executeCollectingInput(t, options, nil, "projects", "get", "core")
	if err != nil {
		t.Fatalf("projects show: %v\n%s", err, board)
	}
	for _, want := range []string{
		"WAITING ON YOU (1)",
		"waiting-task  @agent-a  <- answer: twt tickets answer waiting-task -",
		"IN PROGRESS (1)",
		"progress-task  @agent-b  session running (local)",
		"IN REVIEW (1)",
		"review-task  ready-for-human  [PR: merged ✓]  <- all PRs merged; close it",
		"READY (1)",
		"ready-task  p2  Ready task",
		"BLOCKED (1)",
		"blocked-task  p2  Blocked task",
		"DONE (last 5) (1)",
		"done-task  p2  Done task",
		"Store as of 3m ago.",
		"run 'twt tickets sync --project core' to refresh",
	} {
		if !strings.Contains(board, want) {
			t.Fatalf("board lacks %q:\n%s", want, board)
		}
	}

	boardJSON, _, err := executeCollectingInput(t, options, nil,
		"projects", "get", "core", "--no-fetch", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"waitingOnYou"`, `"inProgress"`, `"inReview"`, `"blocked"`, `"done"`,
		`"sessions"`, `"prStates"`, `"storeAsOf"`,
		`"ticket":"progress-task"`, `"backend":"local"`,
	} {
		if !strings.Contains(boardJSON, want) {
			t.Fatalf("board JSON lacks %s:\n%s", want, boardJSON)
		}
	}
}
