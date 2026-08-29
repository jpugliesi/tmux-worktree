package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// One daemon pass refreshes every Workspace Template pool. A Workspace
// Template without repositories is skipped, and a failing one does not stop
// the others.
func TestDaemonRunRefreshesEveryTemplatePool(t *testing.T) {
	options := maintenanceOptions(t)
	source := filepath.Join(t.TempDir(), "source")
	initGitRepository(t, source)
	writeTemplateFile(t, options.ConfigDir, domain.Template{
		Version: domain.TemplateVersion, Name: "app",
		Repositories: []domain.RepositorySpec{{Name: "app", Clone: domain.CloneSpec{URL: source}}},
	})
	writeTemplateFile(t, options.ConfigDir, domain.Template{Version: domain.TemplateVersion, Name: "empty"})
	writeTemplateFile(t, options.ConfigDir, domain.Template{
		Version: domain.TemplateVersion, Name: "broken",
		Repositories: []domain.RepositorySpec{{Name: "app", Clone: domain.CloneSpec{URL: filepath.Join(t.TempDir(), "missing.git")}}},
	})

	_, stderr, err := runCLI(t, options, "daemon", "run")
	if err == nil || !strings.Contains(err.Error(), "failed for 1 Workspace Templates") {
		t.Fatalf("daemon run with one broken Workspace Template error = %v", err)
	}
	if !strings.Contains(stderr, `"broken" failed`) {
		t.Fatalf("daemon run stderr does not report the broken Workspace Template:\n%s", stderr)
	}
	environments, listErr := store.NewEnvironmentStore(options.StateDir).List()
	if listErr != nil {
		t.Fatal(listErr)
	}
	ready := 0
	for _, environment := range environments {
		if environment.TemplateName == "app" && environment.Status == domain.EnvironmentReady {
			ready++
		}
		if environment.TemplateName == "empty" {
			t.Fatalf("daemon run prepared an environment for a Workspace Template without repositories: %+v", environment)
		}
	}
	if ready != 1 {
		t.Fatalf("daemon run prepared %d ready environments for template \"app\", want 1", ready)
	}
}

// A dry-run install validates without a plist write or a launchctl call.
func TestDaemonInstallDryRunWritesNothing(t *testing.T) {
	options := maintenanceOptions(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", "com.twt.pool-refresh.plist")
	before, statErr := os.Stat(plist)
	output, _, err := runCLI(t, options, "daemon", "install", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("daemon install --dry-run error = %v", err)
	}
	if !strings.Contains(output, `"status":"valid"`) {
		t.Fatalf("daemon install --dry-run output = %s", output)
	}
	after, afterErr := os.Stat(plist)
	if (statErr == nil) != (afterErr == nil) {
		t.Fatalf("dry-run changed the plist presence: before %v, after %v", statErr, afterErr)
	}
	if statErr == nil && !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("dry-run rewrote the plist")
	}
}

func TestDaemonInstallRejectsAShortInterval(t *testing.T) {
	options := maintenanceOptions(t)
	_, _, err := runCLI(t, options, "daemon", "install", "--interval", "10s", "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "one minute or more") {
		t.Fatalf("daemon install with a short interval error = %v", err)
	}
}
