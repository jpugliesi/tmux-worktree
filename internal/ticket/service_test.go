package ticket

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestReadyMatrix(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "done-dep.md"), fixture{title: "Done dep", status: "done"}.content())
	writeFixture(t, filepath.Join(home, "wontfix-dep.md"), fixture{title: "Wontfix dep", status: "wontfix"}.content())
	writeFixture(t, filepath.Join(home, "open-dep.md"), fixture{title: "Open dep", status: "needs-triage"}.content())
	tests := []struct {
		slug  string
		file  fixture
		ready bool
	}{
		{"ready-plain", fixture{title: "Ready plain", status: "ready-for-agent"}, true},
		{"ready-closed-blockers", fixture{title: "Closed blockers", status: "ready-for-agent", blocked: []string{"done-dep", "wontfix-dep"}}, true},
		{"blocked-by-open", fixture{title: "Blocked by open", status: "ready-for-agent", blocked: []string{"open-dep"}}, false},
		{"blocked-by-missing", fixture{title: "Blocked by missing", status: "ready-for-agent", blocked: []string{"no-such-ticket"}}, false},
		{"claimed", fixture{title: "Claimed", status: "ready-for-agent", claimedBy: "agent-a"}, false},
		{"wrong-status", fixture{title: "Wrong status", status: "needs-triage"}, false},
		{"unknown-status", fixture{title: "Unknown status", status: "in-progress"}, false},
	}
	for _, test := range tests {
		writeFixture(t, filepath.Join(home, test.slug+".md"), test.file.content())
	}
	ready, err := service.List(ListFilter{Ready: true})
	if err != nil {
		t.Fatalf("List --ready: %v", err)
	}
	got := map[string]bool{}
	for _, ticket := range ready {
		got[ticket.Slug] = true
	}
	for _, test := range tests {
		if got[test.slug] != test.ready {
			t.Errorf("ready(%s) = %v, want %v", test.slug, got[test.slug], test.ready)
		}
	}
}

func TestShowReportsOpenAndMissingBlockers(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "open-dep.md"), fixture{title: "Open dep", status: "needs-triage"}.content())
	writeFixture(t, filepath.Join(home, "done-dep.md"), fixture{title: "Done dep", status: "done"}.content())
	writeFixture(t, filepath.Join(home, "blocked.md"),
		fixture{title: "Blocked", status: "ready-for-agent", blocked: []string{"open-dep", "done-dep", "gone"}}.content())

	result, err := service.Show("blocked")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if result.Ready {
		t.Fatal("a blocked ticket must not be ready")
	}
	want := []OpenBlocker{{Slug: "open-dep"}, {Slug: "gone", Missing: true}}
	if len(result.BlockedByOpen) != len(want) {
		t.Fatalf("BlockedByOpen = %+v, want %+v", result.BlockedByOpen, want)
	}
	for i := range want {
		if result.BlockedByOpen[i] != want[i] {
			t.Fatalf("BlockedByOpen[%d] = %+v, want %+v", i, result.BlockedByOpen[i], want[i])
		}
	}
	if !strings.Contains(result.Body, "# Blocked") {
		t.Fatalf("Body = %q", result.Body)
	}
}

func TestQueueReturnsTheOpenProjectGraphAndDispatchableTickets(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "core", "index.md"), "# core\n")
	writeFixture(t, filepath.Join(home, "core", "done-dep.md"),
		fixture{title: "Done dep", status: "done", priority: "0"}.content())
	writeFixture(t, filepath.Join(home, "core", "available-z.md"),
		fixture{title: "Available Z", status: "ready-for-agent", priority: "1", blocked: []string{"done-dep"}}.content())
	writeFixture(t, filepath.Join(home, "core", "available-a.md"),
		fixture{title: "Available A", status: "ready-for-agent", priority: "0"}.content())
	writeFixture(t, filepath.Join(home, "core", "blocked.md"),
		fixture{title: "Blocked", status: "ready-for-agent", priority: "0", blocked: []string{"missing-dep"}}.content())
	writeFixture(t, filepath.Join(home, "core", "claimed.md"),
		fixture{title: "Claimed", status: "ready-for-agent", priority: "0", claimedBy: "agent-a"}.content())
	writeFixture(t, filepath.Join(home, "core", "triage.md"),
		fixture{title: "Triage", status: "needs-triage", priority: "0"}.content())

	result, err := service.Queue("core", 0)
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	graph := make([]string, 0, len(result.Graph))
	for _, ticket := range result.Graph {
		graph = append(graph, ticket.Slug)
	}
	if got := strings.Join(graph, ","); got != "available-a,blocked,claimed,triage,available-z" {
		t.Fatalf("graph order = %q", got)
	}
	if result.ReadyTotalCount != 2 || result.ReadyTruncated || len(result.Ready) != 2 ||
		result.Ready[0].Slug != "available-a" || result.Ready[1].Slug != "available-z" {
		t.Fatalf("Ready = %+v, count = %d, truncated = %v", result.Ready, result.ReadyTotalCount, result.ReadyTruncated)
	}
	if result.Graph[1].Ready || len(result.Graph[1].Dependencies) != 1 || result.Graph[1].Dependencies[0].State != QueueDependencyMissing {
		t.Fatalf("blocked graph entry = %+v", result.Graph[1])
	}
	if result.Graph[2].Ready || result.Graph[2].ClaimedBy != "agent-a" {
		t.Fatalf("claimed graph entry = %+v", result.Graph[2])
	}
	for _, ticket := range result.Graph {
		if ticket.Slug == "done-dep" {
			t.Fatal("the open graph includes a closed Ticket")
		}
	}

	limited, err := service.Queue("core", 1)
	if err != nil || len(limited.Graph) != len(result.Graph) || len(limited.Ready) != 1 ||
		limited.ReadyTotalCount != 2 || !limited.ReadyTruncated {
		t.Fatalf("limited Queue = %+v, error = %v", limited, err)
	}
	if _, err := service.Queue("core", -1); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("Queue with negative limit = %v, want invalid_usage", err)
	}
	if _, err := service.Queue("missing", 0); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("Queue(missing) = %v, want not_found", err)
	}
}

func TestQueueReportsCyclesAndCrossProjectDependencies(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "core", "index.md"), "# core\n")
	writeFixture(t, filepath.Join(home, "other", "index.md"), "# other\n")
	writeFixture(t, filepath.Join(home, "core", "cycle-a.md"),
		fixture{title: "Cycle A", status: "ready-for-agent", blocked: []string{"cycle-b", "other-work"}}.content())
	writeFixture(t, filepath.Join(home, "core", "cycle-b.md"),
		fixture{title: "Cycle B", status: "ready-for-agent", blocked: []string{"cycle-a"}}.content())
	writeFixture(t, filepath.Join(home, "other", "other-work.md"),
		fixture{title: "Other work", status: "needs-triage"}.content())

	result, err := service.Queue("core", 0)
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if len(result.Cycles) != 1 || strings.Join(result.Cycles[0], ",") != "cycle-a,cycle-b" {
		t.Fatalf("Cycles = %v", result.Cycles)
	}
	dependency := result.Graph[0].Dependencies[1]
	if dependency.Slug != "other-work" || dependency.State != QueueDependencyOpen ||
		dependency.Project != "other" || dependency.InProject {
		t.Fatalf("cross-Project dependency = %+v", dependency)
	}
}

