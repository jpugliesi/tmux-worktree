package ticket

import (
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestAddPullRequestsKeepsStatusAndClaim(t *testing.T) {
	service := askFixture(t) // claimed fix-auth by twt-local-01234567
	ticket, err := service.AddPullRequests("fix-auth", "twt-local-01234567",
		[]string{"https://origin.cursor.com/acme/api/pull/7"}, false)
	if err != nil {
		t.Fatalf("AddPullRequests: %v", err)
	}
	if ticket.Status != domain.TicketReadyForAgent || ticket.ClaimedBy != "twt-local-01234567" {
		t.Fatalf("pr add touched status or claim: %+v", ticket)
	}
	if len(ticket.PullRequests) != 1 {
		t.Fatalf("PullRequests = %v", ticket.PullRequests)
	}
	// Duplicate add is a no-op; complete later dedupes with the same URL.
	if _, err := service.AddPullRequests("fix-auth", "twt-local-01234567",
		[]string{"https://origin.cursor.com/acme/api/pull/7"}, false); err != nil {
		t.Fatalf("duplicate add: %v", err)
	}
	done, err := service.CompleteWork("fix-auth", "twt-local-01234567", domain.TicketReadyForHuman,
		[]string{"https://origin.cursor.com/acme/api/pull/7"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(done.PullRequests) != 1 {
		t.Fatalf("complete duplicated the URL: %v", done.PullRequests)
	}
}

func TestPullRequestClaimGuards(t *testing.T) {
	service := askFixture(t)
	if _, err := service.AddPullRequests("fix-auth", "", []string{"https://x.example/pr/1"}, false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("claimed ticket without --as error = %v, want invalid_usage", err)
	}
	if _, err := service.AddPullRequests("fix-auth", "someone-else", []string{"https://x.example/pr/1"}, false); clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("foreign claimant error = %v, want locked", err)
	}
	if _, err := service.AddPullRequests("fix-auth", "twt-local-01234567", []string{"http://insecure/pr/1"}, false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatal("http URL accepted")
	}
	// Unclaimed tickets accept any caller.
	if _, err := service.Unclaim("fix-auth", "twt-local-01234567", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddPullRequests("fix-auth", "", []string{"https://x.example/pr/2"}, false); err != nil {
		t.Fatalf("unclaimed add: %v", err)
	}
}

func TestRemovePullRequests(t *testing.T) {
	service := askFixture(t)
	urls := []string{"https://x.example/pr/1", "https://x.example/pr/2"}
	if _, err := service.AddPullRequests("fix-auth", "twt-local-01234567", urls, false); err != nil {
		t.Fatal(err)
	}
	ticket, err := service.RemovePullRequests("fix-auth", "twt-local-01234567", []string{"https://x.example/pr/1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticket.PullRequests) != 1 || ticket.PullRequests[0] != "https://x.example/pr/2" {
		t.Fatalf("PullRequests after rm = %v", ticket.PullRequests)
	}
	// Removing an absent URL is a no-op.
	if _, err := service.RemovePullRequests("fix-auth", "twt-local-01234567", []string{"https://x.example/pr/9"}, false); err != nil {
		t.Fatalf("absent rm: %v", err)
	}
}
