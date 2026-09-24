package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

func newWorkspacesRenameCommand(options Options, service *workspaceservice.Service) *cobra.Command {
	var removeArchived bool
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
			opts := workspaceservice.RenameOptions{RemoveArchived: removeArchived, CurrentPane: os.Getenv("TMUX_PANE")}
			if !opts.RemoveArchived {
				opts.RemoveArchived, err = confirmRemoveArchivedHolder(command, service, workspace.ID, name, opts.CurrentPane)
				if err != nil {
					return err
				}
			}
			return renameWorkspace(command, service, workspace.ID, workspace.Name, name, opts)
		},
	}
	command.Flags().BoolVar(&removeArchived, "remove-archived", false, "Remove the archived Workspace that holds NAME, then rename")
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
		workspace, err := pickSwitchWorkspace(command, options, service, true)
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

// confirmRemoveArchivedHolder asks an interactive terminal whether the rename
// may remove the archived Workspace that holds the new name. It shows the
// removal plan first. A dry run, JSON output, or a script gets no question
// and keeps the validation error with its hint.
func confirmRemoveArchivedHolder(command *cobra.Command, service *workspaceservice.Service, workspaceID, name, currentPane string) (bool, error) {
	if isDryRun(command) || WantsJSON(command) || !interactiveTicketSession(command) {
		return false, nil
	}
	holder, err := service.Find(name)
	if err != nil || holder.Status != domain.WorkspaceArchived || holder.ID == workspaceID || holder.Name != name {
		return false, nil
	}
	plan, err := service.PlanRename(workspaceID, name, workspaceservice.RenameOptions{RemoveArchived: true, CurrentPane: currentPane})
	if err != nil {
		return false, err
	}
	errOut := command.ErrOrStderr()
	if _, err := fmt.Fprintf(errOut, "Workspace %q is archived.\n", name); err != nil {
		return false, err
	}
	if plan.Removal != nil {
		if err := printRemovalPlanText(errOut, *plan.Removal, false); err != nil {
			return false, err
		}
	}
	if _, err := fmt.Fprintf(errOut, "Remove archived Workspace %q and reuse its name? [y/N] ", name); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(command.InOrStdin()).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
		return false, clierr.New(clierr.PreconditionFailed, "Workspace rename was canceled")
	}
	return true, nil
}

func renameWorkspace(command *cobra.Command, service *workspaceservice.Service, reference, oldName, name string, opts workspaceservice.RenameOptions) error {
	removed := ""
	return runMutation(command, "workspaces.rename",
		func() (string, string, error) {
			_, err := service.PlanRename(reference, name, opts)
			return reference, name, err
		},
		func() (string, string, error) {
			plan, err := service.PlanRename(reference, name, opts)
			if err != nil {
				return "", "", err
			}
			if plan.Removal != nil {
				removed = plan.ArchivedHolder.Name
			}
			workspace, err := service.RenameWithOptions(reference, name, opts)
			return workspace.ID, workspace.Name, err
		},
		func(out io.Writer, _, _ string) error {
			if removed != "" {
				if _, err := fmt.Fprintf(out, "Removed archived Workspace %q\n", removed); err != nil {
					return err
				}
			}
			_, err := fmt.Fprintf(out, "Renamed Workspace %q to %q\n", oldName, name)
			return err
		})
}
