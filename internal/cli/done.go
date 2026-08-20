package cli

import (
	"fmt"
	"os"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/spf13/cobra"
)

const doneWorkerArgument = "__twt2_done_worker"

// doneTransientSession hosts the done worker when no other active Project
// exists. The worker kills this session after a successful completion.
const doneTransientSession = "twt2-done"

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
			currentPane := os.Getenv("TMUX_PANE")
			relocate, err := relocationNeeded(command, options, service, project.ID, currentPane)
			if err != nil {
				return err
			}
			if relocate {
				return relocateAndComplete(command, options, service, project, currentPane, keep, removalOptions)
			}
			return doneSynchronously(command, service, project.ID, currentPane, keep, removalOptions)
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

// doneSynchronously archives and removes the Project from the current
// process. The caller is outside the Project tmux session.
func doneSynchronously(command *cobra.Command, service *projectservice.Service, projectID, currentPane string, keep bool, opts projectservice.RemovalOptions) error {
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
			return writeMutation(command, "projects.done", "archived", result.Project.ID, result.Project.Name)
		}
		return nil
	}
	plan, err := service.Remove(projectID, currentPane, opts)
	if err != nil {
		if !WantsJSON(command) && len(plan.Blockers) > 0 {
			if err := printRemovalBlockers(out, plan.Blockers, ""); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "The removal is blocked. Project %q stays archived. Correct the causes above, then run 'twt2 done %s' again.\n", plan.ProjectName, plan.ProjectID); err != nil {
				return err
			}
		}
		return err
	}
	if WantsJSON(command) {
		return writeJSONOutput(command, removalOutput{SchemaVersion: jsonSchemaVersion, Applied: true, Plan: plan, Blockers: plan.Blockers})
	}
	for _, worktree := range plan.Worktrees {
		if _, err := fmt.Fprintf(out, "Removed worktree %s\n", worktree); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(out, "Removed Project %q. Reclaimed %s.\n", plan.ProjectName, formatBytes(plan.Bytes))
	return err
}

// relocateAndComplete tells the user about the client relocation, then hands
// the archive or removal to the DoneRelocate hook. The real hook moves the
// tmux client and completes the work through a worker window in the
// destination session. With keep, the worker only archives; archive from
// inside the Project session behaves like done --keep.
func relocateAndComplete(command *cobra.Command, options Options, service *projectservice.Service, project domain.Project, currentPane string, keep bool, opts projectservice.RemovalOptions) error {
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
	} else if _, err := fmt.Fprintln(out, "No other active Project exists. twt2 detaches the client."); err != nil {
		return err
	}
	return options.DoneRelocate(RelocationRequest{
		ProjectID:            project.ID,
		DestinationProjectID: destinationID,
		Keep:                 keep,
		AllowUnpublished:     opts.AllowUnpublished,
		CurrentPane:          currentPane,
	})
}

// realDoneRelocate returns the tmux implementation of the DoneRelocate hook.
// It starts the done worker in the destination session, moves the calling
// client there or detaches it, then signals the worker. The worker archives
// the Project, removes it unless the request keeps it, and keeps its window
// visible on failure.
func realDoneRelocate(options Options) func(RelocationRequest) error {
	return func(request RelocationRequest) error {
		service := options.projectService()
		retry := "twt2 done " + request.ProjectID
		if request.Keep {
			retry = "twt2 archive " + request.ProjectID
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

// RunDoneWorker runs the private __twt2_done_worker argv mode. It waits for
// the relocation signal, archives the Project, and removes it unless the
// keep flag is set. On failure it keeps its remain-on-exit window visible.
func RunDoneWorker(options Options, args []string) error {
	if len(args) != 6 {
		return fmt.Errorf("invalid done worker request")
	}
	projectID, keepArg, allowArg, transient, channel, clientName := args[0], args[1], args[2], args[3], args[4], args[5]
	keep, err := parseWorkerBoolArg("keep", keepArg)
	if err != nil {
		return err
	}
	allowUnpublished, err := parseWorkerBoolArg("allow-unpublished", allowArg)
	if err != nil {
		return err
	}
	retry := "twt2 done " + projectID
	if keep {
		retry = "twt2 archive " + projectID
	}
	err = runRelocationWorker(options, doneWorker, projectID, channel, clientName, retry,
		func(service *projectservice.Service, result projectservice.ArchiveResult) (string, error) {
			if keep {
				return fmt.Sprintf("Archived Project %s", result.Project.Name), nil
			}
			plan, removeErr := service.Remove(projectID, os.Getenv("TMUX_PANE"), projectservice.RemovalOptions{AllowUnpublished: allowUnpublished})
			if removeErr != nil {
				return "", fmt.Errorf("%w; Project %q stays archived; run 'twt2 done %s' to retry", removeErr, result.Project.Name, projectID)
			}
			return fmt.Sprintf("Finished Project %s; reclaimed %s", plan.ProjectName, formatBytes(plan.Bytes)), nil
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
