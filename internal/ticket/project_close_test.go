package ticket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestCloseProjectRequiresForceForOpenTickets(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("change-monitor", false); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(home, "change-monitor", "open.md"),
		fixture{title: "Open", status: "ready-for-agent"}.content())

	_, err := service.CloseProject("change-monitor", false, false)
	if clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("CloseProject without force = %v, want precondition_failed", err)
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "--force") {
		t.Fatalf("CloseProject hint = %q, want --force", hint)
	}
	project, projectErr := service.Project("change-monitor")
	if projectErr != nil || project.Closed {
		t.Fatalf("Project after refused close = %+v, %v", project, projectErr)
	}
	if _, statErr := os.Stat(filepath.Join(home, "change-monitor", "open.md")); statErr != nil {
		t.Fatalf("refused close moved the Ticket: %v", statErr)
	}
}

func TestCloseProjectSetsOpenTicketsToWontfixAndClearsWorkState(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("change-monitor", false); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(home, "change-monitor", "claimed.md"),
		"---\ntitle: Claimed\nstatus: ready-for-agent\npriority: 2\nclaimed_by: worker\nclaimed_at: 2026-08-20\ntwt_workspace_id: workspace-one\n---\n")
	writeFixture(t, filepath.Join(home, "change-monitor", "triage.md"),
		fixture{title: "Triage", status: "needs-triage"}.content())
	if _, err := service.Create(CreateRequest{
		Title: "Done", Project: "change-monitor", Status: domain.TicketDone, Priority: -1,
	}, false); err != nil {
		t.Fatal(err)
	}

	result, err := service.CloseProject("change-monitor", true, false)
	if err != nil {
		t.Fatalf("CloseProject: %v", err)
	}
	if result.Project.Name != "change-monitor" || !result.Project.Closed {
		t.Fatalf("CloseProject Project = %+v", result.Project)
	}
	if strings.Join(result.WontfixTickets, ",") != "claimed,triage" {
		t.Fatalf("CloseProject wontfix Tickets = %v", result.WontfixTickets)
	}
	for _, slug := range result.WontfixTickets {
		ticket, resolveErr := service.Resolve(slug)
		if resolveErr != nil {
			t.Fatalf("Resolve(%s): %v", slug, resolveErr)
		}
		if ticket.Status != domain.TicketWontfix || ticket.ClaimedBy != "" || ticket.WorkspaceID != "" {
			t.Fatalf("closed Ticket %s = %+v", slug, ticket)
		}
		wantPath := filepath.Join(home, closedDirectoryName, "change-monitor", slug+".md")
		if ticket.Path != wantPath {
			t.Fatalf("closed Ticket %s path = %q, want %q", slug, ticket.Path, wantPath)
		}
	}
	projects, err := service.Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Fatalf("Projects includes closed Project: %+v", projects)
	}
	shown, err := service.Project("change-monitor")
	if err != nil || !shown.Closed || !shown.HasIndex || shown.Tickets != 3 {
		t.Fatalf("Project after close = %+v, %v", shown, err)
	}

	retry, err := service.CloseProject("change-monitor", false, false)
	if err != nil || len(retry.WontfixTickets) != 0 || !retry.Project.Closed {
		t.Fatalf("CloseProject retry = %+v, %v", retry, err)
	}
}

func TestCloseProjectDryRunDoesNotChangeFiles(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("change-monitor", false); err != nil {
		t.Fatal(err)
	}
	ticketPath := filepath.Join(home, "change-monitor", "open.md")
	writeFixture(t, ticketPath, fixture{title: "Open", status: "needs-triage"}.content())
	indexPath := filepath.Join(home, "change-monitor", "index.md")
	beforeTicket := readFile(t, ticketPath)
	beforeIndex := readFile(t, indexPath)

	result, err := service.CloseProject("change-monitor", true, true)
	if err != nil {
		t.Fatalf("CloseProject dry run: %v", err)
	}
	if !result.Project.Closed || strings.Join(result.WontfixTickets, ",") != "open" {
		t.Fatalf("CloseProject dry-run result = %+v", result)
	}
	if readFile(t, ticketPath) != beforeTicket || readFile(t, indexPath) != beforeIndex {
		t.Fatal("CloseProject dry run changed a file")
	}
}

func TestClosedProjectRejectsNewWork(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.CreateProject("change-monitor", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{
		Title: "Old work", Project: "change-monitor", Status: domain.TicketDone, Priority: -1,
	}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CloseProject("change-monitor", false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{
		Title: "New work", Project: "change-monitor", Status: domain.TicketNeedsTriage, Priority: -1,
	}, false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("Create in closed Project = %v, want precondition_failed", err)
	}
	if _, err := service.CreateProject("change-monitor", false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("CreateProject on closed Project = %v, want precondition_failed", err)
	}
	if _, err := service.Set("old-work", SetRequest{
		Status: string(domain.TicketReadyForAgent), StatusSet: true,
	}, false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("reopen Ticket in closed Project = %v, want precondition_failed", err)
	}
	if _, err := service.WriteProjectPlan("change-monitor", "# New plan\n", false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("write plan in closed Project = %v, want precondition_failed", err)
	}
	if _, err := service.SetProjectTemplate("change-monitor", "product", false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("set Template on closed Project = %v, want precondition_failed", err)
	}
}
