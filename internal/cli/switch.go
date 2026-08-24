package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

func newSwitchCommand(options Options) *cobra.Command {
	service := options.workspaceService()
	command := &cobra.Command{
		Use:   "switch [WORKSPACE]",
		Short: "Switch the tmux client to a Workspace session",
		Args:  optionalArg("WORKSPACE"),
		PreRunE: func(command *cobra.Command, _ []string) error {
			if WantsJSON(command) {
				return invalidUsage(command, "switch is an interactive command and does not support JSON output")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			var workspace domain.Workspace
			var err error
			if len(args) == 1 {
				workspace, err = resolveWorkspace(service, args[0])
			} else {
				workspace, err = pickSwitchWorkspace(command, options, service)
			}
			if err != nil {
				return err
			}
			if isDryRun(command) {
				return writeSwitchPlan(command.OutOrStdout(), workspace)
			}
			if workspace.Status == domain.WorkspaceArchived {
				workspace, err = service.Open(workspace.ID)
				if err != nil {
					return err
				}
			}
			return openTmux(options, workspace.TmuxSession)
		},
	}
	setArguments(command, optionalArgument("workspace", "the interactive picker asks for it when absent"))
	command.ValidArgsFunction = workspaceNameCompletion(service)
	return command
}

// writeSwitchPlan tells the user what switch would do, without a change.
func writeSwitchPlan(out io.Writer, workspace domain.Workspace) error {
	if workspace.Status == domain.WorkspaceArchived {
		_, err := fmt.Fprintf(out, "Dry run: open archived Workspace %q, then switch the client to session %q.\n", workspace.Name, workspace.TmuxSession)
		return err
	}
	_, err := fmt.Fprintf(out, "Dry run: switch the client to session %q of Workspace %q.\n", workspace.TmuxSession, workspace.Name)
	return err
}

// pickSwitchWorkspace shows the interactive Workspace picker and returns the
// selected Workspace. Active Workspaces come first, most recent first.
func pickSwitchWorkspace(command *cobra.Command, options Options, service *workspaceservice.Service) (domain.Workspace, error) {
	workspaces, err := service.List()
	if err != nil {
		return domain.Workspace{}, err
	}
	if len(workspaces) == 0 {
		return domain.Workspace{}, clierr.New(clierr.NotFound, "no Workspaces exist; run 'twt start NAME' first")
	}
	sortWorkspacesForDisplay(workspaces)
	now := time.Now().UTC()
	lines := make([]string, 0, len(workspaces))
	for _, workspace := range workspaces {
		age := formatAge(now.Sub(workspaceAgeReference(workspace)))
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%s", workspace.Name, workspace.TemplateName, workspace.Status, age))
	}
	index, err := options.SwitchPick(command, lines)
	if err != nil {
		return domain.Workspace{}, err
	}
	if index < 0 || index >= len(workspaces) {
		return domain.Workspace{}, fmt.Errorf("the Workspace picker returned an invalid selection")
	}
	return workspaces[index], nil
}

// realSwitchPick selects one picker line with fzf when it is installed, or
// with a numbered list on the terminal.
func realSwitchPick(command *cobra.Command, lines []string) (int, error) {
	return pickLine(command, lines, pickOptions{
		Noun:        "Workspace",
		MissingHint: "missing WORKSPACE; use 'twt switch WORKSPACE' in a script",
	})
}
