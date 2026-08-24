package cli_test

import (
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
)

func TestWorkspaceShortcutsAreCreateAndNext(t *testing.T) {
	root := cli.New(cli.Options{ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir()})

	create := findCommand(root, "create")
	if create == nil || !create.Runnable() {
		t.Fatal("twt create is not a runnable command")
	}
	if findCommand(root, "next") == nil {
		t.Fatal("twt next does not exist")
	}
	if findCommand(root, "start") != nil {
		t.Fatal("twt start still exists")
	}
	if keepCurrent := findCommand(root, "next").Flags().Lookup("keep-current"); keepCurrent != nil {
		t.Fatal("twt next can keep the current Workspace active")
	}
	if keepCurrent := findCommand(root, "tickets", "start").Flags().Lookup("keep-current"); keepCurrent != nil {
		t.Fatal("twt tickets start still has --keep-current")
	}
}
