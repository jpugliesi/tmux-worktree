package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// driftTestSetup prepares one ready Prepared Environment on a real Git
// origin and returns the service, the template, the source repository, and
// the ready environment. Progress messages collect in messages.
type driftTestSetup struct {
	service     *Service
	template    domain.Template
	source      string
	environment domain.PreparedEnvironment
	messages    func() []string
}

func newDriftTestSetup(t *testing.T, initialize *domain.InitializeSpec) driftTestSetup {
	t.Helper()
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
			Name:       "app",
			Clone:      domain.CloneSpec{URL: source},
			Initialize: initialize,
		}},
	}
	socket := fmt.Sprintf("twt-workspace-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	var mu sync.Mutex
	var messages []string
	service := NewService(Options{
		StateDir: stateDir, DataDir: dataDir, TmuxSocket: socket,
		Progress: func(message string) {
			mu.Lock()
			messages = append(messages, message)
			mu.Unlock()
		},
	})
	environment, err := service.Prepare(template.Name, template)
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	if environment.Status != domain.EnvironmentReady {
		t.Fatalf("Prepared Environment status = %q, want %q", environment.Status, domain.EnvironmentReady)
	}
	return driftTestSetup{
		service: service, template: template, source: source, environment: environment,
		messages: func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), messages...)
		},
	}
}

func (d driftTestSetup) hasMessage(want string) bool {
	for _, message := range d.messages() {
		if strings.Contains(message, want) {
			return true
		}
	}
	return false
}

// moveDetachedCheckout adds one commit to the origin, fetches it into the
// Repository Cache, and moves the detached prepared checkout to it without
// touching the Prepared Environment record. This is the state a daemon
// refresh leaves when it stops between the reset and the record save.
func (d driftTestSetup) moveDetachedCheckout(t *testing.T, fileName string) string {
	t.Helper()
	repository := d.environment.Repositories[0]
	commitCreateTestFile(t, d.source, fileName)
	testGitOutput(t, repository.CachePath, "fetch", "--no-tags", "origin", "+refs/heads/main:refs/remotes/origin/main")
	testGitOutput(t, repository.Path, "reset", "--hard", "refs/remotes/origin/main")
	head := testGitOutput(t, repository.Path, "rev-parse", "HEAD")
	if head == repository.BaseCommit {
		t.Fatalf("the checkout did not move from %s", head)
	}
	return head
}

