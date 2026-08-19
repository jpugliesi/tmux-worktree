package main

import (
	"os"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
)

func main() {
	command := cli.New(cli.DefaultOptions())
	executed, err := command.ExecuteC()
	if err != nil {
		_ = cli.WriteError(executed, os.Stderr, err)
		os.Exit(1)
	}
}
