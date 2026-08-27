package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

const doneWorkerArgument = "__twt_done_worker"

// doneTransientSession prefixes the private session that hosts a done worker.
// The worker kills its session after a successful completion.
const doneTransientSession = "twt-done"

// noTransientSession marks a worker that runs in another Workspace session.
const noTransientSession = "-"

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
			relocate, err := relocationNeeded(command, options, service, workspace.ID, currentPane)
			if err != nil {
				return err
			}
			if relocate {
				return relocateAndComplete(command, options, service, workspace, currentPane, false, releaseOptions, ticketPlan)
			}
			return doneSynchronously(command, options, service, workspace.ID, currentPane, releaseOptions, ticketPlan)
		},
	}
	command.Flags().BoolVar(&force, "force", false, "Discard uncommitted changes and preserve ignored files")
	setArguments(command, optionalArgument("workspace", "the current Workspace when absent"))
	command.ValidArgsFunction = workspaceNameCompletion(service)
	return command
}

// relocationNeeded decides the shared inside-own-session policy for done and
// archive: the command must move the calling tmux client out of the Workspace
// session first, and JSON output cannot move a client.
func relocationNeeded(command *cobra.Command, options Options, service *workspaceservice.Service, workspaceID, currentPane string) (bool, error) {
	if !insideOwnedSession(options, service, workspaceID, currentPane) {
		return false, nil
	}
	if WantsJSON(command) {
		name := command.Name()
		return false, invalidUsage(command, "%s from inside the Workspace tmux session moves your tmux client and uses text output; run %s from a different session for JSON output", name, name)
	}
	return true, nil
}

// doneDryRun validates the archive and shows the removal plan without a
// change. It validates without the current pane because done relocates the
// tmux client before the archive.
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
// text session, before any relocation or mutation; the default answer is No.
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

