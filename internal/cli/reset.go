package cli

import (
	"fmt"
	"os"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/tmux"
	"github.com/spf13/cobra"
)

func newResetCommand(options Options) *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "Respawn every pane in the current tmux window",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runReset(command, options)
		},
	}
}

type resetOutput struct {
	SchemaVersion int      `json:"schemaVersion"`
	Operation     string   `json:"operation"`
	Status        string   `json:"status"`
	Panes         []string `json:"panes"`
}

func runReset(command *cobra.Command, options Options) error {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return invalidUsageWithHint(command,
			"Run 'twt reset' from a tmux pane.",
			"twt reset requires a tmux pane")
	}
	client := tmux.Client{Socket: options.TmuxSocket}
	var (
		panes []string
		err   error
	)
	if isDryRun(command) {
		panes, err = client.ListWindowPanes(pane)
	} else {
		panes, err = client.ResetWindowPanes(pane)
	}
	if err != nil {
		return clierr.WithHint(
			clierr.Wrap(clierr.PreconditionFailed, err),
			"Run 'twt reset' from a live tmux pane.")
	}
	status := statusApplied
	verb := "Reset"
	if isDryRun(command) {
		status = statusValid
		verb = "Would reset"
	}
	if WantsJSON(command) {
		if err := writeJSONOutput(command, resetOutput{
			SchemaVersion: jsonSchemaVersion,
			Operation:     "reset",
			Status:        status,
			Panes:         panes,
		}); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintf(command.OutOrStdout(), "%s %s\n", verb, paneCount(len(panes))); err != nil {
		return err
	}
	if isDryRun(command) {
		return nil
	}
	if err := client.ResetPane(pane); err != nil {
		return clierr.WithHint(
			clierr.Wrap(clierr.PreconditionFailed, err),
			"Run 'twt reset' from a live tmux pane.")
	}
	return nil
}

func paneCount(n int) string {
	if n == 1 {
		return "1 pane"
	}
	return fmt.Sprintf("%d panes", n)
}
