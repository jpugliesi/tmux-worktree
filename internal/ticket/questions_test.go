package ticket

import (
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func askFixture(t *testing.T) *Service {
	t.Helper()
	service, _ := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{Title: "Fix auth", Slug: "fix-auth", Status: domain.TicketReadyForAgent}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim("fix-auth", "twt-local-01234567", false); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestAskParksTheTicketAndKeepsTheClaim(t *testing.T) {
	service := askFixture(t)
	ticket, err := service.Ask("fix-auth", "twt-local-01234567", "Drop the column or flag it?", false)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if ticket.Status != domain.TicketNeedsInfo || ticket.ClaimedBy != "twt-local-01234567" {
		t.Fatalf("ticket after ask = %+v", ticket)
	}
	shown, err := service.Show("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown.Body, "## Questions") || !strings.Contains(shown.Body, "### Q ") ||
		!strings.Contains(shown.Body, "Drop the column or flag it?") {
		t.Fatalf("questions section:\n%s", shown.Body)
	}
	// The Questions section lands before Comments.
	if strings.Index(shown.Body, "## Questions") > strings.Index(shown.Body, "## Comments") {
		t.Fatalf("questions after comments:\n%s", shown.Body)
	}
	// The prior status is remembered.
	if shown.Ticket.AskStatus != string(domain.TicketReadyForAgent) {
		t.Fatalf("AskStatus = %q", shown.Ticket.AskStatus)
	}
	// Retry of the same open question is a no-op success.
	if _, err := service.Ask("fix-auth", "twt-local-01234567", "Drop the column or flag it?", false); err != nil {
		t.Fatalf("ask retry: %v", err)
	}
	again, err := service.Show("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(again.Body, "Drop the column or flag it?") != 1 {
		t.Fatalf("retry duplicated the question:\n%s", again.Body)
	}
}

func TestAskGuards(t *testing.T) {
	service := askFixture(t)
	if _, err := service.Ask("fix-auth", "someone-else", "q", false); clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("foreign ask error = %v, want locked", err)
	}
	if _, err := service.Ask("fix-auth", "twt-local-01234567", "  ", false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatal("empty question accepted")
	}
	if _, err := service.Unclaim("fix-auth", "twt-local-01234567", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Ask("fix-auth", "twt-local-01234567", "q", false); clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatal("unclaimed ask accepted")
	}
}

func TestAnswerRestoresTheStatusAndRelays(t *testing.T) {
	service := askFixture(t)
	// Park from a non-default status to prove the round trip.
	if _, err := service.Set("fix-auth", SetRequest{Status: string(domain.TicketNeedsTriage), StatusSet: true}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Ask("fix-auth", "twt-local-01234567", "Which provider?", false); err != nil {
		t.Fatal(err)
	}
	// A second ask keeps the ORIGINAL stored status.
	if _, err := service.Ask("fix-auth", "twt-local-01234567", "And which region?", false); err != nil {
		t.Fatal(err)
	}
	answered, err := service.Answer("fix-auth", "OAuth; us-east.", false)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	if answered.Status != domain.TicketNeedsTriage || answered.ClaimedBy != "twt-local-01234567" {
		t.Fatalf("ticket after answer = %+v", answered)
	}
	if answered.AskStatus != "" {
		t.Fatalf("AskStatus not cleared: %q", answered.AskStatus)
	}
	shown, err := service.Show("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown.Body, "### A ") || !strings.Contains(shown.Body, "OAuth; us-east.") {
		t.Fatalf("answer entry missing:\n%s", shown.Body)
	}
	// Answering a ticket that is not waiting is refused.
	if _, err := service.Answer("fix-auth", "again", false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatal("answer on a non-waiting ticket accepted")
	}
}

func TestNeedsInputListFilter(t *testing.T) {
	service := askFixture(t)
	if _, err := service.Ask("fix-auth", "twt-local-01234567", "q?", false); err != nil {
		t.Fatal(err)
	}
	// An unclaimed needs-info ticket is plain triage, not waiting-on-input.
	if _, err := service.Create(CreateRequest{Title: "Triage me", Status: domain.TicketNeedsInfo}, false); err != nil {
		t.Fatal(err)
	}
	waiting, err := service.List(ListFilter{NeedsInput: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 1 || waiting[0].Slug != "fix-auth" {
		t.Fatalf("needs-input list = %+v", waiting)
	}
	if _, err := service.List(ListFilter{NeedsInput: true, Ready: true}); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatal("needs-input plus ready accepted")
	}
}

func TestAskDryRunWritesNothing(t *testing.T) {
	service := askFixture(t)
	if _, err := service.Ask("fix-auth", "twt-local-01234567", "q?", true); err != nil {
		t.Fatal(err)
	}
	ticket, err := service.Resolve("fix-auth")
	if err != nil {
		t.Fatal(err)
	}
	if ticket.Status != domain.TicketReadyForAgent || ticket.AskStatus != "" {
		t.Fatalf("dry-run ask changed the ticket: %+v", ticket)
	}
}
