package cli

import (
	"fmt"

	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

type ticketGitSyncOutput struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Operation     string                   `json:"operation"`
	Status        string                   `json:"status"`
	Result        ticketservice.SyncStatus `json:"result"`
}

func newTicketsGitSyncCommand(options Options) *cobra.Command {
	return &cobra.Command{
		Use:   "git-sync",
		Short: "Reconcile the Tickets home with its git remote",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			dryRun := isDryRun(command)
			result, err := service.Sync(dryRun)
			if err != nil {
				return err
			}
			status := statusApplied
			if dryRun {
				status = statusValid
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, ticketGitSyncOutput{
					SchemaVersion: jsonSchemaVersion,
					Operation:     "tickets.git-sync",
					Status:        status,
					Result:        result,
				})
			}
			verb := "Synced"
			if dryRun {
				verb = "Would sync"
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "%s the Tickets home with %s/%s: pulled %d, pushed %d.\n",
				verb, result.Remote, result.Branch, result.PulledCommits, result.PushedCommits)
			return err
		},
	}
}
