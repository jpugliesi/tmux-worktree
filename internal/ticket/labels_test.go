package ticket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestCreateWritesLabelsWithoutAProject(t *testing.T) {
	service, home := newTestService(t)
	result, err := service.Create(CreateRequest{
		Title:    "Spike the monitor",
		Slug:     "spike-the-monitor",
		Status:   domain.TicketNeedsTriage,
		Priority: -1,
		Labels:   []string{"Change-Monitor", "change-monitor", "dev-env"},
	}, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wantPath := filepath.Join(home, "spike-the-monitor.md")
	if result.Ticket.Path != wantPath || result.Ticket.Project != "" {
		t.Fatalf("created ticket = %+v", result.Ticket)
	}
	if strings.Join(result.Ticket.Labels, ",") != "change-monitor,dev-env" {
		t.Fatalf("Labels = %v", result.Ticket.Labels)
	}
	content := readFile(t, wantPath)
	if !strings.Contains(content, "labels:\n  - change-monitor\n  - dev-env\n") {
		t.Fatalf("created file:\n%s", content)
	}
	if _, err := os.Stat(filepath.Join(home, "change-monitor")); !os.IsNotExist(err) {
		t.Fatal("Create made a Project directory from a label")
	}
}

func TestCreateRejectsAnInvalidLabel(t *testing.T) {
	service, _ := newTestService(t)
	_, err := service.Create(CreateRequest{
		Title:    "Bad label",
		Priority: -1,
		Labels:   []string{"change monitor"},
	}, false)
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("Create invalid label = %v, want invalid_usage", err)
	}
}

func TestListFiltersLabelsAcrossProjectsAndUngrouped(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("origin-pr-ux", false); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	writeFixture(t, filepath.Join(home, "origin-pr-ux", "feature.md"),
		fixture{title: "Feature", status: "needs-triage", labels: []string{"change-monitor", "origin-ui"}}.content())
	writeFixture(t, filepath.Join(home, "spike.md"),
		fixture{title: "Spike", status: "needs-triage", labels: []string{"change-monitor"}}.content())
	writeFixture(t, filepath.Join(home, "other.md"),
		fixture{title: "Other", status: "needs-triage", labels: []string{"dev-env"}}.content())

	listed, err := service.List(ListFilter{Labels: []string{"change-monitor"}})
	if err != nil {
		t.Fatalf("List label: %v", err)
	}
	if slugs := ticketSlugs(listed); slugs != "feature,spike" {
		t.Fatalf("label list = %q", slugs)
	}

	both, err := service.List(ListFilter{Labels: []string{"change-monitor", "origin-ui"}})
	if err != nil {
		t.Fatalf("List AND: %v", err)
	}
	if slugs := ticketSlugs(both); slugs != "feature" {
		t.Fatalf("AND list = %q", slugs)
	}

	ungrouped, err := service.List(ListFilter{ProjectSet: true, Labels: []string{"change-monitor"}})
	if err != nil {
		t.Fatalf("List ungrouped+label: %v", err)
	}
	if slugs := ticketSlugs(ungrouped); slugs != "spike" {
		t.Fatalf("ungrouped label list = %q", slugs)
	}
}

