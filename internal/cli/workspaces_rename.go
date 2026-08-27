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
			if len(args) == 0 && !canPromptWorkspaceName(command) {
				return invalidUsage(command, "missing arguments; use '%s NAME' or '%s WORKSPACE NAME' in a script", command.CommandPath(), command.CommandPath())
			}
			workspace, name, err := resolveRenameArguments(command, options, service, args)
			if err != nil {
				return err
			}
			return renameWorkspace(command, service, workspace.ID, workspace.Name, name)
		},
	}
	setArguments(command,
		optionalArgument("workspace", "the current Workspace when only NAME is given; the picker asks when both arguments are absent"),
		optionalArgument("name", "an interactive terminal asks for it when absent"),
	)
	command.ValidArgsFunction = workspaceNameCompletion(service)
	return command
}

func resolveRenameArguments(command *cobra.Command, options Options, service *workspaceservice.Service, args []string) (domain.Workspace, string, error) {
	switch len(args) {
	case 0:
		workspace, err := pickSwitchWorkspace(command, options, service)
		if err != nil {
			return domain.Workspace{}, "", err
		}
		name, err := promptTicketLine(command, "New Workspace name: ")
		if err != nil {
			return domain.Workspace{}, "", err
		}
		if name == "" {
			return domain.Workspace{}, "", invalidUsage(command, "Workspace rename was canceled; no new name was given")
		}
		return workspace, name, nil
	case 1:
		workspace, err := resolveWorkspace(service, currentWorkspaceReference)
		if err != nil {
			return domain.Workspace{}, "", err
		}
		return workspace, args[0], nil
	default:
		workspace, err := resolveWorkspace(service, args[0])
		if err != nil {
			return domain.Workspace{}, "", err
		}
		return workspace, args[1], nil
	}
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