func TestMutationRenamesTheLegacyBoardFieldToProject(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "core", "legacy.md")
	writeFixture(t, path, `---
title: "Legacy"
status: needs-triage
priority: 2
board: core
custom_key: keep-me
blocked_by: []
---

# Legacy
`)

	ticket, err := service.Resolve("legacy")
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Project != "core" {
		t.Fatalf("legacy Ticket Project = %q", ticket.Project)
	}
	if _, err := service.Comment("legacy", "Migrated.", false); err != nil {
		t.Fatal(err)
	}
	content := readFile(t, path)
	if !strings.Contains(content, "project: core\n") || strings.Contains(content, "board: core\n") {
		t.Fatalf("legacy field was not renamed:\n%s", content)
	}
	if !strings.Contains(content, "custom_key: keep-me\n") {
		t.Fatalf("unknown frontmatter was not preserved:\n%s", content)
	}
}

func TestShowNormalizesBlockedByFormsInMemoryOnly(t *testing.T) {
	service, home := newTestService(t)
	content := strings.Replace(
		fixture{title: "Mixed blockers", status: "ready-for-agent"}.content(),
		"blocked_by: []",
		"blocked_by:\n  - \"[[dep-one]]\"\n  - \"[[dep-two|A dep]]\"\n  - dep-three", 1)
	path := filepath.Join(home, "mixed.md")
	writeFixture(t, path, content)

	ticket, err := service.Resolve("mixed")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Join(ticket.BlockedBy, ",") != "dep-one,dep-two,dep-three" {
		t.Fatalf("BlockedBy = %v", ticket.BlockedBy)
	}
	// A mutation must not rewrite blocked_by on disk.
	if _, err := service.Claim("mixed", "agent-a", false); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	after := readFile(t, path)
	for _, line := range []string{"  - \"[[dep-one]]\"", "  - \"[[dep-two|A dep]]\"", "  - dep-three"} {
		if !strings.Contains(after, line+"\n") {
			t.Fatalf("blocked_by line %q was rewritten:\n%s", line, after)
		}
	}
}

func TestListFiltersAndSort(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "projects", "index.md"), "# hub\n")
	writeFixture(t, filepath.Join(home, "ungrouped.md"), fixture{title: "Ungrouped", status: "needs-triage", priority: "3"}.content())
	writeFixture(t, filepath.Join(home, "projects", "beta.md"), fixture{title: "Beta", status: "needs-triage", priority: "1"}.content())
	writeFixture(t, filepath.Join(home, "projects", "alpha.md"), fixture{title: "Alpha", status: "done", priority: "1"}.content())
	writeFixture(t, filepath.Join(home, "no-priority.md"), fixture{title: "No priority", status: "needs-triage"}.content())

	all, err := service.List(ListFilter{All: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	order := []string{}
	for _, ticket := range all {
		order = append(order, ticket.Slug)
	}
	if strings.Join(order, ",") != "alpha,beta,no-priority,ungrouped" {
		t.Fatalf("sort order = %v", order)
	}

	project, err := service.List(ListFilter{Project: "projects", ProjectSet: true, All: true})
	if err != nil {
		t.Fatalf("List --project: %v", err)
	}
	if len(project) != 2 || project[0].Slug != "alpha" || project[1].Slug != "beta" {
		t.Fatalf("project filter = %+v", project)
	}
	if project[0].Project != "projects" {
		t.Fatalf("Project field = %q", project[0].Project)
	}

	ungrouped, err := service.List(ListFilter{ProjectSet: true})
	if err != nil {
		t.Fatalf("List --project '': %v", err)
	}
	if len(ungrouped) != 2 || ungrouped[0].Slug != "no-priority" || ungrouped[1].Slug != "ungrouped" {
		t.Fatalf("ungrouped filter = %+v", ungrouped)
	}

	done, err := service.List(ListFilter{Status: "done"})
	if err != nil {
		t.Fatalf("List --status: %v", err)
	}
	if len(done) != 1 || done[0].Slug != "alpha" {
		t.Fatalf("status filter = %+v", done)
	}

	if _, err := service.List(ListFilter{Ready: true, Status: "done"}); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("--ready with --status = %v, want invalid_usage", err)
	}

	// A missing priority reads as the default 2.
	for _, ticket := range all {
		if ticket.Slug == "no-priority" && ticket.Priority != 2 {
			t.Fatalf("missing priority read as %d, want 2", ticket.Priority)
		}
	}
}

func TestListHidesClosedTicketsByDefault(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "open.md"), fixture{title: "Open", status: "needs-triage"}.content())
	writeFixture(t, filepath.Join(home, "shipped.md"), fixture{title: "Shipped", status: "done"}.content())
	writeFixture(t, filepath.Join(home, "dropped.md"), fixture{title: "Dropped", status: "wontfix"}.content())
	writeFixture(t, filepath.Join(home, "pickable.md"), fixture{title: "Pickable", status: "ready-for-agent"}.content())

	slugsOf := func(filter ListFilter) string {
		t.Helper()
		tickets, err := service.List(filter)
		if err != nil {
			t.Fatalf("List(%+v): %v", filter, err)
		}
		slugs := make([]string, 0, len(tickets))
		for _, ticket := range tickets {
			slugs = append(slugs, ticket.Slug)
		}
		sort.Strings(slugs)
		return strings.Join(slugs, ",")
	}

	if got := slugsOf(ListFilter{}); got != "open,pickable" {
		t.Fatalf("default list = %q, want only the open tickets", got)
	}
	if got := slugsOf(ListFilter{All: true}); got != "dropped,open,pickable,shipped" {
		t.Fatalf("--all list = %q", got)
	}
	// An explicit status turns the default exclusion off.
	if got := slugsOf(ListFilter{Status: "done"}); got != "shipped" {
		t.Fatalf("--status done list = %q", got)
	}
	if got := slugsOf(ListFilter{Status: "wontfix"}); got != "dropped" {
		t.Fatalf("--status wontfix list = %q", got)
	}
	// --ready is already narrower, so the default exclusion changes nothing.
	if got := slugsOf(ListFilter{Ready: true}); got != "pickable" {
		t.Fatalf("--ready list = %q", got)
	}
	if got := slugsOf(ListFilter{Ready: true, All: true}); got != "pickable" {
		t.Fatalf("--ready --all list = %q", got)
	}
}

func TestCloseResolvesATicket(t *testing.T) {
	service, home := newTestService(t)
	unclaimed := filepath.Join(home, "unclaimed.md")
	writeFixture(t, unclaimed, fixture{title: "Unclaimed", status: "ready-for-agent"}.content())
	mine := filepath.Join(home, "mine.md")
	writeFixture(t, mine, fixture{title: "Mine", status: "ready-for-agent", claimedBy: "agent-a"}.content())
	theirs := filepath.Join(home, "theirs.md")
	writeFixture(t, theirs, fixture{title: "Theirs", status: "ready-for-agent", claimedBy: "agent-b"}.content())

	// An unclaimed Ticket closes.
	closed, err := service.Close("unclaimed", "agent-a", false)
	if err != nil {
		t.Fatalf("Close on an unclaimed ticket: %v", err)
	}
	if closed.Status != domain.TicketDone || closed.ClaimedBy != "" || closed.ClaimedAt != "" {
		t.Fatalf("closed ticket = %+v", closed)
	}

	// The claimant closes its own Ticket.
	if _, err := service.Close("mine", "agent-a", false); err != nil {
		t.Fatalf("Close by the claimant: %v", err)
	}

	// Another claimant is locked out, and the file stays as it was.
	before := readFile(t, theirs)
	_, err = service.Close("theirs", "agent-a", false)
	if clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("Close by another claimant = %v, want locked", err)
	}
	if !strings.Contains(err.Error(), `claimed by "agent-b"`) {
		t.Fatalf("locked message %q does not name the holder", err)
	}
	if readFile(t, theirs) != before {
		t.Fatal("a locked-out close changed the file")
	}
}