func TestSetReplacesAddsAndRemovesLabelsWithoutMoving(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("origin-pr-ux", false); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	path := filepath.Join(home, "origin-pr-ux", "work.md")
	writeFixture(t, path, fixture{title: "Work", status: "needs-triage", labels: []string{"old-theme"}}.content())

	replaced, err := service.Set("work", SetRequest{Labels: []string{"change-monitor"}, LabelsSet: true}, false)
	if err != nil {
		t.Fatalf("replace labels: %v", err)
	}
	if strings.Join(replaced.Labels, ",") != "change-monitor" || replaced.Path != path {
		t.Fatalf("replaced = %+v", replaced)
	}
	if !strings.Contains(readFile(t, path), "labels:\n  - change-monitor\n") {
		t.Fatalf("replaced file:\n%s", readFile(t, path))
	}

	added, err := service.Set("work", SetRequest{AddLabels: []string{"dev-env"}}, false)
	if err != nil {
		t.Fatalf("add label: %v", err)
	}
	if strings.Join(added.Labels, ",") != "change-monitor,dev-env" || added.Path != path {
		t.Fatalf("added = %+v", added)
	}

	removed, err := service.Set("work", SetRequest{RemoveLabels: []string{"change-monitor"}}, false)
	if err != nil {
		t.Fatalf("remove label: %v", err)
	}
	if strings.Join(removed.Labels, ",") != "dev-env" || removed.Path != path {
		t.Fatalf("removed = %+v", removed)
	}

	cleared, err := service.Set("work", SetRequest{LabelsSet: true}, false)
	if err != nil {
		t.Fatalf("clear labels: %v", err)
	}
	if len(cleared.Labels) != 0 || cleared.Path != path {
		t.Fatalf("cleared = %+v", cleared)
	}
	if !strings.Contains(readFile(t, path), "labels: []\n") {
		t.Fatalf("cleared file:\n%s", readFile(t, path))
	}

	_, err = service.Set("work", SetRequest{
		LabelsSet: true,
		Labels:    []string{"change-monitor"},
		AddLabels: []string{"dev-env"},
	}, false)
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("mixed replace and add = %v, want invalid_usage", err)
	}
	_, err = service.Set("work", SetRequest{
		AddLabels:    []string{"shared"},
		RemoveLabels: []string{"shared"},
	}, false)
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("add and remove same = %v, want invalid_usage", err)
	}
}

func TestSetEmptyProjectUngroupsOpenAndClosedTickets(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("origin-pr-ux", false); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	writeFixture(t, filepath.Join(home, "origin-pr-ux", "open-work.md"),
		fixture{title: "Open work", status: "needs-triage", labels: []string{"change-monitor"}}.content())
	writeFixture(t, filepath.Join(home, "closed", ".twt-closed"), "twt closed tickets\n")
	writeFixture(t, filepath.Join(home, "closed", "origin-pr-ux", "done-work.md"),
		fixture{title: "Done work", status: "done", labels: []string{"change-monitor"}}.content())

	open, err := service.Set("open-work", SetRequest{ProjectSet: true}, false)
	if err != nil {
		t.Fatalf("ungroup open: %v", err)
	}
	wantOpen := filepath.Join(home, "open-work.md")
	if open.Path != wantOpen || open.Project != "" {
		t.Fatalf("ungrouped open = %+v", open)
	}
	if _, err := os.Stat(filepath.Join(home, "origin-pr-ux", "open-work.md")); !os.IsNotExist(err) {
		t.Fatal("open source file still exists")
	}
	content := readFile(t, wantOpen)
	if !strings.Contains(content, "labels:\n  - change-monitor\n") {
		t.Fatalf("ungroup dropped labels:\n%s", content)
	}

	closed, err := service.Set("done-work", SetRequest{ProjectSet: true}, false)
	if err != nil {
		t.Fatalf("ungroup closed: %v", err)
	}
	wantClosed := filepath.Join(home, "closed", "done-work.md")
	if closed.Path != wantClosed || closed.Project != "" {
		t.Fatalf("ungrouped closed = %+v", closed)
	}

	before := readFile(t, wantOpen)
	dry, err := service.Set("open-work", SetRequest{Project: "origin-pr-ux", ProjectSet: true}, true)
	if err != nil {
		t.Fatalf("dry-run regroup: %v", err)
	}
	if dry.Project != "origin-pr-ux" {
		t.Fatalf("dry-run result = %+v", dry)
	}
	if readFile(t, wantOpen) != before {
		t.Fatal("dry-run changed the ungrouped file")
	}
	if _, err := os.Stat(filepath.Join(home, "origin-pr-ux", "open-work.md")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote a Project file")
	}
}

