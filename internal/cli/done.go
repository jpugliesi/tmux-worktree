package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/spf13/cobra"
)

const doneWorkerArgument = "__twt_done_worker"

// doneTransientSession hosts the done worker when no other active Project
// exists. The worker kills this session after a successful completion.
const doneTransientSession = "twt-done"

// noTransientSession marks a worker that runs in another Project session.
const noTransientSession = "-"

func newDoneCommand(options Options) *cobra.Command {
	service := options.projectService()
	var keep bool
	var allowUnpublished bool
	command := &cobra.Command{
		Use:   "done [PROJECT]",
		Short: "Archive a Project and remove its data",
		Args:  optionalArg("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			reference := currentProjectReference
			if len(args) == 1 {
				reference = args[0]
			}
			project, err := resolveProject(service, reference)
			if err != nil {
				return err
			}
			removalOptions := projectservice.RemovalOptions{AllowUnpublished: allowUnpublished}
			if isDryRun(command) {
				return doneDryRun(command, service, project.ID, removalOptions)
			}
			ticketPlan := resolveDoneTicket(command, options, project, keep)
			currentPane := os.Getenv("TMUX_PANE")
			relocate, err := relocationNeeded(command, options, service, project.ID, currentPane)
			if err != nil {
				return err
			}
			if relocate {
				return relocateAndComplete(command, options, service, project, currentPane, keep, removalOptions, ticketPlan)
			}
			return doneSynchronously(command, options, service, project.ID, currentPane, keep, removalOptions, ticketPlan)
		},
	}
	command.Flags().BoolVar(&keep, "keep", false, "Stop after the archive and keep the Project data")
	command.Flags().BoolVar(&allowUnpublished, "allow-unpublished", false, "Remove a branch with unpublished commits")
	setArguments(command, optionalArgument("project", "the current Project when absent"))
	command.ValidArgsFunction = projectNameCompletion(service)
	return command
}

// relocationNeeded decides the shared inside-own-session policy for done and
// archive: the command must move the calling tmux client out of the Project
// session first, and JSON output cannot move a client.
func relocationNeeded(command *cobra.Command, options Options, service *projectservice.Service, projectID, currentPane string) (bool, error) {
	if !insideOwnedSession(options, service, projectID, currentPane) {
		return false, nil
	}
	if WantsJSON(command) {
		name := command.Name()
		return false, invalidUsage(command, "%s from inside the Project tmux session moves your tmux client and uses text output; run %s from a different session for JSON output", name, name)
	}
	return true, nil
}

// doneDryRun validates the archive and shows the removal plan without a
// change. It validates without the current pane because done relocates the
// tmux client before the archive.
func doneDryRun(command *cobra.Command, service *projectservice.Service, projectID string, opts projectservice.RemovalOptions) error {
	if err := service.ValidateArchive(projectID, ""); err != nil {
		return err
	}
	plan, err := service.PlanRemoval(projectID, "", opts)
	if err != nil {
		return err
	}
	// Done archives the Project before removal, so the not_archived blocker
	// does not apply.
	blockers := plan.Blockers[:0]
	for _, blocker := range plan.Blockers {
		if blocker.Code != "not_archived" {
			blockers = append(blockers, blocker)
		}
	}
	plan.Blockers = blockers
	if WantsJSON(command) {
		return writeJSONOutput(command, removalOutput{SchemaVersion: jsonSchemaVersion, Applied: false, Plan: plan, Blockers: plan.Blockers})
	}
	out := command.OutOrStdout()
	if _, err := fmt.Fprintf(out, "Archive of Project %q is valid.\n", plan.ProjectName); err != nil {
		return err
	}
	return printRemovalPlanText(out, plan, false)
}

// doneTicketPlan is the Ticket decision of one done run. An empty Slug means
// that done has no Ticket work: the Project links no Ticket, or that Ticket
// is already closed or unreadable.
type doneTicketPlan struct {
	// Slug is the linked open Ticket.
	Slug string
	// Close applies the confirmed close after a successful removal.
	Close bool
	// Claimant is the resolved claimant of the confirmed close.
	Claimant string
}

