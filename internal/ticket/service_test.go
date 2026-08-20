package ticket

import (
	"os"
	"path/filepath"
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
	writeFixture(t, filepath.Join(home, "boards", "index.md"), "# hub\n")
	writeFixture(t, filepath.Join(home, "ungrouped.md"), fixture{title: "Ungrouped", status: "needs-triage", priority: "3"}.content())
	writeFixture(t, filepath.Join(home, "boards", "beta.md"), fixture{title: "Beta", status: "needs-triage", priority: "1"}.content())
	writeFixture(t, filepath.Join(home, "boards", "alpha.md"), fixture{title: "Alpha", status: "done", priority: "1"}.content())
	writeFixture(t, filepath.Join(home, "no-priority.md"), fixture{title: "No priority", status: "needs-triage"}.content())

	all, err := service.List(ListFilter{})
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

	board, err := service.List(ListFilter{Board: "boards", BoardSet: true})
	if err != nil {
		t.Fatalf("List --board: %v", err)
	}
	if len(board) != 2 || board[0].Slug != "alpha" || board[1].Slug != "beta" {
		t.Fatalf("board filter = %+v", board)
	}
	if board[0].Board != "boards" {
		t.Fatalf("Board field = %q", board[0].Board)
	}

	ungrouped, err := service.List(ListFilter{BoardSet: true})
	if err != nil {
		t.Fatalf("List --board '': %v", err)
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
	if !strings.Contains(clierr.HintOf(err), "twt2 tickets list --ready") {
		t.Fatalf("locked hint %q does not point at the ready list", clierr.HintOf(err))
	}
	if readFile(t, path) != bytesBefore {
		t.Fatal("failed claim changed the file")
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
		"project: \"[[Change Monitor Agent]]\"\n",
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
	if _, err := service.Set("work", SetRequest{BoardSet: true}, false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("empty board = %v, want invalid_usage", err)
	}
	_, err = service.Set("work", SetRequest{Board: "nowhere", BoardSet: true}, false)
	if clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("missing board = %v, want not_found", err)
	}
	if !strings.Contains(clierr.HintOf(err), "twt2 tickets boards create") {
		t.Fatalf("hint %q does not point at boards create", clierr.HintOf(err))
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

func TestSetBoardMovesTheFile(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateBoard("change-monitor", false); err != nil {
		t.Fatalf("CreateBoard: %v", err)
	}
	source := filepath.Join(home, "work.md")
	writeFixture(t, source, fixture{title: "Work", status: "needs-triage"}.content())

	moved, err := service.Set("work", SetRequest{Board: "change-monitor", BoardSet: true, Status: "ready-for-agent", StatusSet: true}, false)
	if err != nil {
		t.Fatalf("Set --board: %v", err)
	}
	destination := filepath.Join(home, "change-monitor", "work.md")
	if moved.Path != destination || moved.Board != "change-monitor" {
		t.Fatalf("moved ticket = %+v", moved)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("source file still exists after the move")
	}
	content := readFile(t, destination)
	// All requested changes land in the one destination write.
	if !strings.Contains(content, "board: change-monitor\n") ||
		!strings.Contains(content, "status: ready-for-agent\n") ||
		!strings.Contains(content, "updated: 2026-08-20\n") {
		t.Fatalf("destination frontmatter not healed:\n%s", content)
	}

	// Moving onto itself is a no-op move that still bumps updated.
	before := readFile(t, destination)
	if _, err := service.Set("work", SetRequest{Board: "change-monitor", BoardSet: true}, false); err != nil {
		t.Fatalf("same-board Set: %v", err)
	}
	after := readFile(t, destination)
	if !strings.Contains(after, "updated: 2026-08-20\n") {
		t.Fatalf("updated missing after same-board set:\n%s", after)
	}
	if strings.Count(after, "\n") != strings.Count(before, "\n") {
		t.Fatal("same-board set changed the line count")
	}

	// A slug collision at the destination refuses the move.
	writeFixture(t, filepath.Join(home, "work2.md"), fixture{title: "Work two", status: "needs-triage"}.content())
	writeFixture(t, filepath.Join(home, "change-monitor", "work2.md"), fixture{title: "Occupied", status: "done"}.content())
	// Both files share a slug now, so address the source by path.
	if _, err := service.Set("work2.md", SetRequest{Board: "change-monitor", BoardSet: true}, false); clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("duplicate move = %v, want unsafe_state from the duplicate slug", err)
	}
}

func TestMutationDryRunWritesNothing(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateBoard("boards", false); err != nil {
		t.Fatalf("CreateBoard: %v", err)
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
	moved, err := service.Set("work", SetRequest{Board: "boards", BoardSet: true}, true)
	if err != nil {
		t.Fatalf("dry-run Set: %v", err)
	}
	if moved.Board != "boards" {
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
	if _, err := os.Stat(filepath.Join(home, "boards", "work.md")); !os.IsNotExist(err) {
		t.Fatal("a dry-run move created the destination")
	}
	// Dry-run still performs every check.
	if _, err := service.Claim("missing", "agent-a", true); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("dry-run Claim on a missing ticket = %v, want not_found", err)
	}
}

func TestCreateRendersTheV1Shape(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateBoard("change-monitor", false); err != nil {
		t.Fatalf("CreateBoard: %v", err)
	}
	result, err := service.Create(CreateRequest{Title: "Fix the vfs tools", Board: "change-monitor", Priority: -1}, false)
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
board: change-monitor
blocked_by: []
claimed_by:
claimed_at:
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

func TestCreateUngroupedWithBody(t *testing.T) {
	service, home := newTestService(t)
	result, err := service.Create(CreateRequest{Title: "Quick fix", Body: "Do the thing.\n", Priority: 1, Status: domain.TicketReadyForAgent}, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	content := string(result.Content)
	if !strings.Contains(content, "board:\n") {
		t.Fatalf("ungrouped board is not null:\n%s", content)
	}
	if !strings.Contains(content, "status: ready-for-agent\n") || !strings.Contains(content, "priority: 1\n") {
		t.Fatalf("status or priority wrong:\n%s", content)
	}
	if !strings.HasSuffix(content, "---\n\n# Quick fix\n\nDo the thing.\n") {
		t.Fatalf("body wrong:\n%s", content)
	}
	if result.Ticket.Path != filepath.Join(home, "quick-fix.md") {
		t.Fatalf("path = %q", result.Ticket.Path)
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

	_, err = service.Create(CreateRequest{Title: "New work", Board: "nowhere", Priority: -1}, false)
	if clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("missing board = %v, want not_found", err)
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
	if !first.WroteIndex || !first.WroteTemplate {
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
	if second.WroteIndex || second.WroteTemplate {
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
	if !result.WroteIndex || !result.WroteTemplate {
		t.Fatalf("dry-run Init = %+v", result)
	}
	if _, err := os.Stat(filepath.Join(home, "index.md")); !os.IsNotExist(err) {
		t.Fatal("dry-run Init wrote index.md")
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
	for _, legacy := range []string{"id:", "type:", "category:", "project:", "parent:"} {
		if strings.Contains(template, legacy) {
			t.Fatalf("template carries legacy key %q:\n%s", legacy, template)
		}
	}
	if !strings.Contains(template, "board:\n") || !strings.Contains(template, "None - can start immediately") {
		t.Fatalf("template misses v1 content:\n%s", template)
	}
}

func TestBoards(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatalf("Init: %v", err)
	}
	board, err := service.CreateBoard("change-monitor", false)
	if err != nil {
		t.Fatalf("CreateBoard: %v", err)
	}
	if board.Name != "change-monitor" || !board.HasIndex || board.Tickets != 0 {
		t.Fatalf("board = %+v", board)
	}
	// A second create keeps the personalized index.
	indexPath := filepath.Join(home, "change-monitor", "index.md")
	writeFixture(t, indexPath, "# custom board hub\n")
	if _, err := service.CreateBoard("change-monitor", false); err != nil {
		t.Fatalf("second CreateBoard: %v", err)
	}
	if readFile(t, indexPath) != "# custom board hub\n" {
		t.Fatal("second CreateBoard overwrote index.md")
	}

	writeFixture(t, filepath.Join(home, "change-monitor", "one.md"), fixture{title: "One", status: "needs-triage"}.content())
	boards, err := service.Boards()
	if err != nil {
		t.Fatalf("Boards: %v", err)
	}
	if len(boards) != 1 || boards[0].Name != "change-monitor" || boards[0].Tickets != 1 || !boards[0].HasIndex {
		t.Fatalf("Boards = %+v", boards)
	}

	if _, err := service.Board("nowhere"); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("missing Board = %v, want not_found", err)
	}
	if _, err := service.CreateBoard("bad/name", false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("bad Board name = %v, want invalid_usage", err)
	}
	if _, err := service.CreateBoard("templates", false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("reserved Board name = %v, want invalid_usage", err)
	}
}

func TestBoardIndexScaffoldSubstitutesTheTitle(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateBoard("change-monitor", false); err != nil {
		t.Fatalf("CreateBoard: %v", err)
	}
	content := readFile(t, filepath.Join(home, "change-monitor", "index.md"))
	for _, want := range []string{
		"title: \"change-monitor\"",
		"file.folder == this.file.folder",
		"created: 2026-08-20",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("board index misses %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "{{") {
		t.Fatalf("board index keeps a placeholder:\n%s", content)
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