func TestCloseMovesTicketToTheClosedProjectTree(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("core", false); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	source := filepath.Join(home, "core", "work.md")
	writeFixture(t, source, fixture{title: "Work", status: "ready-for-agent", claimedBy: "agent-a"}.content())

	closed, err := service.Close("work", "agent-a", false)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	destination := filepath.Join(home, closedDirectoryName, "core", "work.md")
	if closed.Path != destination || closed.Project != "core" || closed.Status != domain.TicketDone {
		t.Fatalf("closed Ticket = %+v", closed)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still exists after Close: %v", err)
	}
	if !strings.Contains(readFile(t, destination), "status: done\n") {
		t.Fatalf("destination has the wrong status:\n%s", readFile(t, destination))
	}
}

func TestSetRelocatesAfterStatusAndProjectChanges(t *testing.T) {
	service, home := newTestService(t)
	for _, project := range []string{"core", "other"} {
		if _, err := service.CreateProject(project, false); err != nil {
			t.Fatalf("CreateProject(%s): %v", project, err)
		}
	}
	writeFixture(t, filepath.Join(home, "core", "work.md"), fixture{title: "Work", status: "ready-for-agent"}.content())

	closed, err := service.Set("work", SetRequest{
		Status: "done", StatusSet: true, Project: "other", ProjectSet: true,
	}, false)
	if err != nil {
		t.Fatalf("Set status and Project: %v", err)
	}
	closedPath := filepath.Join(home, closedDirectoryName, "other", "work.md")
	if closed.Path != closedPath || closed.Project != "other" || closed.Status != domain.TicketDone {
		t.Fatalf("closed Ticket = %+v", closed)
	}

	reopened, err := service.Set("work", SetRequest{Status: "ready-for-agent", StatusSet: true}, false)
	if err != nil {
		t.Fatalf("reopen Ticket: %v", err)
	}
	activePath := filepath.Join(home, "other", "work.md")
	if reopened.Path != activePath || reopened.Project != "other" || reopened.Status != domain.TicketReadyForAgent {
		t.Fatalf("reopened Ticket = %+v", reopened)
	}
	if _, err := os.Stat(closedPath); !os.IsNotExist(err) {
		t.Fatalf("closed path still exists after reopen: %v", err)
	}
}

func TestSetMovesAClosedTicketBetweenProjects(t *testing.T) {
	service, home := newTestService(t)
	for _, project := range []string{"core", "other"} {
		if _, err := service.CreateProject(project, false); err != nil {
			t.Fatalf("CreateProject(%s): %v", project, err)
		}
	}
	if err := ensureClosedRoot(home, false); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(home, closedDirectoryName, "core", "work.md"), fixture{title: "Work", status: "done"}.content())

	moved, err := service.Set("work", SetRequest{Project: "other", ProjectSet: true}, false)
	if err != nil {
		t.Fatalf("Set Project: %v", err)
	}
	want := filepath.Join(home, closedDirectoryName, "other", "work.md")
	if moved.Path != want || moved.Project != "other" || moved.Status != domain.TicketDone {
		t.Fatalf("moved closed Ticket = %+v", moved)
	}
}

func TestCloseDoesNotChangeEitherDuplicateDestination(t *testing.T) {
	service, home := newTestService(t)
	if err := ensureClosedRoot(home, false); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, "work.md")
	destination := filepath.Join(home, closedDirectoryName, "work.md")
	writeFixture(t, source, fixture{title: "Active", status: "ready-for-agent"}.content())
	writeFixture(t, destination, fixture{title: "Existing", status: "done"}.content())
	sourceBefore, destinationBefore := readFile(t, source), readFile(t, destination)

	if _, err := service.Close("work", "agent-a", false); clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("Close with a duplicate destination = %v, want unsafe_state", err)
	}
	if readFile(t, source) != sourceBefore || readFile(t, destination) != destinationBefore {
		t.Fatal("Close changed a file in a destination collision")
	}
}

func TestCloseOverwritesAnUnknownStatus(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "odd.md")
	writeFixture(t, path, fixture{title: "Odd", status: "in-progress"}.content())

	closed, err := service.Close("odd", "agent-a", false)
	if err != nil {
		t.Fatalf("Close on an unknown status: %v", err)
	}
	if closed.Status != domain.TicketDone {
		t.Fatalf("status = %q, want done", closed.Status)
	}
	if !strings.Contains(readFile(t, closed.Path), "status: done\n") {
		t.Fatalf("status not written:\n%s", readFile(t, closed.Path))
	}
}

func TestCloseIsOneWriteThatKeepsLegacyKeys(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "change-monitor", "tkt-cm-001.md")
	claimed := strings.Replace(legacyFixture, "claimed_by:\nclaimed_at:\n",
		"claimed_by: agent-a\nclaimed_at: 2026-08-02\n", 1)
	writeFixture(t, path, claimed)

	closed, err := service.Close("tkt-cm-001", "agent-a", false)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	// One read after the single write: every change of the close mutation
	// must be in this one file content.
	content := readFile(t, closed.Path)
	for _, line := range []string{
		"status: done\n",
		"claimed_by:\n",
		"claimed_at:\n",
		"updated: 2026-08-20\n",
		"project: change-monitor\n",
		// The legacy frontmatter survives the write.
		"id: tkt-cm-001\n",
		"type: task\n",
		"category: enhancement\n",
		"workspace: \"[[Change Monitor Agent]]\"\n",
		"parent:\n",
		"title: \"Reconnect Change Monitor VFS tools\"\n",
		"blocked_by: []\n",
	} {
		if !strings.Contains(content, line) {
			t.Fatalf("close result misses %q:\n%s", line, content)
		}
	}
	if strings.Contains(content, "status: ready-for-agent") || strings.Contains(content, "agent-a") {
		t.Fatalf("close left stale values:\n%s", content)
	}
	if !strings.HasSuffix(content, "## Comments\n") {
		t.Fatalf("close changed the body:\n%s", content)
	}
}

func TestCloseDryRunWritesNothing(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "work.md")
	writeFixture(t, path, fixture{title: "Work", status: "ready-for-agent", claimedBy: "agent-a"}.content())
	before := readFile(t, path)

	closed, err := service.Close("work", "agent-a", true)
	if err != nil {
		t.Fatalf("dry-run Close: %v", err)
	}
	if closed.Status != domain.TicketDone || closed.ClaimedBy != "" {
		t.Fatalf("dry-run result = %+v", closed)
	}
	if readFile(t, path) != before {
		t.Fatal("a dry-run close changed the file")
	}
	if _, err := os.Stat(filepath.Join(home, closedDirectoryName)); !os.IsNotExist(err) {
		t.Fatal("a dry-run close created the closed directory")
	}
	// A dry run still runs every check.
	if _, err := service.Close("work", "agent-b", true); clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("dry-run Close by another claimant = %v, want locked", err)
	}
	if _, err := service.Close("missing", "agent-a", true); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("dry-run Close on a missing ticket = %v, want not_found", err)
	}
	if _, err := service.Close("work", "", true); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("dry-run Close without a claimant = %v, want invalid_usage", err)
	}
}