// resolveDoneTicket resolves the linked Ticket of the Project and asks the
// user whether done must close it. The prompt runs only in an interactive
// text session, before any relocation or mutation; the default answer is No.
// --keep never prompts, because the work is not complete after an archive.
func resolveDoneTicket(command *cobra.Command, options Options, project domain.Project, keep bool) doneTicketPlan {
	if project.Ticket == "" {
		return doneTicketPlan{}
	}
	service, err := options.ticketService()
	if err != nil {
		return doneTicketPlan{}
	}
	ticket, err := service.Resolve(project.Ticket)
	if err != nil || ticket.Status == domain.TicketDone || ticket.Status == domain.TicketWontfix {
		return doneTicketPlan{}
	}
	plan := doneTicketPlan{Slug: ticket.Slug}
	if keep || WantsJSON(command) || !interactiveTicketSession(command) {
		return plan
	}
	if _, err := fmt.Fprintf(command.ErrOrStderr(), "Close Ticket %q? [y/N] ", ticket.Slug); err != nil {
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
		printTicketCloseWarning(command.ErrOrStderr(), ticket.Slug, err)
		return plan
	}
	plan.Close = true
	plan.Claimant = claimant
	return plan
}

// finishDoneTicket runs after a successful removal: it closes the confirmed
// Ticket, or prints the close hint for an open one. A close failure warns and
// never fails done.
func finishDoneTicket(command *cobra.Command, options Options, plan doneTicketPlan) error {
	if plan.Slug == "" {
		return nil
	}
	if !plan.Close {
		out := command.OutOrStdout()
		if WantsJSON(command) {
			out = command.ErrOrStderr()
		}
		_, err := fmt.Fprintf(out, "Run 'twt tickets close %s' when the work is complete.\n", plan.Slug)
		return err
	}
	if err := closeDoneTicket(options, plan); err != nil {
		printTicketCloseWarning(command.ErrOrStderr(), plan.Slug, err)
		return nil
	}
	_, err := fmt.Fprintf(command.OutOrStdout(), "Closed Ticket %q\n", plan.Slug)
	return err
}

// closeDoneTicket closes one confirmed Ticket through the close core.
func closeDoneTicket(options Options, plan doneTicketPlan) error {
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	_, err = service.Close(plan.Slug, plan.Claimant, false)
	return err
}

func printTicketCloseWarning(out io.Writer, slug string, cause error) {
	_, _ = fmt.Fprintf(out, "Warning: twt could not close Ticket %q: %v. Run 'twt tickets close %s'.\n", slug, cause, slug)
}

// doneSynchronously archives and removes the Project from the current
// process. The caller is outside the Project tmux session.
func doneSynchronously(command *cobra.Command, options Options, service *projectservice.Service, projectID, currentPane string, keep bool, opts projectservice.RemovalOptions, ticketPlan doneTicketPlan) error {
	result, err := service.Archive(projectID, currentPane)
	if err != nil {
		return err
	}
	out := command.OutOrStdout()
	if !WantsJSON(command) {
		if err := printStoppedAgents(out, result.StoppedAgents); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(out, "Archived Project %q\n", result.Project.Name); err != nil {
			return err
		}
	}
	if keep {
		if WantsJSON(command) {
			if err := writeMutation(command, "projects.done", "archived", result.Project.ID, result.Project.Name); err != nil {
				return err
			}
		}
		return finishDoneTicket(command, options, ticketPlan)
	}
	plan, err := service.Remove(projectID, currentPane, opts)
	if err != nil {
		if !WantsJSON(command) && len(plan.Blockers) > 0 {
			if err := printRemovalBlockers(out, plan.Blockers, ""); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "The removal is blocked. Project %q stays archived. Correct the causes above, then run 'twt done %s' again.\n", plan.ProjectName, plan.ProjectID); err != nil {
				return err
			}
		}
		return err
	}
	if WantsJSON(command) {
		if err := writeJSONOutput(command, removalOutput{SchemaVersion: jsonSchemaVersion, Applied: true, Plan: plan, Blockers: plan.Blockers}); err != nil {
			return err
		}
		return finishDoneTicket(command, options, ticketPlan)
	}
	for _, worktree := range plan.Worktrees {
		if _, err := fmt.Fprintf(out, "Removed worktree %s\n", worktree); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(out, "Removed Project %q. Reclaimed %s.\n", plan.ProjectName, formatBytes(plan.Bytes)); err != nil {
		return err
	}
	return finishDoneTicket(command, options, ticketPlan)
}

