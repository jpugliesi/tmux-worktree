package main

import (
	"os"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "__twt2_prepare_worker" {
		if err := cli.RunPrepareWorker(cli.DefaultOptions(), os.Args[2:]); err != nil {
			_, _ = os.Stderr.WriteString("twt2: " + err.Error() + "\n")
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "__twt2_quick_create_worker" {
		if err := cli.RunQuickCreateWorker(cli.DefaultOptions(), os.Args[2:]); err != nil {
			_, _ = os.Stderr.WriteString("twt2: " + err.Error() + "\n")
			os.Exit(1)
		}
		return
	}
	command := cli.New(cli.DefaultOptions())
	executed, err := command.ExecuteC()
	if err != nil {
		_ = cli.WriteError(executed, os.Stderr, err)
		os.Exit(1)
	}
}
