package cli_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/prstate"
)

type fakePRResolver struct {
	host   string
	states map[string]prstate.PRState
}

func (f *fakePRResolver) Host() string { return f.host }
func (f *fakePRResolver) Fetch(_ context.Context, url string) (prstate.PRState, error) {
	return f.states[url], nil
}

func TestTicketsTreeRendersTheDAGWithPRBadges(t *testing.T) {
	options, _ := ticketTestOptions(t)
	options.PRResolvers = []prstate.Resolver{&fakePRResolver{
		host: "origin.cursor.com",
		states: map[string]prstate.PRState{
			"https://origin.cursor.com/acme/api/pull/7": {State: prstate.StateMerged, Checks: prstate.ChecksPass},
		},
	}}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "core"); err != nil {
		t.Fatal(err)
	}
	create := func(args ...string) {
		t.Helper()
		full := append([]string{"tickets", "create"}, args...)
		if _, _, err := executeCollectingInput(t, options, nil, full...); err != nil {
			t.Fatal(err)
		}
	}
	create("Root task", "--slug", "root-task", "--project", "core", "--status", "ready-for-agent")
	create("Left child", "--slug", "left-child", "--project", "core", "--status", "ready-for-agent", "--blocked-by", "root-task")
	create("Right child", "--slug", "right-child", "--project", "core", "--status", "ready-for-agent", "--blocked-by", "root-task")
	// Diamond: joins both children.
	create("Join task", "--slug", "join-task", "--project", "core", "--status", "ready-for-agent",
		"--blocked-by", "left-child", "--blocked-by", "right-child")
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "pr", "add", "root-task", "--pr", "https://origin.cursor.com/acme/api/pull/7"); err != nil {
		t.Fatal(err)
	}

	tree, _, err := executeCollectingInput(t, options, nil, "tickets", "tree", "--project", "core")
	if err != nil {
		t.Fatalf("tree: %v\n%s", err, tree)
	}
	for _, want := range []string{
		"Project: core",
		"└── root-task  p2  in-review  [PR: merged ✓]",
		"├── left-child  p2  blocked",
		"└── join-task  p2  blocked",
		"…",
	} {
		if !strings.Contains(tree, want) {
			t.Fatalf("tree lacks %q:\n%s", want, tree)
		}
	}
	// The diamond join expands exactly once.
	if strings.Count(tree, "join-task") != 2 {
		t.Fatalf("join-task should appear twice (expansion + ellipsis):\n%s", tree)
	}
	treeJSON, _, err := executeCollectingInput(t, options, nil,
		"tickets", "tree", "--project", "core", "--no-fetch", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(treeJSON, `"prStates"`) || !strings.Contains(treeJSON, `"pullRequests"`) {
		t.Fatalf("tree JSON = %s", treeJSON)
	}
}

func TestTicketsTreeHandlesCycles(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "core"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "A", "--slug", "cycle-a", "--project", "core", "--status", "ready-for-agent", "--blocked-by", "cycle-b"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "B", "--slug", "cycle-b", "--project", "core", "--status", "ready-for-agent", "--blocked-by", "cycle-a"); err != nil {
		t.Fatal(err)
	}
	tree, _, err := executeCollectingInput(t, options, nil, "tickets", "tree", "--project", "core")
	if err != nil {
		t.Fatalf("cyclic tree: %v\n%s", err, tree)
	}
	if !strings.Contains(tree, "Dependency cycle: cycle-a, cycle-b") {
		t.Fatalf("cycle not reported:\n%s", tree)
	}
	if !strings.Contains(tree, "(cycle)") {
		t.Fatalf("cycle members not rendered:\n%s", tree)
	}
}
