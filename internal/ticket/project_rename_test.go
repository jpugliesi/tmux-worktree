package ticket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestRenameProjectMovesFilesAndHealsFrontmatter(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("old-name", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WriteProjectPlan("old-name", "# plan\n", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{
		Title: "Open work", Project: "old-name", Status: domain.TicketReadyForAgent, Priority: 1,
	}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{
		Title: "Shipped work", Project: "old-name", Status: domain.TicketDone, Priority: -1,
	}, false); err != nil {
		t.Fatal(err)
	}

	dry, err := service.RenameProject("old-name", "new-name", true)
	if err != nil {
		t.Fatal(err)
	}
	if dry.Name != "new-name" {
		t.Fatalf("dry-run name = %q", dry.Name)
	}
	if _, err := os.Stat(filepath.Join(home, "old-name", "open-work.md")); err != nil {
		t.Fatal("dry-run moved a Ticket")
	}

	project, err := service.RenameProject("old-name", "new-name", false)
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "new-name" || project.Tickets != 2 {
		t.Fatalf("renamed Project = %+v", project)
	}
	if _, err := service.Project("old-name"); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("old Project after rename = %v, want not_found", err)
	}
	ticket, err := service.Resolve("open-work")
	if err != nil || ticket.Project != "new-name" {
		t.Fatalf("open Ticket after rename = %+v, %v", ticket, err)
	}
	closed, err := service.Resolve("shipped-work")
	if err != nil || closed.Project != "new-name" {
		t.Fatalf("closed Ticket after rename = %+v, %v", closed, err)
	}
	content := readFile(t, filepath.Join(home, "new-name", "open-work.md"))
	if !strings.Contains(content, "project: new-name") {
		t.Fatalf("healed frontmatter = %q", content)
	}
	if _, err := os.Stat(filepath.Join(home, "closed", "new-name", "shipped-work.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "new-name", "plan.md")); err != nil {
		t.Fatal(err)
	}
}

func TestRenameProjectWorksOnAClosedProject(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.CreateProject("old-name", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CloseProject("old-name", false, false); err != nil {
		t.Fatal(err)
	}
	project, err := service.RenameProject("old-name", "new-name", false)
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "new-name" || !project.Closed {
		t.Fatalf("renamed closed Project = %+v", project)
	}
	if _, err := service.Project("old-name"); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("old closed Project = %v, want not_found", err)
	}
}

func TestRenameProjectSameNameIsNoOp(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("same", false); err != nil {
		t.Fatal(err)
	}
	project, err := service.RenameProject("same", "same", false)
	if err != nil {
		t.Fatal(err)
	}
	if project.Name != "same" {
		t.Fatalf("same-name rename = %+v", project)
	}
	if _, err := os.Stat(filepath.Join(home, "same", "index.md")); err != nil {
		t.Fatal(err)
	}
}

func TestRenameProjectRejectsReservedAndExistingNames(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.CreateProject("old-name", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateProject("taken", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RenameProject("old-name", "closed", false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("reserved name = %v, want invalid_usage", err)
	}
	if _, err := service.RenameProject("old-name", "taken", false); clierr.CodeOf(err) != clierr.AlreadyExists {
		t.Fatalf("existing name = %v, want already_exists", err)
	}
	if _, err := service.RenameProject("missing", "new-name", false); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("missing Project = %v, want not_found", err)
	}
}
