package cli

import (
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
	command.Flags().BoolVar(&force, "force", false, "Discard uncommitted changes, stop tmux panes that still use the worktrees, and preserve ignored files")
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
	command.Flags().BoolVar(&force, "force", false, "Discard uncommitted changes, stop tmux panes that still use the worktrees, and preserve ignored files")
	command.ValidArgsFunction = workspaceNameCompletion(service)
	return command
}

// archiveWorkspace archives one Workspace. Inside the Workspace tmux session,
// it cleans from the caller pane and then stops the complete session.
func archiveWorkspace(command *cobra.Command, options Options, service *workspaceservice.Service, reference string, force bool) error {
	if isDryRun(command) {
		releaseOptions, err := releaseOptions(command, service, reference, force)
		if err != nil {
			return err
		}
		return archiveWorkspaceRecord(command, service, reference, releaseOptions)
	}
	workspace, err := service.Find(reference)
	if err != nil {
		return err
	}
	// Find the current session first: it can adopt a restored session, and
	// the panes of an adopted session are not foreign.
	currentPane := os.Getenv("TMUX_PANE")
	fromCurrentSession, err := releaseFromCurrentSession(command, options, service, workspace.ID, currentPane)
	if err != nil {
		return err
	}
	releaseOptions, err := releaseOptions(command, service, workspace.ID, force)
	if err != nil {
		return err
	}
	if fromCurrentSession {
		return completeFromPane(command, options, service, workspace, currentPane, true, releaseOptions, doneTicketPlan{})
	}
	return archiveWorkspaceRecord(command, service, workspace.ID, releaseOptions)
}

// archiveWorkspaceRecord archives one Workspace from outside its tmux session.
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
	options := workspaceservice.ReleaseOptions{Force: force, StopPanes: force, ExpectedFingerprint: plan.Fingerprint, Prevalidated: true}
	if plan.GitOperation != "" {
		return options, service.ValidateRelease(reference, "", options)
	}
	interactive := !isDryRun(command) && !WantsJSON(command) && interactiveTicketSession(command)
	if plan.Dirty && !force {
		if !interactive {
			return options, service.ValidateRelease(reference, "", options)
		}
		if !confirm(command, fmt.Sprintf("Discard uncommitted changes in Workspace %q?", plan.Name)) {
			return workspaceservice.ReleaseOptions{}, clierr.New(clierr.PreconditionFailed, "Workspace archive was canceled")
		}
		options.Force = true
	}
	if force {
		return options, nil
	}
	foreign, err := service.ForeignPanes(plan.WorkspaceID, os.Getenv("TMUX_PANE"))
	if err != nil || len(foreign) == 0 {
		return options, err
	}
	if !interactive {
		return options, service.ValidateRelease(reference, "", options)
	}
	if _, err := fmt.Fprintf(command.ErrOrStderr(), "These tmux panes outside the Workspace session still work in Workspace %q:\n", plan.Name); err != nil {
		return workspaceservice.ReleaseOptions{}, err
	}
	for _, pane := range foreign {
		if _, err := fmt.Fprintf(command.ErrOrStderr(), "  %s %s in session %q (%s)\n", pane.PaneID, pane.Command, pane.SessionName, pane.Path); err != nil {
			return workspaceservice.ReleaseOptions{}, err
		}
	}
	if !confirm(command, "Stop these panes?") {
		return workspaceservice.ReleaseOptions{}, clierr.New(clierr.PreconditionFailed, "Workspace archive was canceled")
	}
	options.StopPanes = true
	return options, nil
}

// confirm asks one yes or no question on stderr. The default answer is No.
func confirm(command *cobra.Command, question string) bool {
	if _, err := fmt.Fprintf(command.ErrOrStderr(), "%s [y/N] ", question); err != nil {
		return false
	}
	// Read one byte at a time: a buffered reader can consume the answer to
	// the next prompt when stdin is a pipe.
	var line []byte
	buffer := make([]byte, 1)
	for {
		n, err := command.InOrStdin().Read(buffer)
		if n == 1 {
			if buffer[0] == '\n' {
				break
			}
			line = append(line, buffer[0])
		}
		if err != nil {
			if err != io.EOF {
				return false
			}
			break
		}
	}
	answer := strings.ToLower(strings.TrimSpace(string(line)))
	return answer == "y" || answer == "yes"
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
