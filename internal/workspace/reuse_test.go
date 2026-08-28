package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestReleaseRequiresForceAndPreservesIgnoredFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	if err := os.WriteFile(filepath.Join(source, ".gitignore"), []byte("secret.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(source, "git", "add", ".gitignore"); err != nil {
		t.Fatal(err)
	}
	if err := run(source, "git", "commit", "-qm", "ignore local secret"); err != nil {
		t.Fatal(err)
	}
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "example",
		Repositories: []domain.RepositorySpec{{Name: "app", Clone: domain.CloneSpec{URL: source}}},
	}
	socket := fmt.Sprintf("twt-release-force-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})

	workspace, err := service.Create("dirty", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	repositoryPath := workspace.Repositories[0].Path
	if err := os.WriteFile(filepath.Join(repositoryPath, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryPath, "untracked.txt"), []byte("discard\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryPath, "secret.env"), []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plan, err := service.InspectRelease(workspace.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Dirty || plan.Fingerprint == "" {
		t.Fatalf("release plan = %+v", plan)
	}
	if _, err := service.Release(workspace.ID, "", ReleaseOptions{ExpectedFingerprint: plan.Fingerprint}); clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("Release() without force error = %v", err)
	}
	if _, err := service.Release(workspace.ID, "", ReleaseOptions{Force: true, ExpectedFingerprint: plan.Fingerprint}); err != nil {
		t.Fatalf("Release() with force: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repositoryPath, "secret.env")); err != nil {
		t.Fatalf("release removed an ignored file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repositoryPath, "untracked.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("release kept a nonignored untracked file: %v", err)
	}
	if status := testGitOutput(t, repositoryPath, "status", "--porcelain", "--ignore-submodules=none"); status != "" {
		t.Fatalf("released worktree is dirty: %q", status)
	}
}

func TestReleaseRunsTheRecycleCommandBeforeReuse(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	if err := os.WriteFile(filepath.Join(source, ".gitignore"), []byte("secret.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(source, "git", "add", ".gitignore"); err != nil {
		t.Fatal(err)
	}
	if err := run(source, "git", "commit", "-qm", "ignore secret"); err != nil {
		t.Fatal(err)
	}
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "example",
		Repositories: []domain.RepositorySpec{{
			Name: "app", Clone: domain.CloneSpec{URL: source},
			Recycle: &domain.RecycleSpec{Command: []string{"sh", "-c", "rm -f secret.env"}},
		}},
	}
	socket := fmt.Sprintf("twt-recycle-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})

	workspace, err := service.Create("recycle", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(workspace.Repositories[0].Path, "secret.env")
	if err := os.WriteFile(secret, []byte("remove\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Release(workspace.ID, "", ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(secret); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recycle command kept the ignored secret: %v", err)
	}
}

func TestOpenMaterializesAReleasedWorkspaceOnItsSavedBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	initLog := filepath.Join(t.TempDir(), "workspace-init.log")
	t.Setenv("TWT_TEST_WORKSPACE_INIT_LOG", initLog)
	template := domain.Template{
		Version: domain.TemplateVersion,
		Name:    "example",
		Repositories: []domain.RepositorySpec{{
			Name: "app", Clone: domain.CloneSpec{URL: source},
		}},
		Initialize: &domain.InitializeSpec{
			Command:          []string{"sh", "-c", "printf 'initialized\\n' >> \"$TWT_TEST_WORKSPACE_INIT_LOG\""},
			WorkingDirectory: "app",
		},
	}
	socket := fmt.Sprintf("twt-reopen-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})

	created, err := service.Create("first", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(created.Repositories[0].Path, "WORK.md"), []byte("saved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "WORK.md"}, {"commit", "-qm", "save work"}} {
		if err := run(created.Repositories[0].Path, "git", args...); err != nil {
			t.Fatal(err)
		}
	}
	savedHead := testGitOutput(t, created.Repositories[0].Path, "rev-parse", "HEAD")
	if _, err := service.Release(created.ID, "", ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}

	opened, err := service.Open(created.ID)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if opened.Status != domain.WorkspaceActive || !opened.Materialized || opened.EnvironmentID == "" || opened.Root == "" {
		t.Fatalf("opened Workspace = %+v", opened)
	}
	if branch := testGitOutput(t, opened.Repositories[0].Path, "branch", "--show-current"); branch != created.Repositories[0].Branch {
		t.Fatalf("opened branch = %q, want %q", branch, created.Repositories[0].Branch)
	}
	if head := testGitOutput(t, opened.Repositories[0].Path, "rev-parse", "HEAD"); head != savedHead {
		t.Fatalf("opened HEAD = %q, want %q", head, savedHead)
	}
	data, err := os.ReadFile(initLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Fields(string(data))); got != 2 {
		t.Fatalf("Workspace Initialization ran %d times, want 2", got)
	}
}

func TestReleaseMakesThePreparedEnvironmentReadyForAnotherWorkspace(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion,
		Name:    "example",
		Repositories: []domain.RepositorySpec{{
			Name: "app", Clone: domain.CloneSpec{URL: source},
		}},
	}
	socket := fmt.Sprintf("twt-reuse-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})

	first, err := service.Create("first", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	firstRoot := first.Root
	firstBranch := first.Repositories[0].Branch
	result, err := service.Release(first.ID, "", ReleaseOptions{})
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if result.Workspace.Status != domain.WorkspaceArchived || result.Workspace.Materialized {
		t.Fatalf("released Workspace = %+v", result.Workspace)
	}
	if result.Workspace.EnvironmentID != "" || result.Workspace.Root != "" || result.Workspace.Repositories[0].Path != "" {
		t.Fatalf("released Workspace keeps physical ownership: %+v", result.Workspace)
	}

	environment, err := store.NewEnvironmentStore(stateDir).Find(first.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if environment.Status != domain.EnvironmentReady || environment.Assignment != nil {
		t.Fatalf("released Prepared Environment = %+v", environment)
	}
	unavailableSource := source + "-offline"
	if err := os.Rename(source, unavailableSource); err != nil {
		t.Fatal(err)
	}

	second, err := service.Create("second", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	if second.EnvironmentID != first.EnvironmentID || second.Root != firstRoot {
		t.Fatalf("second Workspace used environment %q at %q, want %q at %q", second.EnvironmentID, second.Root, first.EnvironmentID, firstRoot)
	}
	if !second.Materialized {
		t.Fatal("second Workspace is not materialized")
	}
	if exists, err := refExists(second.Repositories[0].CachePath, "refs/heads/"+firstBranch); err != nil || !exists {
		t.Fatalf("first Workspace branch was not preserved: exists=%t error=%v", exists, err)
	}
}

func TestPrepareReleaseFromPaneKeepsOnlyTheCallerUntilTheSessionStops(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "example",
		Repositories: []domain.RepositorySpec{{Name: "app", Clone: domain.CloneSpec{URL: source}}},
	}
	socket := fmt.Sprintf("twt-in-session-release-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})

	created, err := service.Create("inside", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	sourceSession, err := service.OwnedSessionID(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	callerPane := testTmuxOutput(t, socket, "display-message", "-p", "-t", sourceSession, "#{pane_id}")
	testTmuxOutput(t, socket, "split-window", "-d", "-P", "-F", "#{pane_id}", "-t", callerPane, "-c", created.Root)

	prepared, err := service.PrepareReleaseFromPane(created.ID, callerPane, ReleaseOptions{})
	if err != nil {
		t.Fatalf("PrepareReleaseFromPane() error = %v", err)
	}
	if prepared.SourceSessionID != sourceSession {
		t.Fatalf("source session = %q, want %q", prepared.SourceSessionID, sourceSession)
	}
	panes := strings.Fields(testTmuxOutput(t, socket, "list-panes", "-s", "-t", sourceSession, "-F", "#{pane_id}"))
	if len(panes) != 1 || panes[0] != callerPane {
		t.Fatalf("remaining panes = %v, want only %q", panes, callerPane)
	}
	bound, err := store.NewWorkspaceStore(stateDir).Find(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Status != domain.WorkspaceArchived || !bound.Materialized || bound.EnvironmentID != created.EnvironmentID {
		t.Fatalf("prepared Workspace = %+v", bound)
	}
	environment, err := store.NewEnvironmentStore(stateDir).Find(created.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if environment.Status != domain.EnvironmentReleasing || environment.Assignment == nil ||
		environment.Assignment.Phase != domain.EnvironmentAssignmentSessionStopPending ||
		environment.Assignment.SourceSessionID != sourceSession {
		t.Fatalf("prepared Environment = %+v", environment)
	}

	if err := service.Reconcile(); err != nil {
		t.Fatal(err)
	}
	stillBound, err := store.NewWorkspaceStore(stateDir).Find(created.ID)
	if err != nil || !stillBound.Materialized {
		t.Fatalf("Workspace before session stop = %+v, error = %v", stillBound, err)
	}
	if err := service.StopPreparedRelease(prepared); err != nil {
		t.Fatalf("StopPreparedRelease() error = %v", err)
	}
	restarted := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})
	if err := restarted.Reconcile(); err != nil {
		t.Fatal(err)
	}
	released, err := store.NewWorkspaceStore(stateDir).Find(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if released.Materialized || released.EnvironmentID != "" || released.Root != "" || released.Repositories[0].Path != "" {
		t.Fatalf("reconciled Workspace = %+v", released)
	}
	environment, err = store.NewEnvironmentStore(stateDir).Find(created.EnvironmentID)
	if err != nil || environment.Status != domain.EnvironmentReady || environment.Assignment != nil {
		t.Fatalf("reconciled Environment = %+v, error = %v", environment, err)
	}
}

func TestReconcileKeepsAnEnvironmentUnavailableWhenTheCallerChangesItAfterCleanup(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "example",
		Repositories: []domain.RepositorySpec{{Name: "app", Clone: domain.CloneSpec{URL: source}}},
	}
	socket := fmt.Sprintf("twt-release-change-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})

	created, err := service.Create("changed", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	sourceSession, err := service.OwnedSessionID(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	callerPane := testTmuxOutput(t, socket, "display-message", "-p", "-t", sourceSession, "#{pane_id}")
	prepared, err := service.PrepareReleaseFromPane(created.ID, callerPane, ReleaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	changedPath := filepath.Join(created.Repositories[0].Path, "changed-after-cleanup.txt")
	if err := os.WriteFile(changedPath, []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := service.StopPreparedRelease(prepared); err != nil {
		t.Fatal(err)
	}
	err = service.Reconcile()
	if err == nil || !strings.Contains(err.Error(), "changed after release cleanup") {
		t.Fatalf("Reconcile() error = %v", err)
	}
	environment, err := store.NewEnvironmentStore(stateDir).Find(created.EnvironmentID)
	if err != nil || environment.Status != domain.EnvironmentReleasing {
		t.Fatalf("changed Environment = %+v, error = %v", environment, err)
	}
	workspace, err := store.NewWorkspaceStore(stateDir).Find(created.ID)
	if err != nil || !workspace.Materialized {
		t.Fatalf("changed Workspace = %+v, error = %v", workspace, err)
	}

	if err := os.Remove(changedPath); err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(); err != nil {
		t.Fatal(err)
	}
	environment, err = store.NewEnvironmentStore(stateDir).Find(created.EnvironmentID)
	if err != nil || environment.Status != domain.EnvironmentReady {
		t.Fatalf("recovered Environment = %+v, error = %v", environment, err)
	}
}

func TestPrepareReleaseFromPaneRestoresOwnershipAfterCleanupFails(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "example",
		Repositories: []domain.RepositorySpec{{
			Name: "app", Clone: domain.CloneSpec{URL: source},
			Recycle: &domain.RecycleSpec{Command: []string{"sh", "-c", "exit 23"}},
		}},
	}
	socket := fmt.Sprintf("twt-in-session-rollback-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})

	created, err := service.Create("rollback", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	sourceSession, err := service.OwnedSessionID(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	callerPane := testTmuxOutput(t, socket, "display-message", "-p", "-t", sourceSession, "#{pane_id}")
	if _, err := service.PrepareReleaseFromPane(created.ID, callerPane, ReleaseOptions{}); err == nil || !strings.Contains(err.Error(), "recycle command") {
		t.Fatalf("PrepareReleaseFromPane() error = %v", err)
	}

	restored, err := store.NewWorkspaceStore(stateDir).Find(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Status != domain.WorkspaceActive || !restored.Materialized || restored.EnvironmentID != created.EnvironmentID {
		t.Fatalf("restored Workspace = %+v", restored)
	}
	environment, err := store.NewEnvironmentStore(stateDir).Find(created.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	if environment.Status != domain.EnvironmentClaimed || environment.Assignment == nil ||
		environment.Assignment.Kind != domain.EnvironmentAssignmentClaim ||
		environment.Assignment.Phase != domain.EnvironmentAssignmentActive {
		t.Fatalf("restored Environment = %+v", environment)
	}
	if branch := testGitOutput(t, created.Repositories[0].Path, "branch", "--show-current"); branch != created.Repositories[0].Branch {
		t.Fatalf("restored branch = %q, want %q", branch, created.Repositories[0].Branch)
	}
	if _, err := os.Stat(filepath.Join(created.Root, ".twt-owned.json")); err != nil {
		t.Fatalf("restored ownership marker: %v", err)
	}
}

func TestOpenRestoresAPreparedReleaseWhileItsSourceSessionExists(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "example",
		Repositories: []domain.RepositorySpec{{Name: "app", Clone: domain.CloneSpec{URL: source}}},
	}
	socket := fmt.Sprintf("twt-pending-open-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})

	created, err := service.Create("restore-pending", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	sourceSession, err := service.OwnedSessionID(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	callerPane := testTmuxOutput(t, socket, "display-message", "-p", "-t", sourceSession, "#{pane_id}")
	if _, err := service.PrepareReleaseFromPane(created.ID, callerPane, ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}

	opened, err := service.Open(created.ID)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if opened.Status != domain.WorkspaceActive || !opened.Materialized || opened.EnvironmentID != created.EnvironmentID {
		t.Fatalf("opened Workspace = %+v", opened)
	}
	if branch := testGitOutput(t, opened.Repositories[0].Path, "branch", "--show-current"); branch != created.Repositories[0].Branch {
		t.Fatalf("opened branch = %q, want %q", branch, created.Repositories[0].Branch)
	}
	environment, err := store.NewEnvironmentStore(stateDir).Find(created.EnvironmentID)
	if err != nil || environment.Status != domain.EnvironmentClaimed || environment.Assignment == nil ||
		environment.Assignment.Kind != domain.EnvironmentAssignmentClaim {
		t.Fatalf("opened Environment = %+v, error = %v", environment, err)
	}
}

func TestOpenRestoresAWorkspaceAfterARecycleFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "example",
		Repositories: []domain.RepositorySpec{{
			Name: "app", Clone: domain.CloneSpec{URL: source},
			Recycle: &domain.RecycleSpec{Command: []string{"sh", "-c", "exit 23"}},
		}},
	}
	socket := fmt.Sprintf("twt-recycle-recovery-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})

	created, err := service.Create("recover", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Release(created.ID, "", ReleaseOptions{}); err == nil || !strings.Contains(err.Error(), "recycle command") {
		t.Fatalf("Release() error = %v", err)
	}
	bound, err := store.NewWorkspaceStore(stateDir).Find(created.ID)
	if err != nil || bound.Status != domain.WorkspaceActive || !bound.Materialized || bound.EnvironmentID == "" {
		t.Fatalf("Workspace after the failed release = %+v, error = %v", bound, err)
	}
	environment, err := store.NewEnvironmentStore(stateDir).Find(created.EnvironmentID)
	if err != nil || environment.Status != domain.EnvironmentReleasing || environment.Assignment == nil {
		t.Fatalf("Prepared Environment after the failed release = %+v, error = %v", environment, err)
	}

	opened, err := service.Open(created.ID)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if opened.Status != domain.WorkspaceActive || !opened.Materialized || opened.EnvironmentID != created.EnvironmentID {
		t.Fatalf("restored Workspace = %+v", opened)
	}
	environment, err = store.NewEnvironmentStore(stateDir).Find(created.EnvironmentID)
	if err != nil || environment.Status != domain.EnvironmentClaimed || environment.Assignment == nil || environment.Assignment.Kind != domain.EnvironmentAssignmentClaim {
		t.Fatalf("restored Prepared Environment = %+v, error = %v", environment, err)
	}
}

func TestReconcileCompletesARecordedRelease(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "example",
		Repositories: []domain.RepositorySpec{{Name: "app", Clone: domain.CloneSpec{URL: source}}},
	}
	socket := fmt.Sprintf("twt-release-reconcile-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})

	created, err := service.Create("reconcile", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Release(created.ID, "", ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}
	environments := store.NewEnvironmentStore(stateDir)
	environment, err := environments.Find(created.EnvironmentID)
	if err != nil {
		t.Fatal(err)
	}
	environment.Generation++
	environment.Status = domain.EnvironmentReleasing
	environment.Assignment = &domain.EnvironmentAssignment{
		Generation: environment.Generation, Kind: domain.EnvironmentAssignmentRelease,
		Phase: domain.EnvironmentAssignmentReserved, Workspace: created, ReservedAt: time.Now().UTC(),
	}
	if err := environments.Save(environment); err != nil {
		t.Fatal(err)
	}

	if err := service.Reconcile(); err != nil {
		t.Fatal(err)
	}
	reconciled, err := environments.Find(created.EnvironmentID)
	if err != nil || reconciled.Status != domain.EnvironmentReady || reconciled.Assignment != nil {
		t.Fatalf("reconciled Prepared Environment = %+v, error = %v", reconciled, err)
	}
}

func TestRemoveAReleasedWorkspaceKeepsThePreparedEnvironment(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "example",
		Repositories: []domain.RepositorySpec{{Name: "app", Clone: domain.CloneSpec{URL: source}}},
	}
	socket := fmt.Sprintf("twt-released-remove-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})

	created, err := service.Create("remove", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	root := created.Root
	branch := created.Repositories[0].Branch
	if _, err := service.Release(created.ID, "", ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}
	plan, err := service.Remove(created.ID, "", RemovalOptions{AllowUnpublished: true})
	if err != nil {
		t.Fatalf("Remove() error = %v, plan = %+v", err, plan)
	}
	if _, err := store.NewWorkspaceStore(stateDir).Find(created.ID); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("removed Workspace lookup error = %v", err)
	}
	environment, err := store.NewEnvironmentStore(stateDir).Find(created.EnvironmentID)
	if err != nil || environment.Status != domain.EnvironmentReady {
		t.Fatalf("kept Prepared Environment = %+v, error = %v", environment, err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("removal deleted the prepared root: %v", err)
	}
	if exists, err := refExists(created.Repositories[0].CachePath, "refs/heads/"+branch); err != nil || exists {
		t.Fatalf("removed branch exists = %t, error = %v", exists, err)
	}
}

func TestReleaseFingerprintIncludesGitOperationState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repository := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, repository)
	worktree := filepath.Join(t.TempDir(), "worktree")
	if err := run(repository, "git", "worktree", "add", "--detach", worktree, "HEAD"); err != nil {
		t.Fatal(err)
	}
	rebasePath, err := output(worktree, "git", "rev-parse", "--git-path", "rebase-merge")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(rebasePath) {
		rebasePath = filepath.Join(repository, rebasePath)
	}
	if err := os.MkdirAll(rebasePath, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(rebasePath, "msgnum")
	if err := os.WriteFile(statePath, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := inspectRepositoryRelease(domain.WorkspaceRepository{Name: "app", Path: worktree})
	if err != nil {
		t.Fatal(err)
	}
	if first.gitOperation != "rebase" {
		t.Fatalf("Git operation = %q", first.gitOperation)
	}
	if err := os.WriteFile(statePath, []byte("2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := inspectRepositoryRelease(domain.WorkspaceRepository{Name: "app", Path: worktree})
	if err != nil {
		t.Fatal(err)
	}
	if releaseFingerprint([]repositoryReleaseState{first}) == releaseFingerprint([]repositoryReleaseState{second}) {
		t.Fatal("release fingerprint did not change with the Git operation state")
	}
}

func TestReleaseFingerprintIncludesHeadAndWorkingTreeState(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repository := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, repository)
	inspect := func() repositoryReleaseState {
		t.Helper()
		state, err := inspectRepositoryRelease(domain.WorkspaceRepository{Name: "app", Path: repository})
		if err != nil {
			t.Fatal(err)
		}
		return state
	}

	clean := inspect()
	if err := run(repository, "git", "commit", "--allow-empty", "-qm", "move head"); err != nil {
		t.Fatal(err)
	}
	newHead := inspect()
	if clean.fingerprint == newHead.fingerprint {
		t.Fatal("release fingerprint did not change when HEAD changed")
	}

	readme := filepath.Join(repository, "README.md")
	if err := os.WriteFile(readme, []byte("unstaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unstaged := inspect()
	if newHead.fingerprint == unstaged.fingerprint {
		t.Fatal("release fingerprint did not change for an unstaged change")
	}
	if err := run(repository, "git", "add", "README.md"); err != nil {
		t.Fatal(err)
	}
	staged := inspect()
	if unstaged.fingerprint == staged.fingerprint {
		t.Fatal("release fingerprint did not change when a change became staged")
	}
	if err := os.WriteFile(readme, []byte("worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	firstIndex := inspect()
	if err := os.WriteFile(readme, []byte("second index\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(repository, "git", "add", "README.md"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(readme, []byte("worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	secondIndex := inspect()
	if firstIndex.fingerprint == secondIndex.fingerprint {
		t.Fatal("release fingerprint did not change with staged content")
	}

	untrackedPath := filepath.Join(repository, "untracked.txt")
	if err := os.WriteFile(untrackedPath, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	firstUntracked := inspect()
	if err := os.WriteFile(untrackedPath, []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	secondUntracked := inspect()
	if firstUntracked.fingerprint == secondUntracked.fingerprint {
		t.Fatal("release fingerprint did not change with untracked file content")
	}
}

func TestOpenDoesNotRaceAnActiveRelease(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "example",
		Repositories: []domain.RepositorySpec{{Name: "app", Clone: domain.CloneSpec{URL: source}}},
	}
	socket := fmt.Sprintf("twt-release-lock-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket})
	workspace, err := service.Create("locked", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.InspectRelease(workspace.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := service.reserveRelease(&workspace, plan, ReleaseOptions{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Open(workspace.ID); clierr.CodeOf(err) != clierr.Locked {
		lock.Release()
		t.Fatalf("Open() during release error = %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	opened, err := service.Open(workspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Status != domain.WorkspaceActive || !opened.Materialized {
		t.Fatalf("restored Workspace = %+v", opened)
	}
}

// A finished release reports its Workspace Template, so the CLI can top up
// the Prepared Environment pool.
func TestReleaseRunsTheAfterReleaseFinalizedHook(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}

	stateDir := t.TempDir()
	dataDir := t.TempDir()
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "example",
		Repositories: []domain.RepositorySpec{{Name: "app", Clone: domain.CloneSpec{URL: source}}},
	}
	socket := fmt.Sprintf("twt-release-hook-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	var refilled []string
	var options Options
	options = Options{
		StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket,
		// The hook takes the mutation lock, like the real pool refill. The
		// service must run it with no lock held.
		AfterReleaseFinalized: func(templateName string) {
			lock, err := store.AcquireMutationLock(options.StateDir)
			if err != nil {
				t.Errorf("AfterReleaseFinalized ran with the mutation lock held: %v", err)
				return
			}
			_ = lock.Release()
			refilled = append(refilled, templateName)
		},
	}
	service := NewService(options)

	workspace, err := service.Create("hook", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	if len(refilled) != 0 {
		t.Fatalf("the hook ran before the release: %v", refilled)
	}
	if _, err := service.Release(workspace.ID, "", ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}
	if len(refilled) != 1 || refilled[0] != "example" {
		t.Fatalf("AfterReleaseFinalized calls = %v, want one call with \"example\"", refilled)
	}
}
