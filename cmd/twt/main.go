package main

import (
	"os"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

// workers maps each hidden worker argument to its entry point. Worker errors
// deliberately bypass the clierr exit codes: a worker is a background process
// without a user, so it reports each failure with exit code 1 only.
var workers = map[string]func(cli.Options, []string) error{
	"__twt_prepare_worker":      cli.RunPrepareWorker,
	"__twt_quick_create_worker": cli.RunQuickCreateWorker,
	"__twt_done_worker":         cli.RunDoneWorker,
}

func main() {
	if len(os.Args) > 1 {
		if worker, found := workers[os.Args[1]]; found {
			if err := worker(cli.DefaultOptions(), os.Args[2:]); err != nil {
				_, _ = os.Stderr.WriteString("twt: " + err.Error() + "\n")
				os.Exit(1)
			}
			return
		}
	}
	command := cli.New(cli.DefaultOptions())
	executed, err := command.ExecuteC()
	if err != nil {
		_ = cli.WriteError(executed, os.Stderr, err)
		os.Exit(clierr.ExitCode(err))
	}
}
