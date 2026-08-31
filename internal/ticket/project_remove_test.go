package ticket

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestRemoveProjectDeletesFilesAndFreesTheName(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("change-monitor", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WriteProjectPlan("change-monitor", "# plan\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{
		Title: "Open work", Project: "change-monitor", Status: domain.TicketReadyForAgent, Priority: 1,
	}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{
		Title: "Shipped work", Project: "change-monitor", Status: domain.TicketDone, Priority: -1,
	}, false); err != nil {
		t.Fatal(err)
	}
	before, err := service.AllProjects()
	if err != nil || len(before) != 1 {
		t.Fatalf("AllProjects before = %+v, %v", before, err)
	}
	plan, err := service.PlanProjectRemoval("change-monitor")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Tickets) != 2 {
		t.Fatalf("plan tickets = %v", plan.Tickets)
	}
	if _, err := os.Stat(filepath.Join(home, "change-monitor", "plan.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RemoveProject("change-monitor", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "change-monitor", "index.md")); err != nil {
		t.Fatal("dry-run removed the Project")
	}
	if _, err := service.RemoveProject("change-monitor", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "change-monitor")); !os.IsNotExist(err) {
		t.Fatalf("project directory after remove = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "closed", "change-monitor")); !os.IsNotExist(err) {
		t.Fatalf("closed project directory after remove = %v", err)
	}
	if _, err := service.CreateProject("change-monitor", false); err != nil {
		t.Fatalf("recreate: %v", err)
	}
}

func TestRemoveProjectWorksOnAClosedProject(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.CreateProject("change-monitor", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CloseProject("change-monitor", false, false); err != nil {
		t.Fatal(err)
	}
	all, err := service.AllProjects()
	if err != nil || len(all) != 1 || !all[0].Closed {
		t.Fatalf("AllProjects after close = %+v, %v", all, err)
	}
	active, err := service.Projects()
	if err != nil || len(active) != 0 {
		t.Fatalf("Projects after close = %+v, %v", active, err)
	}
	if _, err := service.RemoveProject("change-monitor", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Project("change-monitor"); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("closed Project after remove = %v", err)
	}
}

func TestRemoveProjectMissingIsNotFound(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.RemoveProject("missing", false); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("missing Project = %v, want not_found", err)
	}
}

func TestRemoveProjectKeepsOtherProjects(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("keep", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateProject("drop", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{
		Title: "Keep me", Project: "keep", Status: domain.TicketReadyForAgent, Priority: 1,
	}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RemoveProject("drop", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Project("keep"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "keep")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resolve("keep-me"); err != nil {
		t.Fatalf("other Project Ticket = %v", err)
	}
}

func TestAllProjectsIncludesClosed(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.CreateProject("change-monitor", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CloseProject("change-monitor", false, false); err != nil {
		t.Fatal(err)
	}
	all, err := service.AllProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "change-monitor" || !all[0].Closed {
		t.Fatalf("AllProjects = %+v", all)
	}
}