// relocateAndComplete tells the user about the client relocation, then hands
// the archive or removal to the DoneRelocate hook. The real hook moves the
// tmux client and completes the work through a worker window in the
// destination session. With keep, the worker only archives; archive from
// inside the Project session behaves like done --keep.
func relocateAndComplete(command *cobra.Command, options Options, service *projectservice.Service, project domain.Project, currentPane string, keep bool, opts projectservice.RemovalOptions, ticketPlan doneTicketPlan) error {
	destination, found, err := latestOtherActiveProject(service, project.ID)
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
		if _, err := fmt.Fprintf(out, "%s Project %q; switching the client to Project %q\n", verb, project.Name, destination.Name); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(out, "No other active Project exists. twt detaches the client."); err != nil {
		return err
	}
	if ticketPlan.Slug != "" && !ticketPlan.Close {
		if _, err := fmt.Fprintf(out, "Run 'twt tickets close %s' when the work is complete.\n", ticketPlan.Slug); err != nil {
			return err
		}
	}
	request := RelocationRequest{
		ProjectID:            project.ID,
		DestinationProjectID: destinationID,
		Keep:                 keep,
		AllowUnpublished:     opts.AllowUnpublished,
		CurrentPane:          currentPane,
	}
	if ticketPlan.Close {
		request.CloseTicket = ticketPlan.Slug
		request.CloseClaimant = ticketPlan.Claimant
	}
	return options.DoneRelocate(request)
}

// realDoneRelocate returns the tmux implementation of the DoneRelocate hook.
// It starts the done worker in the destination session, moves the calling
// client there or detaches it, then signals the worker. The worker archives
// the Project, removes it unless the request keeps it, and keeps its window
// visible on failure.
func realDoneRelocate(options Options) func(RelocationRequest) error {
	return func(request RelocationRequest) error {
		service := options.projectService()
		retry := "twt done " + request.ProjectID
		if request.Keep {
			retry = "twt archive " + request.ProjectID
		}
		clientName, err := callingTmuxClient(options, request.CurrentPane)
		if err != nil {
			return err
		}
		hostSessionID := ""
		transient := noTransientSession
		if request.DestinationProjectID != "" {
			hostSessionID, err = destinationSessionID(service, request.DestinationProjectID)
			if err != nil {
				return err
			}
		} else {
			hostSessionID, err = ensureTransientDoneSession(options)
			if err != nil {
				return err
			}
			transient = doneTransientSession
		}
		workerArgs := []string{
			request.ProjectID,
			workerBoolArg("keep", request.Keep),
			workerBoolArg("allow-unpublished", request.AllowUnpublished),
			transient,
			workerValueArg(request.CloseTicket),
			workerValueArg(request.CloseClaimant),
		}
		helper, err := startRelocationHelper(options, doneWorker, hostSessionID, clientName, workerArgs)
		if err != nil {
			return err
		}
		if request.DestinationProjectID != "" {
			if err := switchTmuxClient(options, clientName, hostSessionID); err != nil {
				helper.cancel()
				return err
			}
		} else if err := runCommand("tmux", tmuxCommandArgs(options, "detach-client", "-t", clientName)...); err != nil {
			helper.cancel()
			return fmt.Errorf("detach the tmux client: %w", err)
		}
		if err := helper.commit(); err != nil {
			return fmt.Errorf("the done signal failed: %w; the Project did not change; run '%s' if the failure window appears", err, retry)
		}
		return nil
	}
}