func TestCreateAdoptsAMovedDetachedPreparedCheckout(t *testing.T) {
	setup := newDriftTestSetup(t, nil)
	head := setup.moveDetachedCheckout(t, "MOVED.md")

	workspace, err := setup.service.CreateWithOptions("moved", setup.template.Name, setup.template, CreateOptions{})
	if err != nil {
		t.Fatalf("CreateWithOptions() with a moved checkout: %v", err)
	}
	if workspace.Status != domain.WorkspaceActive {
		t.Fatalf("Workspace status = %q, want %q", workspace.Status, domain.WorkspaceActive)
	}
	if workspace.EnvironmentID != setup.environment.ID {
		t.Fatalf("Workspace claimed environment %s, want the moved environment %s", workspace.EnvironmentID, setup.environment.ID)
	}
	if workspace.Repositories[0].BaseCommit != head {
		t.Fatalf("Workspace base commit = %s, want the checkout HEAD %s", workspace.Repositories[0].BaseCommit, head)
	}
	if _, err := os.Stat(filepath.Join(workspace.Root, "app", "MOVED.md")); err != nil {
		t.Fatalf("the Workspace branch does not start at the checkout HEAD: %v", err)
	}
	environment, err := setup.service.environments.Find(setup.environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if environment.Repositories[0].BaseCommit != head {
		t.Fatalf("Prepared Environment base commit = %s, want %s", environment.Repositories[0].BaseCommit, head)
	}
	if !setup.hasMessage("twt uses the checkout commit") {
		t.Fatalf("progress does not report the adopted checkout: %v", setup.messages())
	}
}

func TestRefreshSavesTheMovedBaseBeforeInitializationFails(t *testing.T) {
	// The initialization passes while the sentinel file is absent.
	setup := newDriftTestSetup(t, &domain.InitializeSpec{Command: []string{"sh", "-c",
		`test ! -e "$TWT_ENVIRONMENT_ROOT/../refresh-fails"`}})
	sentinel := filepath.Join(setup.service.options.DataDir, "projects", "refresh-fails")
	if err := os.WriteFile(sentinel, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	commitCreateTestFile(t, setup.source, "TIP.md")
	tip := testGitOutput(t, setup.source, "rev-parse", "HEAD")

	if _, err := setup.service.RefreshPreparedEnvironment(setup.environment.ID); err == nil {
		t.Fatal("RefreshPreparedEnvironment() succeeded, want the initialization failure")
	}
	environment, err := setup.service.environments.Find(setup.environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if environment.Repositories[0].BaseCommit != tip {
		t.Fatalf("Prepared Environment base commit = %s after a failed refresh, want the moved checkout %s", environment.Repositories[0].BaseCommit, tip)
	}
	head := testGitOutput(t, environment.Repositories[0].Path, "rev-parse", "HEAD")
	if head != tip {
		t.Fatalf("checkout HEAD = %s, want %s", head, tip)
	}
}

func TestCreateReplacesAPreparedEnvironmentWithAnUnusableCheckout(t *testing.T) {
	setup := newDriftTestSetup(t, nil)
	// A checkout on a foreign branch is not something a claim can repair.
	testGitOutput(t, setup.environment.Repositories[0].Path, "switch", "-c", "stray")

	workspace, err := setup.service.CreateWithOptions("fix-auth", setup.template.Name, setup.template, CreateOptions{})
	if err != nil {
		t.Fatalf("CreateWithOptions() with an unusable checkout: %v", err)
	}
	if workspace.Status != domain.WorkspaceActive {
		t.Fatalf("Workspace status = %q, want %q", workspace.Status, domain.WorkspaceActive)
	}
	if workspace.EnvironmentID == setup.environment.ID {
		t.Fatalf("Workspace claimed the unusable environment %s", setup.environment.ID)
	}
	if !setup.hasMessage("is not usable") || !setup.hasMessage("twt prepares a replacement.") {
		t.Fatalf("progress does not report the replacement: %v", setup.messages())
	}
	environments, err := setup.service.environments.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range environments {
		if environment.ID == setup.environment.ID {
			t.Fatalf("the unusable Prepared Environment remains: %+v", environment)
		}
	}
	workspaces, err := store.NewWorkspaceStore(setup.service.options.StateDir).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != 1 || workspaces[0].ID != workspace.ID {
		t.Fatalf("Workspace records after the replacement = %+v, want only %s", workspaces, workspace.ID)
	}
}

func TestRetryCreatesAReplacementWhenTheClaimedEnvironmentIsUnusable(t *testing.T) {
	setup := newDriftTestSetup(t, &domain.InitializeSpec{Command: []string{"true"}})
	// Record a claim that stopped before the checkout, the way an
	// interrupted create leaves it, then make the checkout unusable.
	environment := setup.environment
	workspace := setup.service.workspaceForEnvironment("stuck", setup.template.Name, setup.template, environment, "0123456789abcdef0123456789abcdef", "twt/stuck", CreateOptions{})
	environment.Status = domain.EnvironmentClaiming
	environment.Generation++
	environment.Assignment = &domain.EnvironmentAssignment{
		Generation: environment.Generation, Kind: domain.EnvironmentAssignmentClaim,
		Phase: domain.EnvironmentAssignmentReserved, Workspace: workspace, ReservedAt: time.Now().UTC(),
	}
	if err := setup.service.environments.Save(environment); err != nil {
		t.Fatal(err)
	}
	if err := setup.service.store.Save(workspace); err != nil {
		t.Fatal(err)
	}
	testGitOutput(t, environment.Repositories[0].Path, "switch", "-c", "stray")

	retried, err := setup.service.Retry("stuck")
	if err != nil {
		t.Fatalf("Retry() with an unusable claimed environment: %v", err)
	}
	if retried.Status != domain.WorkspaceActive || retried.Name != "stuck" {
		t.Fatalf("retried Workspace = %+v, want an active Workspace named stuck", retried)
	}
	if retried.EnvironmentID == environment.ID {
		t.Fatalf("Retry() kept the unusable environment %s", environment.ID)
	}
	if retried.Repositories[0].Branch != "twt/stuck" {
		t.Fatalf("retried Workspace branch = %q, want the reserved branch twt/stuck", retried.Repositories[0].Branch)
	}
	if _, err := setup.service.store.Find(workspace.ID); err == nil {
		t.Fatalf("the reserved Workspace record %s remains after the replacement", workspace.ID)
	}
}
