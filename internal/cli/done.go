package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

func newDoneCommand(options Options) *cobra.Command {
	service := options.workspaceService()
	var force bool
	command := &cobra.Command{
		Use:   "done [WORKSPACE]",
		Short: "Finish a Workspace and return its worktrees to the prepared pool",
		Args:  optionalArg("WORKSPACE"),
		RunE: func(command *cobra.Command, args []string) error {
			reference := currentWorkspaceReference
			if len(args) == 1 {
				reference = args[0]
			}
			workspace, err := resolveWorkspace(service, reference)
			if err != nil {
				return err
			}
			releaseOptions, err := releaseOptions(command, service, workspace.ID, force)
			if err != nil {
				return err
			}
			if isDryRun(command) {
				return doneDryRun(command, service, workspace.ID, releaseOptions)
			}
			ticketPlan := resolveDoneTicket(command, options, workspace)
			currentPane := os.Getenv("TMUX_PANE")
			fromCurrentSession, err := releaseFromCurrentSession(command, options, service, workspace.ID, currentPane)
			if err != nil {
				return err
			}
			if fromCurrentSession {
				return completeFromPane(command, options, service, workspace, currentPane, false, releaseOptions, ticketPlan)
			}
			return doneSynchronously(command, options, service, workspace.ID, currentPane, releaseOptions, ticketPlan)
		},
	}
	command.Flags().BoolVar(&force, "force", false, "Discard uncommitted changes and preserve ignored files")
	setArguments(command, optionalArgument("workspace", "the current Workspace when absent"))
	command.ValidArgsFunction = workspaceNameCompletion(service)
	return command
}

// releaseFromCurrentSession decides the shared in-session policy for done and
// archive. Tmux handles the client after twt stops the complete session.
func releaseFromCurrentSession(command *cobra.Command, options Options, service *workspaceservice.Service, workspaceID, currentPane string) (bool, error) {
	if !insideOwnedSession(options, service, workspaceID, currentPane) {
		return false, nil
	}
	if WantsJSON(command) {
		name := command.Name()
		return false, invalidUsage(command, "%s uses text output inside the Workspace tmux session. It stops that session after cleanup. Run %s from a different session for JSON output", name, name)
	}
	return true, nil
}

// doneDryRun validates the archive and shows the removal plan without a
// change. It validates without the current pane because a dry run does not
// stop the Workspace session.
func doneDryRun(command *cobra.Command, service *workspaceservice.Service, workspaceID string, opts workspaceservice.ReleaseOptions) error {
	return runMutation(command, "workspaces.done",
		func() (string, string, error) {
			workspace, err := service.Find(workspaceID)
			if err != nil {
				return "", workspaceID, err
			}
			return workspace.ID, workspace.Name, service.ValidateRelease(workspaceID, "", opts)
		},
		func() (string, string, error) { return "", "", nil },
		func(io.Writer, string, string) error { return nil })
}

// doneTicketPlan is the Ticket decision of one done run. An empty Slugs list
// means that done has no Ticket work: the Workspace links no open readable
// Ticket.
type doneTicketPlan struct {
	// Slugs are the linked open Tickets.
	Slugs []string
	// Close applies the confirmed close after a successful release. It can be
	// true only when Slugs contains one Ticket.
	Close bool
	// Claimant is the resolved claimant of the confirmed close.
	Claimant string
}

