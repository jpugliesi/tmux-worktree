package maintenance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/maintenance"
	"github.com/jpugliesi/tmux-worktree/skills"
)

// newService builds a maintenance service on empty twt directories.
func newService(t *testing.T) *maintenance.Service {
	t.Helper()
	root := t.TempDir()
	return maintenance.NewService(filepath.Join(root, "config"), filepath.Join(root, "state"), filepath.Join(root, "data"), "")
}

// findCheck returns the doctor check with one name, or nil.
func findCheck(report maintenance.DoctorReport, name string) *maintenance.Check {
	for index := range report.Checks {
		if report.Checks[index].Name == name {
			return &report.Checks[index]
		}
	}
	return nil
}

func TestDoctorSkillCheckReportsDrift(t *testing.T) {
	tests := []struct {
		name    string
		content string
		status  string
	}{
		{name: "same version", content: skills.Stamped("1.2.3"), status: "pass"},
		{name: "same version changed content", content: strings.Replace(skills.Stamped("1.2.3"), "Use `twt` as the state owner.", "Use direct state files.", 1), status: "warn"},
		{name: "other version", content: skills.Stamped("0.9.0"), status: "warn"},
		{name: "no version", content: skills.Canonical(), status: "warn"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			paths := skills.UserPaths(home)
			if len(paths) != 3 {
				t.Fatalf("UserPaths = %v, want three paths", paths)
			}
			if err := os.MkdirAll(filepath.Dir(paths[0]), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths[0], []byte(test.content), 0o644); err != nil {
				t.Fatal(err)
			}
			report := newService(t).WithSkillCheck("1.2.3", paths).Doctor()
			check := findCheck(report, "skills")
			if check == nil {
				t.Fatalf("doctor has no skills check: %+v", report.Checks)
			}
			if check.Status != test.status {
				t.Fatalf("skills check = %+v, want status %q", *check, test.status)
			}
			if test.status == "warn" && !strings.Contains(check.Message, "Run 'twt skills install' to update the twt skill.") {
				t.Fatalf("warning message has no repair step: %q", check.Message)
			}
			if !report.Healthy {
				t.Fatalf("the skills check made doctor unhealthy: %+v", report.Checks)
			}
		})
	}
}

func TestDoctorSkipsTheSkillCheckWithoutAnInstalledSkill(t *testing.T) {
	home := t.TempDir()
	report := newService(t).WithSkillCheck("1.2.3", skills.UserPaths(home)).Doctor()
	if check := findCheck(report, "skills"); check != nil {
		t.Fatalf("doctor reported the skills check with no installed skill: %+v", *check)
	}
	report = newService(t).Doctor()
	if check := findCheck(report, "skills"); check != nil {
		t.Fatalf("doctor reported the skills check with no request: %+v", *check)
	}
}