// relocateAndComplete tells the user about the client relocation, then hands
// the release to the DoneRelocate hook. The real hook moves the tmux client
// and completes the work through a private worker session.
func relocateAndComplete(command *cobra.Command, options Options, service *workspaceservice.Service, workspace domain.Workspace, currentPane string, keep bool, opts workspaceservice.ReleaseOptions, ticketPlan doneTicketPlan) error {
	destination, found, err := latestOtherActiveWorkspace(service, workspace.ID)
	if err != nil {
		return err
	}
	verb := "Finishing"
	if keep {
		verb = "Archiving"
	}
	out := command.OutOrStdout()
	destinationID := ""
	if found {
		destinationID = destination.ID
		if _, err := fmt.Fprintf(out, "%s Workspace %q; switching the client to Workspace %q\n", verb, workspace.Name, destination.Name); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(out, "No other active Workspace exists. twt detaches the client."); err != nil {
		return err
	}
	if !ticketPlan.Close {
		for _, slug := range ticketPlan.Slugs {
			if _, err := fmt.Fprintf(out, "Run 'twt tickets close %s' when the work is complete.\n", slug); err != nil {
				return err
			}
		}
	}
	request := RelocationRequest{
		WorkspaceID:            workspace.ID,
		DestinationWorkspaceID: destinationID,
		Keep:                   keep,
		Force:                  opts.Force,
		Fingerprint:            opts.ExpectedFingerprint,
		CurrentPane:            currentPane,
	}
	if ticketPlan.Close {
		request.CloseTicket = ticketPlan.Slugs[0]
		request.CloseClaimant = ticketPlan.Claimant
	}
	return options.DoneRelocate(request)
}

// realDoneRelocate returns the tmux implementation of the DoneRelocate hook.
// It starts the done worker in a private session, moves the calling client to
// the destination session or detaches it, then signals the worker. The worker
// keeps its private window visible on failure.
func realDoneRelocate(options Options) func(RelocationRequest) error {
	return func(request RelocationRequest) error {
		service := options.workspaceService()
		retry := "twt done " + request.WorkspaceID
		if request.Keep {
			retry = "twt archive " + request.WorkspaceID
		}
		clientName, err := callingTmuxClient(options, request.CurrentPane)
		if err != nil {
			return err
		}
		targetSessionID := ""
		if request.DestinationWorkspaceID != "" {
			targetSessionID, err = destinationSessionID(service, request.DestinationWorkspaceID)
			if err != nil {
				return err
			}
		}
		transient, err := uniqueRelocationSessionName(doneTransientSession)
		if err != nil {
			return err
		}
		workerArgs := []string{
			request.WorkspaceID,
			workerBoolArg("keep", request.Keep),
			workerBoolArg("force", request.Force),
			workerValueArg(request.Fingerprint),
			transient,
			workerValueArg(request.CloseTicket),
			workerValueArg(request.CloseClaimant),
		}
		helper, err := startPrivateRelocationHelper(options, doneWorker, transient, clientName, workerArgs)
		if err != nil {
			return err
		}
		if request.DestinationWorkspaceID != "" {
			if err := switchTmuxClient(options, clientName, targetSessionID); err != nil {
				helper.cancel()
				return err
			}
		} else if err := runCommand("tmux", tmuxCommandArgs(options, "detach-client", "-t", clientName)...); err != nil {
			helper.cancel()
			return fmt.Errorf("detach the tmux client: %w", err)
		}
		if err := helper.commit(); err != nil {
			return fmt.Errorf("the done signal failed: %w; the Workspace did not change; run '%s' if the failure window appears", err, retry)
		}
		return nil
	}
}

// RunDoneWorker runs the private __twt_done_worker argv mode. It waits for
// the relocation signal and releases the Workspace. After a successful
// release, it closes the confirmed
// Ticket; a close failure only adds a warning with the close hint. On
// failure it keeps its remain-on-exit window visible.
func RunDoneWorker(options Options, args []string) error {
	if len(args) != 9 {
		return fmt.Errorf("invalid done worker request")
	}
	workspaceID, keepArg, forceArg := args[0], args[1], args[2]
	fingerprint, transient := parseWorkerValueArg(args[3]), args[4]
	closeTicket, closeClaimant := parseWorkerValueArg(args[5]), parseWorkerValueArg(args[6])
	channel, clientName := args[7], args[8]
	keep, err := parseWorkerBoolArg("keep", keepArg)
	if err != nil {
		return err
	}
	force, err := parseWorkerBoolArg("force", forceArg)
	if err != nil {
		return err
	}
	retry := "twt done " + workspaceID
	if keep {
		retry = "twt archive " + workspaceID
	}
	err = runRelocationWorker(options, doneWorker, workspaceID, channel, clientName, retry,
		workspaceservice.ReleaseOptions{Force: force, ExpectedFingerprint: fingerprint, Prevalidated: true},
		func(service *workspaceservice.Service, result workspaceservice.ArchiveResult) (string, error) {
			message := fmt.Sprintf("Finished Workspace %s; returned its worktrees to the prepared pool", result.Workspace.Name)
			if keep {
				message = fmt.Sprintf("Archived Workspace %s; returned its worktrees to the prepared pool", result.Workspace.Name)
			}
			if closeTicket == "" {
				return message, nil
			}
			if closeErr := closeDoneTicket(options, doneTicketPlan{Slugs: []string{closeTicket}, Claimant: closeClaimant}); closeErr != nil {
				printTicketCloseWarning(os.Stderr, closeTicket, closeErr)
				return message + fmt.Sprintf("; Ticket %s stays open: run 'twt tickets close %s'", closeTicket, closeTicket), nil
			}
			return message + fmt.Sprintf("; closed Ticket %s", closeTicket), nil
		})
	if err != nil {
		return err
	}
	if transient != noTransientSession && transient != "" {
		return runCommand("tmux", tmuxCommandArgs(options, "kill-session", "-t", "="+transient)...)
	}
	return clearRelocationWindow(options)
}

func workerBoolArg(name string, value bool) string {
	return fmt.Sprintf("%s=%t", name, value)
}

func parseWorkerBoolArg(name, value string) (bool, error) {
	switch value {
	case name + "=true":
		return true, nil
	case name + "=false":
		return false, nil
	}
	return false, fmt.Errorf("invalid done worker request")
}

// workerValueArg encodes one optional worker argv value. The "-" sentinel
// stands for the empty value, so the argv keeps a fixed length.
func workerValueArg(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func parseWorkerValueArg(value string) string {
	if value == "-" {
		return ""
	}
	return value
}

// latestOtherActiveWorkspace returns the most recently updated active Workspace
// that is not the given Workspace.
func latestOtherActiveWorkspace(service *workspaceservice.Service, workspaceID string) (domain.Workspace, bool, error) {
	workspaces, err := service.List()
	if err != nil {
		return domain.Workspace{}, false, err
	}
	var destination domain.Workspace
	found := false
	for _, candidate := range workspaces {
		if candidate.ID == workspaceID || candidate.Status != domain.WorkspaceActive {
			continue
		}
		if !found || candidate.UpdatedAt.After(destination.UpdatedAt) {
			destination = candidate
			found = true
		}
	}
	return destination, found, nil
}

// destinationSessionID returns the tmux session ID for a destination
// Workspace. It opens or repairs the session when necessary.
func destinationSessionID(service *workspaceservice.Service, workspaceID string) (string, error) {
	if sessionID, err := service.OwnedSessionID(workspaceID); err == nil {
		return sessionID, nil
	}
	if _, err := service.Open(workspaceID); err != nil {
		return "", err
	}
	return service.OwnedSessionID(workspaceID)
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
	return err == nil && owned == sessionID
}
