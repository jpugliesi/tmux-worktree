package ticket

import (
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// stackFixture: blocker in review with a PR, dependent blocked by it.
func stackFixture(t *testing.T) *Service {
	t.Helper()
	service, _ := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateProject("core", false); err != nil {
		t.Fatal(err)
	}
	create := func(slug string, blockedBy ...string) {
		t.Helper()
		if _, err := service.Create(CreateRequest{
			Title: slug, Slug: slug, Project: "core",
			Status: domain.TicketReadyForAgent, Priority: 1, BlockedBy: blockedBy,
		}, false); err != nil {
			t.Fatal(err)
		}
	}
	create("blocker")
	create("dependent", "blocker")
	if _, err := service.Claim("blocker", "worker-a", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CompleteWork("blocker", "worker-a", domain.TicketReadyForHuman,
		[]string{"https://origin.cursor.com/acme/api/pull/7"}, false); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestQueueReportsTheStackReadyTier(t *testing.T) {
	service := stackFixture(t)
	queue, err := service.Queue("core", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue.Ready) != 0 {
		t.Fatalf("ready = %+v, want none (blocker only in review)", queue.Ready)
	}
	if len(queue.StackReady) != 1 || queue.StackReady[0].Slug != "dependent" {
		t.Fatalf("stackReady = %+v", queue.StackReady)
	}
}

func TestStackReadyExcludesTheUnreviewedAndTheReady(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateProject("core", false); err != nil {
		t.Fatal(err)
	}
	create := func(slug string, blockedBy ...string) {
		t.Helper()
		if _, err := service.Create(CreateRequest{
			Title: slug, Slug: slug, Project: "core",
			Status: domain.TicketReadyForAgent, Priority: 1, BlockedBy: blockedBy,
		}, false); err != nil {
			t.Fatal(err)
		}
	}
	create("open-blocker")
	create("waiting", "open-blocker")
	queue, err := service.Queue("core", 0)
	if err != nil {
		t.Fatal(err)
	}
	// open-blocker is true-ready, not stack-ready; waiting has an open
	// blocker without a PR, so it is neither.
	if len(queue.StackReady) != 0 {
		t.Fatalf("stackReady = %+v, want none", queue.StackReady)
	}
	if len(queue.Ready) != 1 || queue.Ready[0].Slug != "open-blocker" {
		t.Fatalf("ready = %+v", queue.Ready)
	}
}

func TestClaimStackReadyStampsTheBase(t *testing.T) {
	service := stackFixture(t)
	if _, err := service.ClaimReady("dependent", "worker-b", false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("ClaimReady on a stacked dependent = %v, want precondition_failed", err)
	}
	claimed, err := service.ClaimStackReady("dependent", "worker-b", "blocker@twt/blocker", false)
	if err != nil {
		t.Fatalf("ClaimStackReady: %v", err)
	}
	if claimed.ClaimedBy != "worker-b" || claimed.StackBase != "blocker@twt/blocker" {
		t.Fatalf("claimed = %+v", claimed)
	}
	// Same-claimant retry is a no-op success; a rival gets locked.
	if _, err := service.ClaimStackReady("dependent", "worker-b", "", false); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := service.ClaimStackReady("dependent", "worker-c", "", false); clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("rival claim = %v, want locked", err)
	}
}

func TestClaimStackReadyRefusesAnUnreviewedBlocker(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateProject("core", false); err != nil {
		t.Fatal(err)
	}
	for _, req := range []CreateRequest{
		{Title: "blocker", Slug: "blocker", Project: "core", Status: domain.TicketReadyForAgent, Priority: 1},
		{Title: "dependent", Slug: "dependent", Project: "core", Status: domain.TicketReadyForAgent, Priority: 1, BlockedBy: []string{"blocker"}},
	} {
		if _, err := service.Create(req, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.ClaimStackReady("dependent", "worker-b", "", false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("unreviewed-blocker stack claim = %v, want precondition_failed", err)
	}
	// A true-ready ticket stays claimable through the stack verb (the
	// coordinator can use one code path).
	if _, err := service.ClaimStackReady("blocker", "worker-a", "", false); err != nil {
		t.Fatalf("stack claim on ready ticket: %v", err)
	}
}
