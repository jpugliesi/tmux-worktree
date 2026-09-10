package ticket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestPauseProjectHidesFromDefaultListAndStaysWritable(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("learn", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(CreateRequest{
		Title: "Open work", Slug: "open-work", Project: "learn",
		Status: domain.TicketReadyForAgent, Priority: 1,
	}, false); err != nil {
		t.Fatal(err)
	}

	paused, err := service.PauseProject("learn", false)
	if err != nil || !paused.Paused || paused.Closed {
		t.Fatalf("PauseProject() = %+v, %v", paused, err)
	}
	if !strings.Contains(readFile(t, filepath.Join(home, "learn", "index.md")), "twt_paused: true") {
		t.Fatal("pause did not write twt_paused")
	}

	projects, err := service.Projects()
	if err != nil || len(projects) != 0 {
		t.Fatalf("Projects() after pause = %+v, %v", projects, err)
	}
	all, err := service.AllProjects()
	if err != nil || len(all) != 1 || !all[0].Paused || all[0].Name != "learn" {
		t.Fatalf("AllProjects() after pause = %+v, %v", all, err)
	}

	if _, err := service.Create(CreateRequest{
		Title: "More work", Slug: "more-work", Project: "learn",
		Status: domain.TicketNeedsTriage, Priority: 2,
	}, false); err != nil {
		t.Fatalf("Create in paused Project: %v", err)
	}
	hidden, err := service.List(ListFilter{})
	if err != nil || len(hidden) != 0 {
		t.Fatalf("unscoped List after pause = %+v, %v", hidden, err)
	}
	scoped, err := service.List(ListFilter{Project: "learn", ProjectSet: true})
	if err != nil || len(scoped) != 2 {
		t.Fatalf("scoped List after pause = %+v, %v", scoped, err)
	}
	included, err := service.List(ListFilter{IncludePaused: true})
	if err != nil || len(included) != 2 {
		t.Fatalf("IncludePaused List = %+v, %v", included, err)
	}

	resumed, err := service.ResumeProject("learn", false)
	if err != nil || resumed.Paused {
		t.Fatalf("ResumeProject() = %+v, %v", resumed, err)
	}
	projects, err = service.Projects()
	if err != nil || len(projects) != 1 || projects[0].Name != "learn" {
		t.Fatalf("Projects() after resume = %+v, %v", projects, err)
	}
}

func TestPauseAndResumeAreIdempotentAndRejectClosed(t *testing.T) {
	service, _ := newTestService(t)
	if _, err := service.CreateProject("learn", false); err != nil {
		t.Fatal(err)
	}
	first, err := service.PauseProject("learn", false)
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.PauseProject("learn", false)
	if err != nil || !again.Paused || again.Name != first.Name {
		t.Fatalf("pause retry = %+v, %v", again, err)
	}
	active, err := service.ResumeProject("learn", false)
	if err != nil || active.Paused {
		t.Fatal(err)
	}
	still, err := service.ResumeProject("learn", false)
	if err != nil || still.Paused {
		t.Fatalf("resume retry = %+v, %v", still, err)
	}

	if _, err := service.CloseProject("learn", false, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PauseProject("learn", false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("pause closed = %v", err)
	}
	if _, err := service.ResumeProject("learn", false); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("resume closed = %v", err)
	}
}

func TestPauseProjectDryRunDoesNotChangeFiles(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("learn", false); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(home, "learn", "index.md")
	before := readFile(t, indexPath)
	got, err := service.PauseProject("learn", true)
	if err != nil || !got.Paused {
		t.Fatalf("PauseProject dry run = %+v, %v", got, err)
	}
	if readFile(t, indexPath) != before {
		t.Fatal("PauseProject dry run changed the index")
	}
}

func TestCloseProjectClearsPaused(t *testing.T) {
	service, home := newTestService(t)
	if _, err := service.CreateProject("learn", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PauseProject("learn", false); err != nil {
		t.Fatal(err)
	}
	result, err := service.CloseProject("learn", false, false)
	if err != nil || !result.Project.Closed || result.Project.Paused {
		t.Fatalf("close paused = %+v, %v", result, err)
	}
	shown, err := service.Project("learn")
	if err != nil || shown.Paused || !shown.Closed {
		t.Fatalf("Project after close = %+v, %v", shown, err)
	}
	index := readFile(t, filepath.Join(home, "learn", "index.md"))
	if strings.Contains(index, "twt_paused: true") {
		t.Fatalf("close kept twt_paused:\n%s", index)
	}
	if _, err := os.Stat(filepath.Join(home, "learn", "index.md")); err != nil {
		t.Fatal(err)
	}
}