func TestClaimCompareAndSet(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "work.md")
	writeFixture(t, path, fixture{title: "Work", status: "ready-for-agent"}.content())

	claimed, err := service.Claim("work", "agent-a", false)
	if err != nil {
		t.Fatalf("first Claim: %v", err)
	}
	if claimed.ClaimedBy != "agent-a" || claimed.ClaimedAt != "2026-08-20" {
		t.Fatalf("claimed ticket = %+v", claimed)
	}
	content := readFile(t, path)
	if !strings.Contains(content, "claimed_by: agent-a\n") || !strings.Contains(content, "claimed_at: 2026-08-20\n") {
		t.Fatalf("claim not written:\n%s", content)
	}
	if !strings.Contains(content, "updated: 2026-08-20\n") {
		t.Fatalf("updated not bumped:\n%s", content)
	}

	// Same claimant: success with no write at all.
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	bytesBefore := readFile(t, path)
	if _, err := service.Claim("work", "agent-a", false); err != nil {
		t.Fatalf("same-claimant Claim: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, path) != bytesBefore {
		t.Fatal("same-claimant claim rewrote the file")
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("same-claimant claim touched the file")
	}

	// Different claimant: locked, and the file stays untouched.
	_, err = service.Claim("work", "agent-b", false)
	if clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("cross-claimant Claim = %v, want locked", err)
	}
	if !strings.Contains(err.Error(), `claimed by "agent-a"`) {
		t.Fatalf("locked message %q does not name the claimant", err)
	}
	if !strings.Contains(clierr.HintOf(err), "twt tickets list --ready") {
		t.Fatalf("locked hint %q does not point at the ready list", clierr.HintOf(err))
	}
	if readFile(t, path) != bytesBefore {
		t.Fatal("failed claim changed the file")
	}
}

func TestSetWorkspaceStampsAndClears(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "work.md")
	writeFixture(t, path, fixture{title: "Work", status: "ready-for-agent"}.content())

	stamped, err := service.SetWorkspace("work", "514a26ed287e429b888000aaa288333a", false)
	if err != nil {
		t.Fatalf("SetWorkspace: %v", err)
	}
	if stamped.WorkspaceID != "514a26ed287e429b888000aaa288333a" {
		t.Fatalf("stamped WorkspaceID = %q", stamped.WorkspaceID)
	}
	content := readFile(t, path)
	if !strings.Contains(content, "twt_workspace_id: 514a26ed287e429b888000aaa288333a\n") {
		t.Fatalf("stamp not written:\n%s", content)
	}

	if _, err := service.Claim("work", "agent-a", false); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	closed, err := service.Close("work", "agent-a", false)
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closed.WorkspaceID != "" || closed.ClaimedBy != "" {
		t.Fatalf("closed ticket still linked: %+v", closed)
	}
	closedPath := filepath.Join(home, "closed", "work.md")
	if strings.Contains(readFile(t, closedPath), "twt_workspace_id: 514a26ed287e429b888000aaa288333a") {
		t.Fatalf("close kept the Workspace stamp:\n%s", readFile(t, closedPath))
	}
}

func TestListClaimedOmitsUnclaimedTickets(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "open.md"), fixture{title: "Open", status: "ready-for-agent"}.content())
	writeFixture(t, filepath.Join(home, "held.md"), fixture{title: "Held", status: "ready-for-agent", claimedBy: "agent-a"}.content())

	claimed, err := service.List(ListFilter{Claimed: true})
	if err != nil {
		t.Fatalf("List claimed: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Slug != "held" {
		t.Fatalf("claimed list = %+v", claimed)
	}
	if _, err := service.List(ListFilter{Ready: true, Claimed: true}); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("ready+claimed = %v, want invalid_usage", err)
	}
}

func TestMutationRespectsTheTicketLock(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "work.md")
	writeFixture(t, path, fixture{title: "Work", status: "ready-for-agent"}.content())
	before := readFile(t, path)

	lock, err := store.AcquireNamedLock(service.options.StateDir, "ticket", "work")
	if err != nil {
		t.Fatalf("AcquireNamedLock: %v", err)
	}
	defer lock.Release()

	if _, err := service.Claim("work", "agent-a", false); clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("Claim under a held lock = %v, want locked", err)
	}
	if readFile(t, path) != before {
		t.Fatal("a locked-out claim changed the file")
	}
}

func TestClaimRefusesUnknownStatus(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "odd.md"), fixture{title: "Odd", status: "in-progress"}.content())
	_, err := service.Claim("odd", "agent-a", false)
	if clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("Claim on unknown status = %v, want unsafe_state", err)
	}
	if !strings.Contains(err.Error(), `unrecognized status "in-progress"`) {
		t.Fatalf("message %q does not name the status", err)
	}
	for _, status := range domain.TicketStatuses() {
		if !strings.Contains(clierr.HintOf(err), status) {
			t.Fatalf("hint %q does not list %q", clierr.HintOf(err), status)
		}
	}
}

func TestClaimValidatesClaimant(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "work.md"), fixture{title: "Work", status: "ready-for-agent"}.content())
	for _, claimant := range []string{"", "  ", "a/b", "../up", "a%20b"} {
		if _, err := service.Claim("work", claimant, false); clierr.CodeOf(err) != clierr.InvalidUsage {
			t.Fatalf("Claim with claimant %q = %v, want invalid_usage", claimant, err)
		}
	}
}

func TestClaimReadyRequiresAReadyTicket(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "ready.md"), fixture{title: "Ready", status: "ready-for-agent"}.content())
	writeFixture(t, filepath.Join(home, "open-dep.md"), fixture{title: "Open dep", status: "needs-triage"}.content())
	writeFixture(t, filepath.Join(home, "blocked.md"), fixture{title: "Blocked", status: "ready-for-agent", blocked: []string{"open-dep"}}.content())

	reserved, err := service.ClaimReady("ready", "worker-abcd-1234", false)
	if err != nil {
		t.Fatalf("ClaimReady: %v", err)
	}
	if reserved.ClaimedBy != "worker-abcd-1234" {
		t.Fatalf("reserved Ticket = %+v", reserved)
	}
	if _, err := service.ClaimReady("blocked", "worker-abcd-5678", false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("blocked ClaimReady = %v, want precondition_failed", err)
	}
	if _, err := service.ClaimReady("ready", "worker-abcd-5678", false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("claimed ClaimReady = %v, want precondition_failed", err)
	}
}

func TestCompleteClaimChangesStatusAndOnlyClearsTheExpectedClaim(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "work.md"), fixture{title: "Work", status: "ready-for-agent"}.content())
	if _, err := service.ClaimReady("work", "worker-abcd-1234", false); err != nil {
		t.Fatalf("ClaimReady: %v", err)
	}
	if _, err := service.SetWorkspace("work", "514a26ed287e429b888000aaa288333a", false); err != nil {
		t.Fatalf("SetWorkspace: %v", err)
	}

	completed, err := service.CompleteClaim("work", "worker-abcd-1234", domain.TicketReadyForHuman, false)
	if err != nil {
		t.Fatalf("CompleteClaim: %v", err)
	}
	if completed.Status != domain.TicketReadyForHuman || completed.ClaimedBy != "" {
		t.Fatalf("completed Ticket = %+v", completed)
	}
	if completed.WorkspaceID != "514a26ed287e429b888000aaa288333a" {
		t.Fatalf("CompleteClaim cleared Workspace ID: %+v", completed)
	}

	again, err := service.CompleteClaim("work", "worker-abcd-1234", domain.TicketReadyForHuman, false)
	if err != nil || again.Status != domain.TicketReadyForHuman {
		t.Fatalf("repeated CompleteClaim = %+v, %v", again, err)
	}
}

