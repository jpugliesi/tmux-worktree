package workspace

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func busyTestService(t *testing.T) (*Service, domain.Template, string) {
	t.Helper()
	for _, tool := range []string{"git", "tmux"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	source := filepath.Join(t.TempDir(), "source")
	initCreateTestRepository(t, source)
	template := domain.Template{
		Version: domain.TemplateVersion, Name: "example",
		Repositories: []domain.RepositorySpec{{Name: "app", Clone: domain.CloneSpec{URL: source}}},
	}
	// A tmux pane runs SHELL. The startup files of a login shell can run
	// git in another directory, and tmux then reports that directory for
	// the pane, so the tests use a plain shell.
	t.Setenv("SHELL", "/bin/sh")
	socket := fmt.Sprintf("twt-busy-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	service := NewService(Options{StateDir: t.TempDir(), DataDir: t.TempDir(), TmuxSocket: socket})
	return service, template, socket
}

func TestPrepareSkipsAReadyEnvironmentThatALivePaneUses(t *testing.T) {
	service, template, socket := busyTestService(t)
	workspace, err := service.Create("first", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Release(workspace.ID, "", ReleaseOptions{Force: true, StopPanes: true}); err != nil {
		t.Fatal(err)
	}
	// A shell from an old session still works in the released worktree.
	if err := run("", "tmux", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-s", "leftover", "-c", workspace.Repositories[0].Path); err != nil {
		t.Fatal(err)
	}
	waitForPane(t, service, "leftover", workspace.Root)

	environment, err := service.Prepare(template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	if environment.ID == workspace.EnvironmentID {
		t.Fatalf("Prepare() returned busy Prepared Environment %q", environment.ID)
	}
}

func TestReleaseRefusesAForeignPaneUnlessItMayStopIt(t *testing.T) {
	service, template, socket := busyTestService(t)
	workspace, err := service.Create("busy", template.Name, template)
	if err != nil {
		t.Fatal(err)
	}
	if err := run("", "tmux", "-L", socket, "-f", "/dev/null", "new-session", "-d", "-s", "other", "-c", workspace.Repositories[0].Path); err != nil {
		t.Fatal(err)
	}
	waitForPane(t, service, "other", workspace.Root)

	_, err = service.Release(workspace.ID, "", ReleaseOptions{})
	if clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("Release() error = %v, want an unsafe-state refusal", err)
	}
	panes, err := service.ForeignPanes(workspace.ID, "")
	if err != nil || len(panes) != 1 {
		t.Fatalf("ForeignPanes() = %v, %v, want one pane", panes, err)
	}

	if _, err := service.Release(workspace.ID, "", ReleaseOptions{StopPanes: true}); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("tmux", "-L", socket, "has-session", "-t", "=other").Run(); err == nil {
		t.Fatal("Release() with StopPanes kept the foreign pane")
	}
}

// waitForPane waits until tmux reports a pane inside root. A new pane can
// report another directory until its shell starts.
// waitForPane waits until a pane of the named tmux session works inside
// root. A new pane reports another directory until its shell starts.
func waitForPane(t *testing.T, service *Service, sessionName, root string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		panes, err := service.livePanes()
		if err != nil {
			t.Fatal(err)
		}
		for _, pane := range panesInside(panes, root) {
			if pane.SessionName == sessionName {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pane of tmux session %q works inside %q", sessionName, root)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
