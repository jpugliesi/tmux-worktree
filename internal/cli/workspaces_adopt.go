package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

func newWorkspacesAdoptCommand(service *workspaceservice.Service) *cobra.Command {
	var name string
	command := &cobra.Command{
		Use:   "adopt [SESSION]",
		Short: "Adopt an existing tmux session as a Workspace",
		Args:  optionalArg("SESSION"),
		RunE: func(command *cobra.Command, args []string) error {
			session := ""
			if len(args) == 1 {
				session = args[0]
			}
			pane := os.Getenv("TMUX_PANE")
			var workspace domain.Workspace
			return runMutation(command, "workspaces.adopt",
				func() (string, string, error) {
					validated, err := service.ValidateAdopt(session, pane, name)
					return "", validated.Name, err
				},
				func() (string, string, error) {
					var err error
					workspace, err = service.Adopt(session, pane, name)
					return workspace.ID, workspace.Name, err
				},
				func(out io.Writer, _, _ string) error {
					_, err := fmt.Fprintf(out, "Adopted Workspace %q (%s)\nRepositories: %d\n", workspace.Name, workspace.ID, len(workspace.Repositories))
					return err
				})
		},
	}
	command.Flags().StringVar(&name, "name", "", "Set the Workspace name. The default name is the tmux session name")
	setArguments(command, optionalArgument("session", "the default is the tmux session of the caller"))
	command.ValidArgsFunction = adoptSessionCompletion(service)
	return command
}