func TestUnclaimBranches(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "work.md")
	writeFixture(t, path, fixture{title: "Work", status: "ready-for-agent", claimedBy: "agent-a"}.content())

	// Different claimant: locked.
	if _, err := service.Unclaim("work", "agent-b", false); clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("cross-claimant Unclaim = %v, want locked", err)
	}

	// Same claimant: both fields cleared.
	ticket, err := service.Unclaim("work", "agent-a", false)
	if err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	if ticket.ClaimedBy != "" || ticket.ClaimedAt != "" {
		t.Fatalf("unclaimed ticket = %+v", ticket)
	}
	content := readFile(t, path)
	if !strings.Contains(content, "claimed_by:\n") || !strings.Contains(content, "claimed_at:\n") {
		t.Fatalf("claim fields not cleared:\n%s", content)
	}
	if !strings.Contains(content, "updated: 2026-08-20\n") {
		t.Fatalf("updated not bumped:\n%s", content)
	}

	// Already unclaimed: success with no write.
	bytesBefore := readFile(t, path)
	before, _ := os.Stat(path)
	if _, err := service.Unclaim("work", "agent-b", false); err != nil {
		t.Fatalf("no-op Unclaim: %v", err)
	}
	after, _ := os.Stat(path)
	if readFile(t, path) != bytesBefore || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("no-op unclaim touched the file")
	}
}

func TestCommentAppendsToExistingSection(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "work.md")
	writeFixture(t, path, fixture{title: "Work", status: "needs-triage",
		body: "\n# Work\n\n## What to build\n\nThings.\n\n## Comments\n\nFirst note.\n"}.content())

	if _, err := service.Comment("work", "Second note.\n", false); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	content := readFile(t, path)
	if !strings.HasSuffix(content, "## Comments\n\nFirst note.\n\nSecond note.\n") {
		t.Fatalf("comment not appended:\n%s", content)
	}
	if !strings.Contains(content, "updated: 2026-08-20\n") {
		t.Fatalf("updated not bumped:\n%s", content)
	}
}

func TestCommentCreatesMissingSection(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "work.md")
	writeFixture(t, path, fixture{title: "Work", status: "needs-triage",
		body: "\n# Work\n\nBody only.\n"}.content())

	if _, err := service.Comment("work", "A note.", false); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if !strings.HasSuffix(readFile(t, path), "Body only.\n\n## Comments\n\nA note.\n") {
		t.Fatalf("section not created:\n%s", readFile(t, path))
	}
}

func TestCommentOnBodyWithoutTrailingNewline(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "work.md")
	writeFixture(t, path, fixture{title: "Work", status: "needs-triage",
		body: "\n# Work\n\n## Comments"}.content())

	if _, err := service.Comment("work", "A note.", false); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if !strings.HasSuffix(readFile(t, path), "## Comments\n\nA note.\n") {
		t.Fatalf("append after a missing newline failed:\n%s", readFile(t, path))
	}
}

func TestCommentOnLegacyFixturePreservesLegacyKeys(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "change-monitor", "tkt-cm-001.md")
	writeFixture(t, path, legacyFixture)

	if _, err := service.Comment("tkt-cm-001", "Agent note.", false); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	content := readFile(t, path)
	for _, line := range []string{
		"id: tkt-cm-001\n",
		"type: task\n",
		"category: enhancement\n",
		"workspace: \"[[Change Monitor Agent]]\"\n",
		"parent:\n",
		"title: \"Reconnect Change Monitor VFS tools\"\n",
	} {
		if !strings.Contains(content, line) {
			t.Fatalf("legacy line %q lost:\n%s", line, content)
		}
	}
	if !strings.HasSuffix(content, "## Comments\n\nAgent note.\n") {
		t.Fatalf("comment not appended:\n%s", content)
	}
}

func TestCommentRejectsEmptyText(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "work.md"), fixture{title: "Work", status: "needs-triage"}.content())
	for _, text := range []string{"", "   \n\t\n"} {
		if _, err := service.Comment("work", text, false); clierr.CodeOf(err) != clierr.InvalidUsage {
			t.Fatalf("Comment(%q) = %v, want invalid_usage", text, err)
		}
	}
}

func TestEditReplacesBodyOnly(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "work.md")
	writeFixture(t, path, fixture{title: "Work", status: "needs-triage"}.content())

	if _, err := service.Edit("work", "\n\n# Work\n\nNew body.\n\n\n", false); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	content := readFile(t, path)
	if !strings.HasSuffix(content, "---\n\n# Work\n\nNew body.\n") {
		t.Fatalf("body not replaced:\n%s", content)
	}
	if !strings.Contains(content, "title: \"Work\"\n") || !strings.Contains(content, "status: needs-triage\n") {
		t.Fatalf("frontmatter changed:\n%s", content)
	}
	if !strings.Contains(content, "updated: 2026-08-20\n") {
		t.Fatalf("updated not bumped:\n%s", content)
	}

	if _, err := service.Edit("work", " \n ", false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("empty Edit = %v, want invalid_usage", err)
	}
}

func TestSetValidation(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "work.md"), fixture{title: "Work", status: "needs-triage"}.content())

	_, err := service.Set("work", SetRequest{Status: "open", StatusSet: true}, false)
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("invalid status = %v, want invalid_usage", err)
	}
	for _, status := range domain.TicketStatuses() {
		if !strings.Contains(clierr.HintOf(err), status) {
			t.Fatalf("hint %q does not list %q", clierr.HintOf(err), status)
		}
	}
	if _, err := service.Set("work", SetRequest{Priority: 5, PrioritySet: true}, false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("priority 5 = %v, want invalid_usage", err)
	}
	if _, err := service.Set("work", SetRequest{ProjectSet: true}, false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("empty project = %v, want invalid_usage", err)
	}
	_, err = service.Set("work", SetRequest{Project: "nowhere", ProjectSet: true}, false)
	if clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("missing project = %v, want not_found", err)
	}
	if !strings.Contains(clierr.HintOf(err), "twt projects create") {
		t.Fatalf("hint %q does not point at projects create", clierr.HintOf(err))
	}
}

func TestSetReplacesBlockedBy(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "work.md")
	writeFixture(t, path, fixture{title: "Work", status: "ready-for-agent", blocked: []string{"old-dep"}}.content())

	ticket, err := service.Set("work", SetRequest{
		BlockedBy:    []string{"[[new-dep]]", "new-dep", ""},
		BlockedBySet: true,
	}, false)
	if err != nil {
		t.Fatalf("Set blocked_by: %v", err)
	}
	if strings.Join(ticket.BlockedBy, ",") != "new-dep" {
		t.Fatalf("BlockedBy = %v", ticket.BlockedBy)
	}
	content := readFile(t, path)
	if !strings.Contains(content, "blocked_by:\n  - \"[[new-dep]]\"\n") {
		t.Fatalf("set blocked_by:\n%s", content)
	}
	if strings.Contains(content, "old-dep") {
		t.Fatalf("old blocker remains:\n%s", content)
	}

	cleared, err := service.Set("work", SetRequest{BlockedBySet: true}, false)
	if err != nil {
		t.Fatalf("clear blocked_by: %v", err)
	}
	if len(cleared.BlockedBy) != 0 {
		t.Fatalf("cleared BlockedBy = %v", cleared.BlockedBy)
	}
	if !strings.Contains(readFile(t, path), "blocked_by: []\n") {
		t.Fatalf("cleared file:\n%s", readFile(t, path))
	}

	if _, err := service.Set("work", SetRequest{Priority: 1, PrioritySet: true}, false); err != nil {
		t.Fatalf("priority-only Set: %v", err)
	}
	if !strings.Contains(readFile(t, path), "blocked_by: []\n") {
		t.Fatalf("unrelated Set rewrote blocked_by:\n%s", readFile(t, path))
	}

	_, err = service.Set("work", SetRequest{BlockedBy: []string{"work"}, BlockedBySet: true}, false)
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("self blocker = %v, want invalid_usage", err)
	}
}