func TestAddRemoveRenameLabelsAcrossTickets(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("origin-pr-ux", false); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	writeFixture(t, filepath.Join(home, "origin-pr-ux", "feature.md"),
		fixture{title: "Feature", status: "needs-triage", labels: []string{"change-monitor"}}.content())
	writeFixture(t, filepath.Join(home, "spike.md"),
		fixture{title: "Spike", status: "needs-triage"}.content())
	writeFixture(t, filepath.Join(home, "closed", ".twt-closed"), "twt closed tickets\n")
	writeFixture(t, filepath.Join(home, "closed", "done.md"),
		fixture{title: "Done", status: "done", labels: []string{"change-monitor"}}.content())

	if _, err := service.AddLabel("change-monitor", nil, false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("AddLabel without tickets = %v, want invalid_usage", err)
	}
	added, err := service.AddLabel("Change-Monitor", []string{"spike"}, false)
	if err != nil {
		t.Fatalf("AddLabel: %v", err)
	}
	if added.Name != "change-monitor" || strings.Join(added.Tickets, ",") != "spike" {
		t.Fatalf("AddLabel result = %+v", added)
	}
	if !strings.Contains(readFile(t, filepath.Join(home, "spike.md")), "change-monitor") {
		t.Fatalf("AddLabel did not write spike:\n%s", readFile(t, filepath.Join(home, "spike.md")))
	}
	if filepath.Dir(mustResolvePath(t, service, "feature")) != filepath.Join(home, "origin-pr-ux") {
		t.Fatal("AddLabel moved a Project Ticket")
	}

	dry, err := service.RenameLabel("change-monitor", "monitor-theme", true)
	if err != nil {
		t.Fatalf("dry-run RenameLabel: %v", err)
	}
	if dry.NewName != "monitor-theme" || strings.Join(dry.Tickets, ",") != "done,feature,spike" {
		t.Fatalf("dry-run RenameLabel = %+v", dry)
	}
	if strings.Contains(readFile(t, filepath.Join(home, "spike.md")), "monitor-theme") {
		t.Fatal("dry-run RenameLabel wrote a file")
	}

	renamed, err := service.RenameLabel("change-monitor", "monitor-theme", false)
	if err != nil {
		t.Fatalf("RenameLabel: %v", err)
	}
	if renamed.Name != "change-monitor" || renamed.NewName != "monitor-theme" {
		t.Fatalf("RenameLabel result = %+v", renamed)
	}
	for _, path := range []string{
		filepath.Join(home, "origin-pr-ux", "feature.md"),
		filepath.Join(home, "spike.md"),
		filepath.Join(home, "closed", "done.md"),
	} {
		content := readFile(t, path)
		if !strings.Contains(content, "monitor-theme") || strings.Contains(content, "change-monitor") {
			t.Fatalf("rename left %s:\n%s", path, content)
		}
	}

	removed, err := service.RemoveLabel("monitor-theme", nil, false)
	if err != nil {
		t.Fatalf("RemoveLabel: %v", err)
	}
	if strings.Join(removed.Tickets, ",") != "done,feature,spike" {
		t.Fatalf("RemoveLabel result = %+v", removed)
	}
	if strings.Contains(readFile(t, filepath.Join(home, "spike.md")), "monitor-theme") {
		t.Fatalf("RemoveLabel left spike:\n%s", readFile(t, filepath.Join(home, "spike.md")))
	}
	if _, err := service.RemoveLabel("monitor-theme", nil, false); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("RemoveLabel missing = %v, want not_found", err)
	}
}

func mustResolvePath(t *testing.T, service *Service, slug string) string {
	t.Helper()
	ticket, err := service.Resolve(slug)
	if err != nil {
		t.Fatal(err)
	}
	return ticket.Path
}

func ticketSlugs(tickets []domain.Ticket) string {
	slugs := make([]string, 0, len(tickets))
	for _, ticket := range tickets {
		slugs = append(slugs, ticket.Slug)
	}
	return strings.Join(slugs, ",")
}
