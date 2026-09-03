package maintenance_test

import (
	"fmt"
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

// markTwtCache writes the twt ownership marker that makes doctor treat one
// directory as a Repository Cache.
func markTwtCache(t *testing.T, cachePath string) {
	t.Helper()
	marker := filepath.Join(cachePath, "twt-ownership.json")
	if err := os.WriteFile(marker, []byte(`{"owner":"twt","url":"https://example.com/app.git"}`), 0o600); err != nil {
		t.Fatal(err)
	}
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

func TestDoctorWarnsAboutAnEmptyPreparedPool(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := `version: 1
name: example
repositories:
  - name: app
    clone:
      url: https://example.com/app.git
`
	if err := os.WriteFile(filepath.Join(configDir, "templates", "example.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	service := maintenance.NewService(configDir, filepath.Join(root, "state"), filepath.Join(root, "data"), "")
	report := service.Doctor()
	check := findCheck(report, "pool:example")
	if check == nil {
		t.Fatalf("doctor has no pool check: %+v", report.Checks)
	}
	if check.Status != "warn" || !strings.Contains(check.Message, "twt templates prepare example") {
		t.Fatalf("pool check = %+v, want a warning with the prepare hint", *check)
	}
}

func TestDoctorWarnsAboutABloatedRepositoryCache(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	packDirectory := filepath.Join(dataDir, "caches", "app-abc.git", "objects", "pack")
	if err := os.MkdirAll(packDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	markTwtCache(t, filepath.Join(dataDir, "caches", "app-abc.git"))
	for index := 0; index < 65; index++ {
		name := filepath.Join(packDirectory, "pack-"+strings.Repeat("a", 3)+string(rune('a'+index%26))+string(rune('a'+index/26))+".pack")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(packDirectory, "tmp_pack_x"), make([]byte, 2_000_000), 0o644); err != nil {
		t.Fatal(err)
	}
	service := maintenance.NewService(filepath.Join(root, "config"), filepath.Join(root, "state"), dataDir, "")
	report := service.Doctor()
	check := findCheck(report, "cache:app-abc.git")
	if check == nil {
		t.Fatalf("doctor has no cache check: %+v", report.Checks)
	}
	if check.Status != "warn" || !strings.Contains(check.Message, "65 pack files") || !strings.Contains(check.Message, "2 MB") {
		t.Fatalf("cache check = %+v", *check)
	}
}

// A Repository Cache with an unpacked ref store slows every Git command that
// enumerates refs, long before the cache looks large.
func TestDoctorWarnsAboutAnUnpackedRefStore(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	refDirectory := filepath.Join(dataDir, "caches", "app-abc.git", "refs", "remotes", "origin")
	if err := os.MkdirAll(refDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	markTwtCache(t, filepath.Join(dataDir, "caches", "app-abc.git"))
	for index := 0; index < 1000; index++ {
		name := filepath.Join(refDirectory, fmt.Sprintf("branch-%d", index))
		if err := os.WriteFile(name, []byte(strings.Repeat("a", 40)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	service := maintenance.NewService(filepath.Join(root, "config"), filepath.Join(root, "state"), dataDir, "")

	report := service.Doctor()

	check := findCheck(report, "cache:app-abc.git")
	if check == nil {
		t.Fatalf("doctor has no cache check: %+v", report.Checks)
	}
	if check.Status != "warn" || !strings.Contains(check.Message, "loose refs") {
		t.Fatalf("cache check = %+v, want a loose-ref warning", *check)
	}
}

// A packed ref store must not raise the warning.
func TestDoctorAcceptsAPackedRefStore(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	cache := filepath.Join(dataDir, "caches", "app-abc.git")
	if err := os.MkdirAll(filepath.Join(cache, "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	markTwtCache(t, cache)
	if err := os.WriteFile(filepath.Join(cache, "packed-refs"), []byte("# pack-refs with: peeled\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	service := maintenance.NewService(filepath.Join(root, "config"), filepath.Join(root, "state"), dataDir, "")

	report := service.Doctor()

	if check := findCheck(report, "cache:app-abc.git"); check != nil {
		t.Fatalf("doctor warned about a compact cache: %+v", *check)
	}
}