func TestSetStatusIsTheUnknownStatusEscapeHatch(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "odd.md"), fixture{title: "Odd", status: "in-progress"}.content())

	// Without StatusSet the unknown status refuses the mutation.
	if _, err := service.Set("odd", SetRequest{Priority: 1, PrioritySet: true}, false); clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("Set without StatusSet = %v, want unsafe_state", err)
	}
	ticket, err := service.Set("odd", SetRequest{Status: "needs-triage", StatusSet: true}, false)
	if err != nil {
		t.Fatalf("escape hatch Set: %v", err)
	}
	if ticket.Status != domain.TicketNeedsTriage {
		t.Fatalf("status = %q", ticket.Status)
	}
}

func TestSetProjectMovesTheFile(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("change-monitor", false); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	source := filepath.Join(home, "work.md")
	writeFixture(t, source, fixture{title: "Work", status: "needs-triage"}.content())

	moved, err := service.Set("work", SetRequest{Project: "change-monitor", ProjectSet: true, Status: "ready-for-agent", StatusSet: true}, false)
	if err != nil {
		t.Fatalf("Set --project: %v", err)
	}
	destination := filepath.Join(home, "change-monitor", "work.md")
	if moved.Path != destination || moved.Project != "change-monitor" {
		t.Fatalf("moved ticket = %+v", moved)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("source file still exists after the move")
	}
	content := readFile(t, destination)
	// All requested changes land in the one destination write.
	if !strings.Contains(content, "project: change-monitor\n") ||
		!strings.Contains(content, "status: ready-for-agent\n") ||
		!strings.Contains(content, "updated: 2026-08-20\n") {
		t.Fatalf("destination frontmatter not healed:\n%s", content)
	}

	// Moving onto itself is a no-op move that still bumps updated.
	before := readFile(t, destination)
	if _, err := service.Set("work", SetRequest{Project: "change-monitor", ProjectSet: true}, false); err != nil {
		t.Fatalf("same-project Set: %v", err)
	}
	after := readFile(t, destination)
	if !strings.Contains(after, "updated: 2026-08-20\n") {
		t.Fatalf("updated missing after same-project set:\n%s", after)
	}
	if strings.Count(after, "\n") != strings.Count(before, "\n") {
		t.Fatal("same-project set changed the line count")
	}

	// A slug collision at the destination refuses the move.
	writeFixture(t, filepath.Join(home, "work2.md"), fixture{title: "Work two", status: "needs-triage"}.content())
	writeFixture(t, filepath.Join(home, "change-monitor", "work2.md"), fixture{title: "Occupied", status: "done"}.content())
	// Both files share a slug now, so address the source by path.
	if _, err := service.Set("work2.md", SetRequest{Project: "change-monitor", ProjectSet: true}, false); clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("duplicate move = %v, want unsafe_state from the duplicate slug", err)
	}
}

func TestMutationDryRunWritesNothing(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("projects", false); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	path := filepath.Join(home, "work.md")
	writeFixture(t, path, fixture{title: "Work", status: "ready-for-agent"}.content())
	before := readFile(t, path)

	claimed, err := service.Claim("work", "agent-a", true)
	if err != nil {
		t.Fatalf("dry-run Claim: %v", err)
	}
	if claimed.ClaimedBy != "agent-a" {
		t.Fatalf("dry-run result = %+v", claimed)
	}
	moved, err := service.Set("work", SetRequest{Project: "projects", ProjectSet: true}, true)
	if err != nil {
		t.Fatalf("dry-run Set: %v", err)
	}
	if moved.Project != "projects" {
		t.Fatalf("dry-run move result = %+v", moved)
	}
	if _, err := service.Comment("work", "note", true); err != nil {
		t.Fatalf("dry-run Comment: %v", err)
	}
	if _, err := service.Edit("work", "new body", true); err != nil {
		t.Fatalf("dry-run Edit: %v", err)
	}
	if readFile(t, path) != before {
		t.Fatal("a dry run changed the file")
	}
	if _, err := os.Stat(filepath.Join(home, "projects", "work.md")); !os.IsNotExist(err) {
		t.Fatal("a dry-run move created the destination")
	}
	// Dry-run still performs every check.
	if _, err := service.Claim("missing", "agent-a", true); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("dry-run Claim on a missing ticket = %v, want not_found", err)
	}
}

func TestCreateRendersTheV1Shape(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("change-monitor", false); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	result, err := service.Create(CreateRequest{Title: "Fix the vfs tools", Project: "change-monitor", Priority: -1}, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	want := `---
title: "Fix the vfs tools"
aliases:
  - Fix the vfs tools
tags:
  - tickets
status: needs-triage
priority: 2
project: change-monitor
blocked_by: []
claimed_by:
claimed_at:
pull_requests: []
created: 2026-08-20
updated: 2026-08-20
---

# Fix the vfs tools

## What to build

## Acceptance criteria

- [ ]

## Blocked by

None - can start immediately

## Comments
`
	if string(result.Content) != want {
		t.Fatalf("created content:\n---got---\n%s\n---want---\n%s", result.Content, want)
	}
	path := filepath.Join(home, "change-monitor", "fix-the-vfs-tools.md")
	if result.Ticket.Path != path || result.Ticket.Slug != "fix-the-vfs-tools" {
		t.Fatalf("created ticket = %+v", result.Ticket)
	}
	if readFile(t, path) != want {
		t.Fatal("written file differs from the returned content")
	}
	// The new file round-trips through the resolver.
	if _, err := service.Resolve("[[fix-the-vfs-tools]]"); err != nil {
		t.Fatalf("Resolve of the new ticket: %v", err)
	}
}

func TestCreateWritesBlockedByWikiLinks(t *testing.T) {
	service, home := newTestService(t)
	result, err := service.Create(CreateRequest{
		Title:     "Follow-up work",
		Priority:  -1,
		Status:    domain.TicketReadyForAgent,
		BlockedBy: []string{"[[dep-one|A dep]]", "dep-two", "dep-two", "  "},
	}, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if strings.Join(result.Ticket.BlockedBy, ",") != "dep-one,dep-two" {
		t.Fatalf("BlockedBy = %v", result.Ticket.BlockedBy)
	}
	content := string(result.Content)
	if !strings.Contains(content, "blocked_by:\n  - \"[[dep-one]]\"\n  - \"[[dep-two]]\"\n") {
		t.Fatalf("frontmatter blocked_by:\n%s", content)
	}
	if !strings.Contains(content, "## Blocked by\n\n- [[dep-one]]\n- [[dep-two]]\n") {
		t.Fatalf("body blocked_by:\n%s", content)
	}
	shown, err := service.Show("follow-up-work")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if shown.Ready {
		t.Fatal("a ticket with missing blockers must not be ready")
	}
	if readFile(t, filepath.Join(home, "follow-up-work.md")) != content {
		t.Fatal("written file differs from the returned content")
	}

	_, err = service.Create(CreateRequest{Title: "Bad blocker", Priority: -1, BlockedBy: []string{"Not A Slug"}}, false)
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("invalid blocker = %v, want invalid_usage", err)
	}
	_, err = service.Create(CreateRequest{Title: "Self block", Slug: "self-block", Priority: -1, BlockedBy: []string{"self-block"}}, false)
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("self blocker = %v, want invalid_usage", err)
	}
}

