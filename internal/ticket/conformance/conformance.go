// Package conformance is the executable contract of the ticket Store
// interface. Every backend implementation must pass Run. The suite exercises
// only Store methods, never backend internals.
package conformance

import (
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
)

// Factory returns a fresh, initialized, empty Store for one test.
type Factory func(t *testing.T) ticketservice.Store

// Run executes the full contract suite against one backend.
func Run(t *testing.T, backend string, factory Factory) {
	t.Helper()
	cases := []struct {
		name string
		test func(t *testing.T, store ticketservice.Store)
	}{
		{"CreateAndResolve", testCreateAndResolve},
		{"ClaimIsCompareAndSet", testClaimIsCompareAndSet},
		{"ClaimReadyRespectsBlockers", testClaimReadyRespectsBlockers},
		{"CompleteWorkIsAtomicAndIdempotent", testCompleteWorkIsAtomicAndIdempotent},
		{"UnclaimGuardsTheClaimant", testUnclaimGuardsTheClaimant},
		{"CloseUnblocksDependents", testCloseUnblocksDependents},
		{"DryRunWritesNothing", testDryRunWritesNothing},
		{"QueueReportsCycles", testQueueReportsCycles},
		{"ApprovePlanLifecycle", testApprovePlanLifecycle},
	}
	for _, testCase := range cases {
		t.Run(backend+"/"+testCase.name, func(t *testing.T) {
			testCase.test(t, factory(t))
		})
	}
}

func mustCreate(t *testing.T, store ticketservice.Store, slug string, blockedBy ...string) {
	t.Helper()
	_, err := store.Create(ticketservice.CreateRequest{
		Title: slug, Slug: slug, Status: domain.TicketReadyForAgent, Priority: 1, BlockedBy: blockedBy,
	}, false)
	if err != nil {
		t.Fatalf("create %s: %v", slug, err)
	}
}

func testCreateAndResolve(t *testing.T, store ticketservice.Store) {
	mustCreate(t, store, "fix-auth")
	ticket, err := store.Resolve("fix-auth")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ticket.Slug != "fix-auth" || ticket.Status != domain.TicketReadyForAgent || ticket.ClaimedBy != "" {
		t.Fatalf("resolved ticket = %+v", ticket)
	}
}

func testClaimIsCompareAndSet(t *testing.T, store ticketservice.Store) {
	mustCreate(t, store, "fix-auth")
	if _, err := store.Claim("fix-auth", "worker-a", false); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, err := store.Claim("fix-auth", "worker-b", false); clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("competing claim error = %v, want locked", err)
	}
	// A retry of a mutation that already succeeded is a no-op success.
	if _, err := store.Claim("fix-auth", "worker-a", false); err != nil {
		t.Fatalf("same-claimant retry: %v", err)
	}
	claimed, err := store.Resolve("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ClaimedBy != "worker-a" {
		t.Fatalf("ClaimedBy = %q, want worker-a", claimed.ClaimedBy)
	}
}

func testClaimReadyRespectsBlockers(t *testing.T, store ticketservice.Store) {
	mustCreate(t, store, "blocker")
	mustCreate(t, store, "dependent", "blocker")
	if _, err := store.ClaimReady("dependent", "worker-a", false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("blocked claim-ready error = %v, want precondition_failed", err)
	}
	if _, err := store.Close("blocker", "human", false); err != nil {
		t.Fatalf("close blocker: %v", err)
	}
	if _, err := store.ClaimReady("dependent", "worker-a", false); err != nil {
		t.Fatalf("claim-ready after blocker closed: %v", err)
	}
}

func testCompleteWorkIsAtomicAndIdempotent(t *testing.T, store ticketservice.Store) {
	mustCreate(t, store, "fix-auth")
	if _, err := store.Claim("fix-auth", "worker-a", false); err != nil {
		t.Fatal(err)
	}
	urls := []string{"https://example.com/pr/1"}
	done, err := store.CompleteWork("fix-auth", "worker-a", domain.TicketReadyForHuman, urls, false)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if done.Status != domain.TicketReadyForHuman || done.ClaimedBy != "" || len(done.PullRequests) != 1 {
		t.Fatalf("completed ticket = %+v", done)
	}
	// Retry after success is a no-op success.
	if _, err := store.CompleteWork("fix-auth", "worker-a", domain.TicketReadyForHuman, urls, false); err != nil {
		t.Fatalf("complete retry: %v", err)
	}
	// A foreign claimant cannot complete a re-claimed ticket.
	if _, err := store.Claim("fix-auth", "worker-b", false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteWork("fix-auth", "worker-a", domain.TicketReadyForHuman, nil, false); clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("foreign complete error = %v, want locked", err)
	}
}

