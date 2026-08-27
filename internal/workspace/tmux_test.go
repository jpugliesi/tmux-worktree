package workspace

import (
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestParseWorkspaceSessionRowsKeepsAnEmptyFinalOwner(t *testing.T) {
	rows := parseWorkspaceSessionRows("$1\texample", true)
	if len(rows) != 1 || rows[0].id != "$1" || rows[0].name != "example" || rows[0].ownerID != "" {
		t.Fatalf("parseWorkspaceSessionRows() = %+v", rows)
	}
}

func TestParseCombinedWorkspaceSessionRowsUsesTheCurrentThenLegacyOwner(t *testing.T) {
	rows := parseCombinedWorkspaceSessionRows("$1\tcurrent\tworkspace-one\tlegacy-one\n$2\tlegacy\t\tworkspace-two\n$3\tplain\t\t", true)
	if len(rows) != 3 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].ownerID != "workspace-one" || rows[1].ownerID != "workspace-two" || rows[2].ownerID != "" {
		t.Fatalf("owners = %+v", rows)
	}
}

func TestCreateWorkspaceSessionBatchesMultipleRepositoryWindows(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	socket := fmt.Sprintf("twt-tmux-batch-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	first := t.TempDir()
	second := t.TempDir()
	service := NewService(Options{StateDir: t.TempDir(), DataDir: t.TempDir(), TmuxSocket: socket})
	workspace := domain.Workspace{
		ID: "workspace-id", Name: "batched", TmuxSession: "example-batched",
		Repositories: []domain.WorkspaceRepository{
			{Name: "app", WindowName: "app", Path: first},
			{Name: "api", WindowName: "api", Path: second},
		},
	}
	sessionID, windows, err := service.createWorkspaceSession(workspace.TmuxSession, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if sessionID == "" || len(windows) != 2 || windows["app"] == "" || windows["api"] == "" {
		t.Fatalf("created session = %q, windows = %+v", sessionID, windows)
	}
	rows, err := service.workspaceSessionRows(true)
	if err != nil || len(rows) != 1 || rows[0].ownerID != workspace.ID {
		t.Fatalf("Workspace session rows = %+v, error = %v", rows, err)
	}
}