func TestCreateUngroupedWithBody(t *testing.T) {
	service, home := newTestService(t)
	result, err := service.Create(CreateRequest{Title: "Quick fix", Body: "Do the thing.\n", Priority: 1, Status: domain.TicketReadyForAgent}, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	content := string(result.Content)
	if !strings.Contains(content, "project:\n") {
		t.Fatalf("ungrouped project is not null:\n%s", content)
	}
	if !strings.Contains(content, "status: ready-for-agent\n") || !strings.Contains(content, "priority: 1\n") {
		t.Fatalf("status or priority wrong:\n%s", content)
	}
	// A plain body merges into the skeleton under ## What to build, so the
	// section anchors (Blocked by, Comments) exist on every generated ticket.
	if !strings.Contains(content, "# Quick fix\n\n## What to build\n\nDo the thing.\n") ||
		!strings.Contains(content, "## Comments") {
		t.Fatalf("body wrong:\n%s", content)
	}
	if result.Ticket.Path != filepath.Join(home, "quick-fix.md") {
		t.Fatalf("path = %q", result.Ticket.Path)
	}
}

func TestCreateWithSectionedBodyPassesThrough(t *testing.T) {
	service, _ := newTestService(t)
	body := "## What to build\n\nA custom spec.\n\n## Comments\n"
	result, err := service.Create(CreateRequest{Title: "Custom", Body: body, Priority: 1, Status: domain.TicketReadyForAgent}, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasSuffix(string(result.Content), "---\n\n# Custom\n\n"+body) {
		t.Fatalf("sectioned body changed:\n%s", result.Content)
	}
}

func TestCreateClosedTicketsUsesTheClosedTree(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("core", false); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	done, err := service.Create(CreateRequest{Title: "Shipped", Status: domain.TicketDone, Priority: -1}, false)
	if err != nil {
		t.Fatalf("Create done Ticket: %v", err)
	}
	if want := filepath.Join(home, closedDirectoryName, "shipped.md"); done.Ticket.Path != want {
		t.Fatalf("done Ticket path = %q, want %q", done.Ticket.Path, want)
	}

	wontfix, err := service.Create(CreateRequest{Title: "Dropped", Project: "core", Status: domain.TicketWontfix, Priority: -1}, false)
	if err != nil {
		t.Fatalf("Create wontfix Ticket: %v", err)
	}
	if want := filepath.Join(home, closedDirectoryName, "core", "dropped.md"); wontfix.Ticket.Path != want {
		t.Fatalf("wontfix Ticket path = %q, want %q", wontfix.Ticket.Path, want)
	}

	if content := readFile(t, filepath.Join(home, closedDirectoryName, closedMarkerName)); content == "" {
		t.Fatal("the closed directory marker is empty")
	}
}

func TestCreateErrors(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "taken.md"), fixture{title: "Taken", status: "needs-triage"}.content())

	_, err := service.Create(CreateRequest{Title: "Taken", Priority: -1}, false)
	if clierr.CodeOf(err) != clierr.AlreadyExists {
		t.Fatalf("duplicate slug = %v, want already_exists", err)
	}
	if !strings.Contains(clierr.HintOf(err), "--slug") {
		t.Fatalf("hint %q does not name --slug", clierr.HintOf(err))
	}

	_, err = service.Create(CreateRequest{Title: "日本語", Priority: -1}, false)
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("empty slug = %v, want invalid_usage", err)
	}
	if !strings.Contains(err.Error(), "produces an empty slug") {
		t.Fatalf("message %q", err)
	}

	_, err = service.Create(CreateRequest{Title: "New work", Project: "nowhere", Priority: -1}, false)
	if clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("missing project = %v, want not_found", err)
	}

	_, err = service.Create(CreateRequest{Title: "Ensure me", Project: "nowhere", Priority: -1, EnsureProject: true}, true)
	if err != nil {
		t.Fatalf("EnsureProject dry-run = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "nowhere")); !os.IsNotExist(err) {
		t.Fatal("EnsureProject dry-run created the Project")
	}

	result, err := service.Create(CreateRequest{Title: "Ensure me", Project: "nowhere", Priority: -1, EnsureProject: true}, false)
	if err != nil {
		t.Fatalf("EnsureProject create = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "nowhere", "index.md")); err != nil {
		t.Fatalf("EnsureProject did not create the Project: %v", err)
	}
	if result.Ticket.Project != "nowhere" {
		t.Fatalf("EnsureProject ticket project = %q", result.Ticket.Project)
	}

	if _, err := service.Create(CreateRequest{Title: "Bad status", Status: "open", Priority: -1}, false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("bad status = %v, want invalid_usage", err)
	}
	if _, err := service.Create(CreateRequest{Title: "Bad priority", Priority: 9}, false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("bad priority = %v, want invalid_usage", err)
	}
	if _, err := service.Create(CreateRequest{Title: "Bad slug", Slug: "Bad Slug", Priority: -1}, false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("bad slug = %v, want invalid_usage", err)
	}
}

func TestCreateDryRunWritesNothing(t *testing.T) {
	service, home := newTestService(t)
	result, err := service.Create(CreateRequest{Title: "Dry run", Priority: -1}, true)
	if err != nil {
		t.Fatalf("dry-run Create: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("dry-run Create returned no content")
	}
	if _, err := os.Stat(filepath.Join(home, "dry-run.md")); !os.IsNotExist(err) {
		t.Fatal("dry-run Create wrote the file")
	}
}

func TestInitIsIdempotent(t *testing.T) {
	service, home := newTestService(t)
	first, err := service.Init(false)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if !first.WroteIndex || !first.WroteTemplate || !first.WroteClosedMarker {
		t.Fatalf("first Init = %+v", first)
	}
	indexPath := filepath.Join(home, "index.md")
	templatePath := filepath.Join(home, "templates", "ticket.md")
	// Personalize both notes; the second Init must not overwrite them.
	writeFixture(t, indexPath, "# my custom hub\n")
	writeFixture(t, templatePath, "my custom template\n")
	second, err := service.Init(false)
	if err != nil {
		t.Fatalf("second Init: %v", err)
	}
	if second.WroteIndex || second.WroteTemplate || second.WroteClosedMarker {
		t.Fatalf("second Init = %+v, want no writes", second)
	}
	if readFile(t, indexPath) != "# my custom hub\n" || readFile(t, templatePath) != "my custom template\n" {
		t.Fatal("second Init overwrote a note")
	}
}

func TestInitDryRunWritesNothing(t *testing.T) {
	service, home := newTestService(t)
	result, err := service.Init(true)
	if err != nil {
		t.Fatalf("dry-run Init: %v", err)
	}
	if !result.WroteIndex || !result.WroteTemplate || !result.WroteClosedMarker {
		t.Fatalf("dry-run Init = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(home, "index.md")); !os.IsNotExist(err) {
		t.Fatal("dry-run Init wrote index.md")
	}
	if _, err := os.Stat(filepath.Join(home, closedDirectoryName)); !os.IsNotExist(err) {
		t.Fatal("dry-run Init created the closed directory")
	}
}

func TestInitScaffoldSubstitutesTheFolderName(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatalf("Init: %v", err)
	}
	content := readFile(t, filepath.Join(home, "index.md"))
	folder := filepath.Base(home)
	for _, want := range []string{
		"file.inFolder(\"" + folder + "\")",
		"'!file.inFolder(\"" + folder + "/templates\")'",
		"'!file.inFolder(\"" + folder + "/closed\")'",
		"created: 2026-08-20",
		"status == \"ready-for-agent\"",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("root index misses %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "{{") {
		t.Fatalf("root index keeps a placeholder:\n%s", content)
	}
	template := readFile(t, filepath.Join(home, "templates", "ticket.md"))
	for _, legacy := range []string{"id:", "type:", "category:", "workspace:", "parent:"} {
		if strings.Contains(template, legacy) {
			t.Fatalf("template carries legacy key %q:\n%s", legacy, template)
		}
	}
	if !strings.Contains(template, "project:\n") || !strings.Contains(template, "None - can start immediately") {
		t.Fatalf("template misses v1 content:\n%s", template)
	}
}

func TestProjects(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatalf("Init: %v", err)
	}
	project, err := service.CreateProject("change-monitor", false)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if project.Name != "change-monitor" || !project.HasIndex || project.Tickets != 0 {
		t.Fatalf("project = %+v", project)
	}
	// A second create keeps the personalized index.
	indexPath := filepath.Join(home, "change-monitor", "index.md")
	writeFixture(t, indexPath, "# custom project hub\n")
	if _, err := service.CreateProject("change-monitor", false); err != nil {
		t.Fatalf("second CreateProject: %v", err)
	}
	if readFile(t, indexPath) != "# custom project hub\n" {
		t.Fatal("second CreateProject overwrote index.md")
	}

	writeFixture(t, filepath.Join(home, "change-monitor", "one.md"), fixture{title: "One", status: "needs-triage"}.content())
	writeFixture(t, filepath.Join(home, closedDirectoryName, "change-monitor", "shipped.md"), fixture{title: "Shipped", status: "done"}.content())
	projects, err := service.Projects()
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}
	if len(projects) != 1 || projects[0].Name != "change-monitor" || projects[0].Tickets != 2 || !projects[0].HasIndex {
		t.Fatalf("Projects = %+v", projects)
	}

	if _, err := service.Project("nowhere"); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("missing Project = %v, want not_found", err)
	}
	if _, err := service.CreateProject("bad/name", false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("bad Project name = %v, want invalid_usage", err)
	}
	if _, err := service.CreateProject("templates", false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("reserved Project name = %v, want invalid_usage", err)
	}
	if _, err := service.CreateProject("Closed", false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("closed Project name = %v, want invalid_usage", err)
	}
}

func TestProjectTemplateRoundTripPreservesIndexBody(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateProject("change-monitor", false); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(home, "change-monitor", "index.md")
	before := readFile(t, indexPath)

	dryRun, err := service.SetProjectTemplate("change-monitor", "everysphere", true)
	if err != nil {
		t.Fatalf("SetProjectTemplate(dry run) error = %v", err)
	}
	if dryRun.TemplateName != "everysphere" {
		t.Fatalf("dry-run TemplateName = %q", dryRun.TemplateName)
	}
	if got := readFile(t, indexPath); got != before {
		t.Fatal("dry-run changed index.md")
	}

	project, err := service.SetProjectTemplate("change-monitor", "everysphere", false)
	if err != nil {
		t.Fatalf("SetProjectTemplate() error = %v", err)
	}
	if project.TemplateName != "everysphere" {
		t.Fatalf("TemplateName = %q", project.TemplateName)
	}
	if got := readFile(t, indexPath); !strings.Contains(got, "twt_template: everysphere") || !strings.Contains(got, "# change-monitor") {
		t.Fatalf("index.md did not preserve its body and add the Template:\n%s", got)
	}
	shown, err := service.Project("change-monitor")
	if err != nil {
		t.Fatal(err)
	}
	if shown.TemplateName != "everysphere" {
		t.Fatalf("Project().TemplateName = %q", shown.TemplateName)
	}
}

func TestCloseRejectsASymbolicLinkClosedProjectDirectory(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("core", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{
		Title: "Keep inside home", Project: "core", Status: domain.TicketReadyForAgent, Priority: -1,
	}, false); err != nil {
		t.Fatal(err)
	}
	if err := ensureClosedRoot(home, false); err != nil {
		t.Fatal(err)
	}
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(home, closedDirectoryName, "core")); err != nil {
		t.Skipf("create symlink: %v", err)
	}

	_, err := service.Close("keep-inside-home", "test-agent", false)
	if clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("Close() through a closed Project symlink = %v, want unsafe_state", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "core", "keep-inside-home.md")); statErr != nil {
		t.Fatalf("Close() removed the source Ticket: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(external, "keep-inside-home.md")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Close() wrote through the symlink: %v", statErr)
	}
}

