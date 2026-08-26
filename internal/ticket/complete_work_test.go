package ticket

import (
	"os"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func completeWorkFixture(t *testing.T) *Service {
	t.Helper()
	service, _ := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{
		Title: "Fix auth", Slug: "fix-auth", Status: domain.TicketReadyForAgent, Priority: 1,
	}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim("fix-auth", "twt-local-01234567", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetWorkspace("fix-auth", "ws01234567", false); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestCompleteWorkRecordsPullRequestsAndReleasesTheClaimAtomically(t *testing.T) {
	service := completeWorkFixture(t)
	ticket, err := service.CompleteWork("fix-auth", "twt-local-01234567", domain.TicketReadyForHuman,
		[]string{"https://origin.cursor.com/acme/api/pull/7"}, false)
	if err != nil {
		t.Fatalf("CompleteWork: %v", err)
	}
	if ticket.Status != domain.TicketReadyForHuman || ticket.ClaimedBy != "" {
		t.Fatalf("ticket = %+v", ticket)
	}
	if len(ticket.PullRequests) != 1 || ticket.PullRequests[0] != "https://origin.cursor.com/acme/api/pull/7" {
		t.Fatalf("PullRequests = %v", ticket.PullRequests)
	}
	if ticket.WorkspaceID != "ws01234567" {
		t.Fatalf("WorkspaceID = %q, want preserved", ticket.WorkspaceID)
	}
	content, err := os.ReadFile(ticket.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "pull_requests:\n  - https://origin.cursor.com/acme/api/pull/7") {
		t.Fatalf("frontmatter:\n%s", content)
	}
}

func TestCompleteWorkMergesAndDeduplicatesPullRequests(t *testing.T) {
	service := completeWorkFixture(t)
	if _, err := service.CompleteWork("fix-auth", "twt-local-01234567", domain.TicketReadyForHuman,
		[]string{"https://origin.cursor.com/acme/api/pull/7"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Claim("fix-auth", "twt-local-01234567", false); err != nil {
		t.Fatal(err)
	}
	ticket, err := service.CompleteWork("fix-auth", "twt-local-01234567", domain.TicketReadyForHuman,
		[]string{"https://origin.cursor.com/acme/api/pull/7", "https://origin.cursor.com/acme/web/pull/8"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticket.PullRequests) != 2 || ticket.PullRequests[1] != "https://origin.cursor.com/acme/web/pull/8" {
		t.Fatalf("PullRequests = %v", ticket.PullRequests)
	}
}

func TestCompleteWorkIsIdempotentForARetryAfterSuccess(t *testing.T) {
	service := completeWorkFixture(t)
	urls := []string{"https://origin.cursor.com/acme/api/pull/7"}
	if _, err := service.CompleteWork("fix-auth", "twt-local-01234567", domain.TicketReadyForHuman, urls, false); err != nil {
		t.Fatal(err)
	}
	ticket, err := service.CompleteWork("fix-auth", "twt-local-01234567", domain.TicketReadyForHuman, urls, false)
	if err != nil {
		t.Fatalf("retry after success: %v", err)
	}
	if ticket.Status != domain.TicketReadyForHuman || len(ticket.PullRequests) != 1 {
		t.Fatalf("retry ticket = %+v", ticket)
	}
}

func TestCompleteWorkGuardsTheClaim(t *testing.T) {
	service := completeWorkFixture(t)
	_, err := service.CompleteWork("fix-auth", "someone-else", domain.TicketReadyForHuman, nil, false)
	if clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("foreign claimant error = %v, want locked", err)
	}
	if _, err := service.Unclaim("fix-auth", "twt-local-01234567", false); err != nil {
		t.Fatal(err)
	}
	_, err = service.CompleteWork("fix-auth", "twt-local-01234567", domain.TicketReadyForHuman,
		[]string{"https://origin.cursor.com/acme/api/pull/7"}, false)
	if clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("released claim error = %v, want unsafe_state", err)
	}
}

func TestCompleteWorkValidatesInputs(t *testing.T) {
	service := completeWorkFixture(t)
	_, err := service.CompleteWork("fix-auth", "twt-local-01234567", domain.TicketDone, nil, false)
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("done status error = %v, want invalid_usage", err)
	}
	_, err = service.CompleteWork("fix-auth", "twt-local-01234567", domain.TicketReadyForHuman,
		[]string{"http://insecure.example/pr/1"}, false)
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("http URL error = %v, want invalid_usage", err)
	}
}