// RunDoneWorker runs the private __twt_done_worker argv mode. It waits for
// the relocation signal, archives the Project, and removes it unless the
// keep flag is set. After a successful removal it closes the confirmed
// Ticket; a close failure only adds a warning with the close hint. On
// failure it keeps its remain-on-exit window visible.
func RunDoneWorker(options Options, args []string) error {
	if len(args) != 8 {
		return fmt.Errorf("invalid done worker request")
	}
	projectID, keepArg, allowArg, transient := args[0], args[1], args[2], args[3]
	closeTicket, closeClaimant := parseWorkerValueArg(args[4]), parseWorkerValueArg(args[5])
	channel, clientName := args[6], args[7]
	keep, err := parseWorkerBoolArg("keep", keepArg)
	if err != nil {
		return err
	}
	allowUnpublished, err := parseWorkerBoolArg("allow-unpublished", allowArg)
	if err != nil {
		return err
	}
	retry := "twt done " + projectID
	if keep {
		retry = "twt archive " + projectID
	}
	err = runRelocationWorker(options, doneWorker, projectID, channel, clientName, retry,
		func(service *projectservice.Service, result projectservice.ArchiveResult) (string, error) {
			if keep {
				return fmt.Sprintf("Archived Project %s", result.Project.Name), nil
			}
			plan, removeErr := service.Remove(projectID, os.Getenv("TMUX_PANE"), projectservice.RemovalOptions{AllowUnpublished: allowUnpublished})
			if removeErr != nil {
				return "", fmt.Errorf("%w; Project %q stays archived; run 'twt done %s' to retry", removeErr, result.Project.Name, projectID)
			}
			message := fmt.Sprintf("Finished Project %s; reclaimed %s", plan.ProjectName, formatBytes(plan.Bytes))
			if closeTicket == "" {
				return message, nil
			}
			if closeErr := closeDoneTicket(options, doneTicketPlan{Slug: closeTicket, Claimant: closeClaimant}); closeErr != nil {
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

// latestOtherActiveProject returns the most recently updated active Project
// that is not the given Project.
func latestOtherActiveProject(service *projectservice.Service, projectID string) (domain.Project, bool, error) {
	projects, err := service.List()
	if err != nil {
		return domain.Project{}, false, err
	}
	var destination domain.Project
	found := false
	for _, candidate := range projects {
		if candidate.ID == projectID || candidate.Status != domain.ProjectActive {
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
// Project. It opens or repairs the session when necessary.
func destinationSessionID(service *projectservice.Service, projectID string) (string, error) {
	if sessionID, err := service.OwnedSessionID(projectID); err == nil {
		return sessionID, nil
	}
	if _, err := service.Open(projectID); err != nil {
		return "", err
	}
	return service.OwnedSessionID(projectID)
}

func ensureTransientDoneSession(options Options) (string, error) {
	sessionID, err := commandOutput("tmux", tmuxCommandArgs(options, "display-message", "-p", "-t", "="+doneTransientSession, "#{session_id}")...)
	if err == nil && sessionID != "" {
		return sessionID, nil
	}
	sessionID, err = commandOutput("tmux", tmuxCommandArgs(options, "new-session", "-d", "-P", "-F", "#{session_id}", "-s", doneTransientSession)...)
	if err != nil {
		return "", fmt.Errorf("create the transient done session: %w", err)
	}
	return sessionID, nil
}

// insideOwnedSession reports whether the current pane is inside the tmux
// session that the Project owns.
func insideOwnedSession(options Options, service *projectservice.Service, projectID, currentPane string) bool {
	if currentPane == "" {
		return false
	}
	sessionID, err := commandOutput("tmux", tmuxCommandArgs(options, "display-message", "-p", "-t", currentPane, "#{session_id}")...)
	if err != nil || sessionID == "" {
		return false
	}
	owned, err := service.OwnedSessionID(projectID)
	return err == nil && owned == sessionID
}
