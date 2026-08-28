package ticket

import (
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

func planFixture(t *testing.T) *Service {
	t.Helper()
	service, _ := newTestService(t)
	if _, err := service.Init(false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateProject("core", false); err != nil {
		t.Fatal(err)
	}
	return service
}

func TestProjectPlanLifecycle(t *testing.T) {
	service := planFixture(t)
	if _, err := service.ProjectPlan("core"); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("missing plan error = %v, want not_found", err)
	}
	created, err := service.WriteProjectPlan("core", "# core Plan\n\nShip it.\n", false)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !created.Created {
		t.Fatal("write did not report creation")
	}
	shown, err := service.ProjectPlan("core")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown.Content, "# core Plan") || !strings.Contains(shown.Content, "Ship it.") {
		t.Fatalf("plan content:\n%s", shown.Content)
	}
	if _, err := service.WriteProjectPlan("core", "# core Plan\n\nRevised.\n", false); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	shown, err = service.ProjectPlan("core")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown.Content, "Revised.") || shown.UpdatedAt == "" {
		t.Fatalf("edited plan = %+v", shown)
	}
	if _, err := service.WriteProjectPlan("core", "  ", false); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatal("empty plan accepted")
	}
	if _, err := service.WriteProjectPlan("missing", "x", false); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatal("plan write on a missing project accepted")
	}
	// The upsert creates the file for a project without one.
	if _, err := service.CreateProject("other", false); err != nil {
		t.Fatal(err)
	}
	upserted, err := service.WriteProjectPlan("other", "# Other\n", false)
	if err != nil {
		t.Fatal(err)
	}
	if !upserted.Created {
		t.Fatal("upsert did not create plan.md")
	}
}

func TestProjectInfoReportsThePlan(t *testing.T) {
	service := planFixture(t)
	project, err := service.Project("core")
	if err != nil {
		t.Fatal(err)
	}
	if project.HasPlan {
		t.Fatal("HasPlan true before write")
	}
	if _, err := service.WriteProjectPlan("core", "# core Plan\n\nShip it.\n", false); err != nil {
		t.Fatal(err)
	}
	project, err = service.Project("core")
	if err != nil {
		t.Fatal(err)
	}
	if !project.HasPlan || project.PlanTitle != "core Plan" || project.PlanUpdatedAt == "" {
		t.Fatalf("project plan fields = %+v", project)
	}
	if project.Tickets != 0 {
		t.Fatalf("plan.md counted as a ticket: %d", project.Tickets)
	}
	// Dry-run write creates nothing.
	if _, err := service.CreateProject("other", false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.WriteProjectPlan("other", "# Other\n", true); err != nil {
		t.Fatal(err)
	}
	other, err := service.Project("other")
	if err != nil {
		t.Fatal(err)
	}
	if other.HasPlan {
		t.Fatal("dry-run write created plan.md")
	}
}
