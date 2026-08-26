package localdispatch

import (
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
)

// stackedFixture: a stacking template, a blocker in review with a PR whose
// dispatch left a Workspace on this machine, and a stacked dependent.
func stackedFixture(t *testing.T) localFixture {
	t.Helper()
	fixture := newLocalFixture(t)
	template, err := fixture.service.options.Templates.Load("product")
	if err != nil {
		t.Fatal(err)
	}
	template.LocalDispatch.Stacking = true
	if err := fixture.service.options.Templates.Save(template); err != nil {
		t.Fatal(err)
	}
	create := func(slug string, blockedBy ...string) {
		t.Helper()
		if _, err := fixture.tickets.Create(ticketservice.CreateRequest{
			Title: slug, Slug: slug, Project: "core",
			Status: domain.TicketReadyForAgent, Priority: 1, BlockedBy: blockedBy,
		}, false); err != nil {
			t.Fatal(err)
		}
	}
	create("blocker")
	create("dependent", "blocker")
	// The blocker's dispatch ran here: its session points at a Workspace
	// whose branch is the stack base.
	session, err := fixture.service.Dispatch(DispatchOptions{TicketRef: "blocker", Mode: domain.DispatchModeAgent})
	if err != nil {
		t.Fatalf("dispatch blocker: %v", err)
	}
	if err := store.NewWorkspaceStore(fixture.state).Save(domain.Workspace{
		Version: domain.WorkspaceVersion, ID: session.WorkspaceID, Name: "blocker",
		TemplateName: "product", Status: domain.WorkspaceActive,
		Repositories: []domain.WorkspaceRepository{{Name: "api", Branch: "twt/blocker-branch"}},
	}); err != nil {
		t.Fatal(err)
	}
	// The worker completes with a pull request: the blocker is in review.
	if _, err := fixture.tickets.CompleteWork("blocker", session.Claimant, domain.TicketReadyForHuman,
		[]string{"https://origin.cursor.com/acme/api/pull/7"}, false); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestDispatchStacksOnTheBlockersBranch(t *testing.T) {
	fixture := stackedFixture(t)
	session, err := fixture.service.Dispatch(DispatchOptions{TicketRef: "dependent", Mode: domain.DispatchModeAgent})
	if err != nil {
		t.Fatalf("stacked dispatch: %v", err)
	}
	if session.StackBase != "blocker@twt/blocker-branch" {
		t.Fatalf("session StackBase = %q", session.StackBase)
	}
	request := fixture.launcher.launchCalls[len(fixture.launcher.launchCalls)-1]
	if request.BaseRef != "twt/blocker-branch" {
		t.Fatalf("launch BaseRef = %q", request.BaseRef)
	}
	prompt := session.PromptSnapshot
	for _, want := range []string{
		"STACKED on an unmerged parent",
		"https://origin.cursor.com/acme/api/pull/7",
		"--stack-on https://origin.cursor.com/acme/api/pull/7 --base twt/blocker-branch",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("stacked prompt lacks %q:\n%s", want, prompt)
		}
	}
	claimed, err := fixture.tickets.Resolve("dependent")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ClaimedBy != session.Claimant || claimed.StackBase != "blocker@twt/blocker-branch" {
		t.Fatalf("claimed dependent = %+v", claimed)
	}
}

func TestDispatchWithoutStackingStaysGated(t *testing.T) {
	fixture := stackedFixture(t)
	template, err := fixture.service.options.Templates.Load("product")
	if err != nil {
		t.Fatal(err)
	}
	template.LocalDispatch.Stacking = false
	if err := fixture.service.options.Templates.Save(template); err != nil {
		t.Fatal(err)
	}
	_, err = fixture.service.Dispatch(DispatchOptions{TicketRef: "dependent", Mode: domain.DispatchModeAgent})
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("unstacked dispatch of a blocked ticket = %v, want precondition_failed", err)
	}
}