// resolveDoneTicket resolves the linked Ticket of the Workspace and asks the
// user whether done must close it. The prompt runs only in an interactive
// text session, before any mutation. The default answer is No.
func resolveDoneTicket(command *cobra.Command, options Options, workspace domain.Workspace) doneTicketPlan {
	if len(workspace.Tickets) == 0 {
		return doneTicketPlan{}
	}
	service, err := options.ticketService()
	if err != nil {
		return doneTicketPlan{}
	}
	plan := doneTicketPlan{}
	for _, slug := range workspace.Tickets {
		ticket, err := service.Resolve(slug)
		if err != nil || ticket.Status == domain.TicketDone || ticket.Status == domain.TicketWontfix {
			continue
		}
		plan.Slugs = append(plan.Slugs, ticket.Slug)
	}
	if len(plan.Slugs) == 0 {
		return doneTicketPlan{}
	}
	// done never chooses one Ticket from a set. The user must close each linked
	// Ticket explicitly after the Workspace is gone.
	if len(plan.Slugs) > 1 || WantsJSON(command) || !interactiveTicketSession(command) {
		return plan
	}
	slug := plan.Slugs[0]
	if _, err := fmt.Fprintf(command.ErrOrStderr(), "Close Ticket %q? [y/N] ", slug); err != nil {
		return plan
	}
	line, err := bufio.NewReader(command.InOrStdin()).ReadString('\n')
	if err != nil && err != io.EOF {
		return plan
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	if answer != "y" && answer != "yes" {
		return plan
	}
	claimant, err := resolveClaimant(command, "")
	if err != nil {
		printTicketCloseWarning(command.ErrOrStderr(), slug, err)
		return plan
	}
	plan.Close = true
	plan.Claimant = claimant
	return plan
}

// finishDoneTicket runs after a successful release. It closes the confirmed
// Ticket, or prints the close hint for an open one. A close failure warns and
// never fails done.
func finishDoneTicket(command *cobra.Command, options Options, plan doneTicketPlan) error {
	if len(plan.Slugs) == 0 {
		return nil
	}
	if !plan.Close {
		out := command.OutOrStdout()
		if WantsJSON(command) {
			out = command.ErrOrStderr()
		}
		for _, slug := range plan.Slugs {
			if _, err := fmt.Fprintf(out, "Run 'twt tickets close %s' when the work is complete.\n", slug); err != nil {
				return err
			}
		}
		return nil
	}
	if err := closeDoneTicket(options, plan); err != nil {
		printTicketCloseWarning(command.ErrOrStderr(), plan.Slugs[0], err)
		return nil
	}
	_, err := fmt.Fprintf(command.OutOrStdout(), "Closed Ticket %q\n", plan.Slugs[0])
	return err
}

// closeDoneTicket closes one confirmed Ticket through the close core.
func closeDoneTicket(options Options, plan doneTicketPlan) error {
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	_, err = service.Close(plan.Slugs[0], plan.Claimant, false)
	return err
}

func printTicketCloseWarning(out io.Writer, slug string, cause error) {
	_, _ = fmt.Fprintf(out, "Warning: twt could not close Ticket %q: %v. Run 'twt tickets close %s'.\n", slug, cause, slug)
}

// doneSynchronously releases the Workspace from the current process. The
// caller is outside the Workspace tmux session.
func doneSynchronously(command *cobra.Command, options Options, service *workspaceservice.Service, workspaceID, currentPane string, opts workspaceservice.ReleaseOptions, ticketPlan doneTicketPlan) error {
	result, err := service.Release(workspaceID, currentPane, opts)
	if err != nil {
		return err
	}
	out := command.OutOrStdout()
	if !WantsJSON(command) {
		if err := printStoppedAgents(out, result.StoppedAgents); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "Archived Workspace %q\n", result.Workspace.Name); err != nil {
			return err
		}
	}
	if WantsJSON(command) {
		if err := writeMutation(command, "workspaces.done", statusApplied, result.Workspace.ID, result.Workspace.Name); err != nil {
			return err
		}
		return finishDoneTicket(command, options, ticketPlan)
	}
	if _, err := fmt.Fprintf(out, "Finished Workspace %q. Its worktrees returned to the prepared pool.\n", result.Workspace.Name); err != nil {
		return err
	}
	return finishDoneTicket(command, options, ticketPlan)
}

// completeFromPane cleans the Workspace in the caller pane. It then stops
// the complete source session. Tmux switches to another session or detaches.
func completeFromPane(command *cobra.Command, options Options, service *workspaceservice.Service, workspace domain.Workspace, currentPane string, archiveOnly bool, opts workspaceservice.ReleaseOptions, ticketPlan doneTicketPlan) error {
	verb := "Finishing"
	if archiveOnly {
		verb = "Archiving"
	}
	out := command.OutOrStdout()
	if _, err := fmt.Fprintf(out, "%s Workspace %q. Cleanup runs before tmux stops this session.\n", verb, workspace.Name); err != nil {
		return err
	}
	prepared, err := service.PrepareReleaseFromPane(workspace.ID, currentPane, opts)
	if err != nil {
		return err
	}
	return finishPreparedRelease(service, prepared, func() error {
		if err := printStoppedAgents(out, prepared.ArchiveResult.StoppedAgents); err != nil {
			return err
		}
		if err := finishDoneTicket(command, options, ticketPlan); err != nil {
			return err
		}
		noun := "Finished"
		if archiveOnly {
			noun = "Archived"
		}
		if _, err := fmt.Fprintf(out, "%s Workspace %q. Cleanup is complete.\n", noun, workspace.Name); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "Tmux will now stop this session. It can select another session or detach this client.")
		return err
	})
}

// finishPreparedRelease stops the source session even when result reporting
// fails. This rule prevents a prepared release from leaving a live shell.
func finishPreparedRelease(service *workspaceservice.Service, prepared workspaceservice.PreparedRelease, report func() error) error {
	reportErr := report()
	stopErr := service.StopPreparedRelease(prepared)
	if stopErr != nil {
		return errors.Join(reportErr, stopErr)
	}
	return errors.Join(reportErr, service.Reconcile())
}

// insideOwnedSession reports whether the current pane is inside the tmux
// session that the Workspace owns.
func insideOwnedSession(options Options, service *workspaceservice.Service, workspaceID, currentPane string) bool {
	if currentPane == "" {
		return false
	}
	sessionID, err := commandOutput("tmux", tmuxCommandArgs(options, "display-message", "-p", "-t", currentPane, "#{session_id}")...)
	if err != nil || sessionID == "" {
		return false
	}
	owned, err := service.OwnedSessionID(workspaceID)
	if err == nil && owned == sessionID {
		return true
	}
	// A tmux restore clears the session owner option. Adopt the session back
	// when it is unowned and uses the saved Workspace session name, so done
	// and archive still stop it.
	adopted, adoptErr := service.AdoptUnownedSession(workspaceID, sessionID)
	return adoptErr == nil && adopted
}
