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

func newWorkspacesArchiveCommand(options Options, service *workspaceservice.Service) *cobra.Command {
	var force bool
	command := &cobra.Command{
		Use:   "archive WORKSPACE",
		Short: "Archive a Workspace and return its worktrees to the prepared pool",
		Args:  exactArgs("WORKSPACE"),
		RunE: func(command *cobra.Command, args []string) error {
			reference, err := resolveWorkspaceReference(service, args[0])
			if err != nil {
				return err
			}
			return archiveWorkspace(command, options, service, reference, force)
		},
	}
	setArguments(command, requiredArgument("workspace"))
	command.Flags().BoolVar(&force, "force", false, "Discard uncommitted changes and preserve ignored files")
	command.ValidArgsFunction = workspaceNameCompletion(service)
	return command
}

func newArchiveCommand(options Options) *cobra.Command {
	service := options.workspaceService()
	var force bool
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
			return archiveWorkspace(command, options, service, reference, force)
		},
	}
	setArguments(command, optionalArgument("workspace", "the current Workspace when absent"))
	command.Flags().BoolVar(&force, "force", false, "Discard uncommitted changes and preserve ignored files")
	command.ValidArgsFunction = workspaceNameCompletion(service)
	return command
}

// archiveWorkspace archives one Workspace. Inside the Workspace tmux session it
// relocates the calling client first and uses the shared release.
func archiveWorkspace(command *cobra.Command, options Options, service *workspaceservice.Service, reference string, force bool) error {
	releaseOptions, err := releaseOptions(command, service, reference, force)
	if err != nil {
		return err
	}
	if isDryRun(command) {
		return archiveWorkspaceRecord(command, service, reference, releaseOptions)
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
		return relocateAndComplete(command, options, service, workspace, currentPane, true, releaseOptions, doneTicketPlan{})
	}
	return archiveWorkspaceRecord(command, service, workspace.ID, releaseOptions)
}

// archiveWorkspaceRecord archives one Workspace without tmux client relocation.
// The apply command uses it directly.
func archiveWorkspaceRecord(command *cobra.Command, service *workspaceservice.Service, reference string, opts workspaceservice.ReleaseOptions) error {
	result := workspaceservice.ArchiveResult{}
	return runMutation(command, "workspaces.archive",
		func() (string, string, error) {
			return "", reference, service.ValidateRelease(reference, os.Getenv("TMUX_PANE"), opts)
		},
		func() (string, string, error) {
			var err error
			result, err = service.Release(reference, os.Getenv("TMUX_PANE"), opts)
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

func releaseOptions(command *cobra.Command, service *workspaceservice.Service, reference string, force bool) (workspaceservice.ReleaseOptions, error) {
	plan, err := service.InspectRelease(reference, "")
	if err != nil {
		return workspaceservice.ReleaseOptions{}, err
	}
	options := workspaceservice.ReleaseOptions{Force: force, ExpectedFingerprint: plan.Fingerprint, Prevalidated: true}
	if plan.GitOperation != "" {
		return options, service.ValidateRelease(reference, "", options)
	}
	if !plan.Dirty || force {
		return options, nil
	}
	if isDryRun(command) || WantsJSON(command) || !interactiveTicketSession(command) {
		return options, service.ValidateRelease(reference, "", options)
	}
	if _, err := fmt.Fprintf(command.ErrOrStderr(), "Discard uncommitted changes in Workspace %q? [y/N] ", plan.Name); err != nil {
		return workspaceservice.ReleaseOptions{}, err
	}
	line, err := bufio.NewReader(command.InOrStdin()).ReadString('\n')
	if err != nil && err != io.EOF {
		return workspaceservice.ReleaseOptions{}, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
		return workspaceservice.ReleaseOptions{}, clierr.New(clierr.PreconditionFailed, "Workspace archive was canceled")
	}
	options.Force = true
	return options, nil
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
