package ticket

import (
	"path/filepath"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestReservedProjectFilesAreNeverTickets(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateProject("core", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{Title: "Real ticket", Project: "core", Status: domain.TicketReadyForAgent}, false); err != nil {
		t.Fatal(err)
	}
	// A plan.md beside the tickets must be invisible to every walker.
	writeFixture(t, filepath.Join(home, "core", "plan.md"), "# Core Plan\n")
	writeFixture(t, filepath.Join(home, "plan.md"), "# Root Plan\n")

	tickets, err := service.List(ListFilter{Project: "core", ProjectSet: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tickets) != 1 || tickets[0].Slug != "real-ticket" {
		t.Fatalf("List = %+v, want only real-ticket", tickets)
	}
	project, err := service.Project("core")
	if err != nil {
		t.Fatal(err)
	}
	if project.Tickets != 1 {
		t.Fatalf("project ticket count = %d, want 1", project.Tickets)
	}
	report, err := service.Doctor()
	if err != nil {
		t.Fatal(err)
	}
	if !report.Healthy || report.TicketCount != 1 {
		t.Fatalf("doctor = healthy %v count %d, want healthy 1 (issues: %+v)", report.Healthy, report.TicketCount, report.Issues)
	}
}

func TestReservedSlugsAreRejectedAtCreate(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Plan", "Index"} {
		if _, err := service.Create(CreateRequest{Title: title}, false); clierr.CodeOf(err) != clierr.InvalidUsage {
			t.Fatalf("create %q error = %v, want invalid_usage", title, err)
		}
	}
	if _, err := service.Create(CreateRequest{Title: "Fine", Slug: "plan"}, false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatal("explicit reserved slug accepted")
	}
}
