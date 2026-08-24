package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

func newWorkspacesArchiveCommand(options Options, service *workspaceservice.Service) *cobra.Command {
	command := &cobra.Command{
		Use:   "archive WORKSPACE",
		Short: "Archive a Workspace without removing its data",
		Args:  exactArgs("WORKSPACE"),
		RunE: func(command *cobra.Command, args []string) error {
			reference, err := resolveWorkspaceReference(service, args[0])
			if err != nil {
				return err
			}
			return archiveWorkspace(command, options, service, reference)
		},
	}
	setArguments(command, requiredArgument("workspace"))
	command.ValidArgsFunction = workspaceNameCompletion(service)
	return command
}

func newArchiveCommand(options Options) *cobra.Command {
	service := options.workspaceService()
	command := &cobra.Command{
		Use:   "archive [WORKSPACE]",
		Short: "Archive the current Workspace or a specified Workspace",
		Args:  optionalArg("WORKSPACE"),
		RunE: func(command *cobra.Command, args []string) error {
			reference := currentWorkspaceReference
			if len(args) == 1 {
				reference = args[0]
			}
			reference, err := resolveWorkspaceReference(service, reference)
			if err != nil {
				return err
			}
			return archiveWorkspace(command, options, service, reference)
		},
	}
	setArguments(command, optionalArgument("workspace", "the current Workspace when absent"))
	command.ValidArgsFunction = workspaceNameCompletion(service)
	return command
}

// archiveWorkspace archives one Workspace. Inside the Workspace tmux session it
// relocates the calling client first and behaves like done --keep.
func archiveWorkspace(command *cobra.Command, options Options, service *workspaceservice.Service, reference string) error {
	if isDryRun(command) {
		return archiveWorkspaceRecord(command, service, reference)
	}
	workspace, err := service.Find(reference)
	if err != nil {
		return err
	}
	currentPane := os.Getenv("TMUX_PANE")
	relocate, err := relocationNeeded(command, options, service, workspace.ID, currentPane)
	if err != nil {
		return err
	}
	if relocate {
		return relocateAndComplete(command, options, service, workspace, currentPane, true, workspaceservice.RemovalOptions{}, doneTicketPlan{})
	}
	return archiveWorkspaceRecord(command, service, workspace.ID)
}

// archiveWorkspaceRecord archives one Workspace without tmux client relocation.
// The apply command uses it directly.
func archiveWorkspaceRecord(command *cobra.Command, service *workspaceservice.Service, reference string) error {
	result := workspaceservice.ArchiveResult{}
	return runMutation(command, "workspaces.archive",
		func() (string, string, error) {
			return "", reference, service.ValidateArchive(reference, os.Getenv("TMUX_PANE"))
		},
		func() (string, string, error) {
			var err error
			result, err = service.Archive(reference, os.Getenv("TMUX_PANE"))
			return result.Workspace.ID, result.Workspace.Name, err
		},
		func(out io.Writer, _, name string) error {
			if err := printStoppedAgents(out, result.StoppedAgents); err != nil {
				return err
			}
			_, err := fmt.Fprintf(out, "Archived Workspace %q\n", name)
			return err
		})
}

func printStoppedAgents(out io.Writer, agents []domain.AgentSession) error {
	if len(agents) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(out, "Stopping %d live Agent Sessions:\n", len(agents)); err != nil {
		return err
	}
	for _, agent := range agents {
		if _, err := fmt.Fprintf(out, "  %s %s %s\n", agent.ID, agent.Provider, agent.Label); err != nil {
			return err
		}
	}
	return nil
}
