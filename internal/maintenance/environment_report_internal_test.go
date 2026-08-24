package maintenance

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestEnvironmentReportDoesNotMeasureDirectorySizes(t *testing.T) {
	root := t.TempDir()
	template := domain.Template{
		Version: domain.TemplateVersion,
		Name:    "example",
		Repositories: []domain.RepositorySpec{{
			Name: "app", Clone: domain.CloneSpec{URL: "https://example.com/app.git"},
		}},
	}
	digest, err := store.EnvironmentDigest(template)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	environment := domain.PreparedEnvironment{
		Version: domain.PreparedEnvironmentVersion, FormatVersion: domain.PreparationFormatVersion,
		ID: "failed-environment", TemplateName: template.Name, TemplateDigest: digest, TemplateSnapshot: template,
		Status: domain.EnvironmentFailed, Root: filepath.Join(root, "environment"), QueueToken: "queue-token",
		QueuedAt: now, CreatedAt: now, UpdatedAt: now, Failure: "clone failed",
	}
	if err := store.NewEnvironmentStore(filepath.Join(root, "state")).Save(environment); err != nil {
		t.Fatal(err)
	}

	service := NewService(filepath.Join(root, "config"), filepath.Join(root, "state"), filepath.Join(root, "data"), "")
	measurements := 0
	service.directoryBytes = func(string) (int64, string) {
		measurements++
		return 1024, ""
	}
	report, err := service.EnvironmentReport()
	if err != nil {
		t.Fatal(err)
	}
	if len(report) != 1 || report[0].Bytes != nil {
		t.Fatalf("metadata-only environment report = %+v", report)
	}
	if measurements != 0 {
		t.Fatalf("EnvironmentReport measured %d directories, want zero", measurements)
	}
}

func TestMeasureEnvironmentSizesSkipsWorkspaceBoundRoots(t *testing.T) {
	service := &Service{}
	var measured []string
	service.directoryBytes = func(root string) (int64, string) {
		measured = append(measured, root)
		if root == "/failed" {
			return 0, "cannot read failed root"
		}
		return 2048, ""
	}
	report := []EnvironmentInfo{
		{ID: "ready", root: "/ready", environmentStatus: domain.EnvironmentReady},
		{ID: "failed", root: "/failed", environmentStatus: domain.EnvironmentFailed},
		{ID: "claiming", root: "/claiming", environmentStatus: domain.EnvironmentClaiming},
		{ID: "claimed", root: "/claimed", environmentStatus: domain.EnvironmentClaimed},
	}

	service.MeasureEnvironmentSizes(report)

	if len(measured) != 2 || measured[0] != "/ready" || measured[1] != "/failed" {
		t.Fatalf("measured roots = %v, want ready and failed", measured)
	}
	if report[0].Bytes == nil || *report[0].Bytes != 2048 || report[0].SizeWarning != "" {
		t.Fatalf("ready size = %+v", report[0])
	}
	if report[1].Bytes != nil || report[1].SizeWarning != "cannot read failed root" {
		t.Fatalf("failed size = %+v", report[1])
	}
	for _, info := range report[2:] {
		if info.Bytes != nil || info.SizeWarning != "" {
			t.Fatalf("Workspace-bound size = %+v", info)
		}
	}
}