func TestUnmarkedClosedDirectoryIsAConflict(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, closedDirectoryName, "index.md"), "# Existing Project\n")
	writeFixture(t, filepath.Join(home, closedDirectoryName, "work.md"), fixture{title: "Existing work", status: "needs-triage"}.content())

	if _, err := service.Projects(); clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("Projects with an unmarked closed directory = %v, want unsafe_state", err)
	}
	if _, err := service.Create(CreateRequest{Title: "Shipped", Status: domain.TicketDone, Priority: -1}, false); clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("Create with an unmarked closed directory = %v, want unsafe_state", err)
	}
}

func TestProjectIndexScaffoldSubstitutesTheTitle(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("change-monitor", false); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	content := readFile(t, filepath.Join(home, "change-monitor", "index.md"))
	for _, want := range []string{
		"title: \"change-monitor\"",
		"file.folder == this.file.folder",
		"created: 2026-08-20",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("project index misses %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "{{") {
		t.Fatalf("project index keeps a placeholder:\n%s", content)
	}
}

func TestTitleFallsBackToH1ThenSlug(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "h1-only.md"),
		"---\nstatus: needs-triage\n---\n\n# Heading Title\n\nBody.\n")
	writeFixture(t, filepath.Join(home, "bare.md"),
		"---\nstatus: needs-triage\n---\n\nNo heading.\n")

	ticket, err := service.Resolve("h1-only")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ticket.Title != "Heading Title" {
		t.Fatalf("title = %q, want the H1", ticket.Title)
	}
	ticket, err = service.Resolve("bare.md")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if ticket.Title != "bare" {
		t.Fatalf("title = %q, want the slug", ticket.Title)
	}
}

func TestUnknownStatusIsCarriedOnReads(t *testing.T) {
	service, home := newTestService(t)
	writeFixture(t, filepath.Join(home, "odd.md"), fixture{title: "Odd", status: "in-progress", priority: "9"}.content())
	tickets, err := service.List(ListFilter{Status: "in-progress"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tickets) != 1 || string(tickets[0].Status) != "in-progress" || tickets[0].Priority != 9 {
		t.Fatalf("carried ticket = %+v", tickets)
	}
}
