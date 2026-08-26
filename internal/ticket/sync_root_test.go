package ticket

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSyncRootCarriesSharedFilesBesideTheTickets: with SyncOptions.Root set
// to the twt home, files outside the tickets tree but inside the home (for
// example shared Workspace Templates) ride the same sync rounds.
func TestSyncRootCarriesSharedFilesBesideTheTickets(t *testing.T) {
	env := newSyncEnv(t)
	rootedService := func(clone string, warnings *syncWarnings) *Service {
		service := NewService(Options{
			Home:     filepath.Join(clone, "tickets"),
			StateDir: t.TempDir(),
			Sync:     SyncOptions{Mode: SyncModeGit, Root: clone},
			Logf:     warnings.logf,
		})
		service.now = func() time.Time { return time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC) }
		return service
	}
	serviceA := rootedService(env.cloneA, env.warnA)
	serviceB := rootedService(env.cloneB, env.warnB)

	templates := filepath.Join(env.cloneA, "templates")
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "shared.yaml"), []byte("version: 1\nname: shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, err := serviceA.Sync(false)
	if err != nil {
		t.Fatalf("sync A: %v", err)
	}
	if !status.Enabled || !status.CommittedManualEdits {
		t.Fatalf("sync A did not commit the shared file: %+v", status)
	}
	if _, err := serviceB.Sync(false); err != nil {
		t.Fatalf("sync B: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(env.cloneB, "templates", "shared.yaml"))
	if err != nil {
		t.Fatalf("shared template did not arrive in clone B: %v", err)
	}
	if string(data) != "version: 1\nname: shared\n" {
		t.Fatalf("shared template content = %q", data)
	}
}

// TestSyncRootOutsideTheHomeIsIgnored: a Root that does not contain the
// tickets home falls back to the tickets pathspec.
func TestSyncRootOutsideTheHomeIsIgnored(t *testing.T) {
	env := newSyncEnv(t)
	service := NewService(Options{
		Home:     filepath.Join(env.cloneA, "tickets"),
		StateDir: t.TempDir(),
		Sync:     SyncOptions{Mode: SyncModeGit, Root: t.TempDir()},
		Logf:     env.warnA.logf,
	})
	service.now = func() time.Time { return time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC) }
	if _, err := service.Sync(false); err != nil {
		t.Fatalf("sync with a foreign root: %v", err)
	}
}
