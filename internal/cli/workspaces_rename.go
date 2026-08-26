package cli

import (
	"fmt"
	"io"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

func newWorkspacesRenameCommand(options Options, service *workspaceservice.Service) *cobra.Command {
	command := &cobra.Command{
		Use:   "rename [WORKSPACE] [NAME]",
		Short: "Rename a Workspace",
		Args: func(command *cobra.Command, args []string) error {
			if len(args) > 2 {
				return invalidUsage(command, "unexpected argument %q; expected [WORKSPACE] [NAME]", args[2])
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			if len(args) < 2 && !canPromptWorkspaceName(command) {
				return invalidUsage(command, "missing arguments; use '%s WORKSPACE NAME' in a script", command.CommandPath())
			}
			var workspace domain.Workspace
			var err error
			if len(args) == 0 {
				workspace, err = pickSwitchWorkspace(command, options, service)
			} else {
				workspace, err = resolveWorkspace(service, args[0])
			}
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 2 {
				name = args[1]
			} else {
				name, err = promptTicketLine(command, "New Workspace name: ")
				if err != nil {
					return err
				}
				if name == "" {
					return invalidUsage(command, "Workspace rename was canceled; no new name was given")
				}
			}
			return renameWorkspace(command, service, workspace.ID, workspace.Name, name)
		},
	}
	setArguments(command,
		optionalArgument("workspace", "the interactive picker asks for it when absent"),
		optionalArgument("name", "an interactive terminal asks for it when absent"),
	)
	command.ValidArgsFunction = workspaceNameCompletion(service)
	return command
}

func renameWorkspace(command *cobra.Command, service *workspaceservice.Service, reference, oldName, name string) error {
	return runMutation(command, "workspaces.rename",
		func() (string, string, error) {
			return reference, name, service.ValidateRename(reference, name)
		},
		func() (string, string, error) {
			workspace, err := service.Rename(reference, name)
			return workspace.ID, workspace.Name, err
		},
		func(out io.Writer, _, _ string) error {
			_, err := fmt.Fprintf(out, "Renamed Workspace %q to %q\n", oldName, name)
			return err
		})
}