func testUnclaimGuardsTheClaimant(t *testing.T, store ticketservice.Store) {
	mustCreate(t, store, "fix-auth")
	if _, err := store.Claim("fix-auth", "worker-a", false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Unclaim("fix-auth", "worker-b", false); clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("foreign unclaim error = %v, want locked", err)
	}
	if _, err := store.Unclaim("fix-auth", "worker-a", false); err != nil {
		t.Fatalf("own unclaim: %v", err)
	}
	// Unclaiming an unclaimed ticket is a no-op success.
	if _, err := store.Unclaim("fix-auth", "worker-a", false); err != nil {
		t.Fatalf("unclaim retry: %v", err)
	}
}

func testCloseUnblocksDependents(t *testing.T, store ticketservice.Store) {
	if _, err := store.CreateProject("core", false); err != nil {
		t.Fatalf("create project: %v", err)
	}
	create := func(slug string, blockedBy ...string) {
		_, err := store.Create(ticketservice.CreateRequest{
			Title: slug, Slug: slug, Project: "core",
			Status: domain.TicketReadyForAgent, Priority: 1, BlockedBy: blockedBy,
		}, false)
		if err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
	}
	create("blocker")
	create("dependent", "blocker")
	queue, err := store.Queue("core", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue.Ready) != 1 || queue.Ready[0].Slug != "blocker" {
		t.Fatalf("ready before close = %+v", queue.Ready)
	}
	if _, err := store.Close("blocker", "human", false); err != nil {
		t.Fatal(err)
	}
	queue, err = store.Queue("core", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue.Ready) != 1 || queue.Ready[0].Slug != "dependent" {
		t.Fatalf("ready after close = %+v", queue.Ready)
	}
}

func testDryRunWritesNothing(t *testing.T, store ticketservice.Store) {
	mustCreate(t, store, "fix-auth")
	if _, err := store.Claim("fix-auth", "worker-a", true); err != nil {
		t.Fatalf("dry-run claim: %v", err)
	}
	ticket, err := store.Resolve("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if ticket.ClaimedBy != "" {
		t.Fatalf("dry run claimed the ticket: %q", ticket.ClaimedBy)
	}
}

func testApprovePlanLifecycle(t *testing.T, store ticketservice.Store) {
	mustCreate(t, store, "fix-auth")
	if _, err := store.Approve("fix-auth", "human", "", false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("approve without a plan = %v, want precondition_failed", err)
	}
	if _, err := store.SetPlanSection("fix-auth", "", "Do the thing.", false); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	approved, err := store.Approve("fix-auth", "human", "looks good", false)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.PlanApprovedBy != "human" || approved.PlanApprovedAt == "" {
		t.Fatalf("approval stamp = %+v", approved)
	}
	// A retry after success is a no-op success, and the first stamp stays.
	retried, err := store.Approve("fix-auth", "someone-else", "", false)
	if err != nil {
		t.Fatalf("approve retry: %v", err)
	}
	if retried.PlanApprovedBy != "human" {
		t.Fatalf("retry overwrote the stamp: %+v", retried)
	}
	// A plan rewrite clears the approval: a changed plan needs a new one.
	replanned, err := store.SetPlanSection("fix-auth", "", "Do it differently.", false)
	if err != nil {
		t.Fatalf("rewrite plan: %v", err)
	}
	if replanned.PlanApprovedBy != "" || replanned.PlanApprovedAt != "" {
		t.Fatalf("plan rewrite kept the approval: %+v", replanned)
	}
}

func testQueueReportsCycles(t *testing.T, store ticketservice.Store) {
	if _, err := store.CreateProject("core", false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ticketservice.CreateRequest{
		Title: "a", Slug: "a", Project: "core", Status: domain.TicketReadyForAgent, Priority: 1, BlockedBy: []string{"b"},
	}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Create(ticketservice.CreateRequest{
		Title: "b", Slug: "b", Project: "core", Status: domain.TicketReadyForAgent, Priority: 1, BlockedBy: []string{"a"},
	}, false); err != nil {
		t.Fatal(err)
	}
	queue, err := store.Queue("core", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue.Cycles) != 1 || len(queue.Ready) != 0 {
		t.Fatalf("queue = ready %+v cycles %+v, want no ready and one cycle", queue.Ready, queue.Cycles)
	}
}
