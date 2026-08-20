package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/spf13/cobra"
)

const finishWorkerArgument = "__twt2_finish_worker"

// finishTransientSession hosts the finish worker when no other active
// Project exists. The worker kills this session after a successful finish.
const finishTransientSession = "twt2-finish"

// noTransientSession marks a worker that runs in another Project session.
const noTransientSession = "-"

func newFinishCommand(options Options) *cobra.Command {
	service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
	var keep bool
	var allowUnpublished bool
	command := &cobra.Command{
		Use:   "finish [PROJECT]",
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
				return finishDryRun(command, service, project.ID, removalOptions)
			}
			currentPane := os.Getenv("TMUX_PANE")
			if insideOwnedSession(options, service, project.ID, currentPane) {
				if WantsJSON(command) {
					return invalidUsage(command, "finish from inside the Project tmux session moves your tmux client and uses text output; run finish from a different session for JSON output")
				}
				if options.FinishRelocate != nil || terminalWriter(command.OutOrStdout()) {
					return finishWithRelocation(command, options, service, project, currentPane, keep, removalOptions)
				}
			}
			return finishSynchronously(command, service, project.ID, currentPane, keep, removalOptions)
		},
	}
	command.Flags().BoolVar(&keep, "keep", false, "Stop after the archive and keep the Project data")
	command.Flags().BoolVar(&allowUnpublished, "allow-unpublished", false, "Remove a branch with unpublished commits")
	setArguments(command, optionalArgument("project", "the current Project when absent"))
	command.ValidArgsFunction = projectNameCompletion(service)
	return command
}

// finishDryRun validates the archive and shows the removal plan without a
// change. It validates without the current pane because finish relocates the
// tmux client before the archive.
func finishDryRun(command *cobra.Command, service *projectservice.Service, projectID string, opts projectservice.RemovalOptions) error {
	if err := service.ValidateArchive(projectID, ""); err != nil {
		return err
	}
	plan, err := service.PlanRemoval(projectID, "", opts)
	if err != nil {
		return err
	}
	// Finish archives the Project before removal, so the not_archived
	// blocker does not apply.
	blockers := plan.Blockers[:0]
	for _, blocker := range plan.Blockers {
		if blocker.Code != "not_archived" {
			blockers = append(blockers, blocker)
		}
	}
	plan.Blockers = blockers
	if WantsJSON(command) {
		return writeJSONOutput(command, removalOutput{SchemaVersion: jsonSchemaVersion, Applied: false, Plan: plan, Blockers: plan.Blockers, Bytes: plan.Bytes})
	}
	out := command.OutOrStdout()
	if _, err := fmt.Fprintf(out, "Archive of Project %q is valid.\n", plan.ProjectName); err != nil {
		return err
	}
	return printRemovalPlanText(out, plan, false)
}

// finishSynchronously archives and removes the Project from the current
// process. The caller is outside the Project tmux session.
func finishSynchronously(command *cobra.Command, service *projectservice.Service, projectID, currentPane string, keep bool, opts projectservice.RemovalOptions) error {
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
			return writeMutation(command, "projects.finish", "archived", result.Project.ID, result.Project.Name)
		}
		return nil
	}
	plan, err := service.Remove(projectID, currentPane, opts)
	if err != nil {
		if !WantsJSON(command) && len(plan.Blockers) > 0 {
			if err := printRemovalBlockers(out, plan.Blockers, ""); err != nil {
				return err
			}
			if _, err := fmt.Fprintf(out, "The removal is blocked. Project %q stays archived. Correct the causes above, then run 'twt2 finish %s' again.\n", plan.ProjectName, plan.ProjectID); err != nil {
				return err
			}
		}
		return err
	}
	if WantsJSON(command) {
		return writeJSONOutput(command, removalOutput{SchemaVersion: jsonSchemaVersion, Applied: true, Plan: plan, Blockers: plan.Blockers, Bytes: plan.Bytes})
	}
	for _, worktree := range plan.Worktrees {
		if _, err := fmt.Fprintf(out, "Removed worktree %s\n", worktree); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(out, "Removed Project %q. Reclaimed %s.\n", plan.ProjectName, formatBytes(plan.Bytes))
	return err
}

// finishWithRelocation moves the tmux client out of the Project session,
// then archives and removes the Project through a worker window in the
// destination session. With keep, the worker only archives.
func finishWithRelocation(command *cobra.Command, options Options, service *projectservice.Service, project domain.Project, currentPane string, keep bool, opts projectservice.RemovalOptions) error {
	destination, found, err := latestOtherActiveProject(service, project.ID)
	if err != nil {
		return err
	}
	if options.FinishRelocate != nil {
		destinationID := ""
		if found {
			destinationID = destination.ID
		}
		if err := options.FinishRelocate(destinationID); err != nil {
			return err
		}
		return finishSynchronously(command, service, project.ID, "", keep, opts)
	}
	clientName, err := callingTmuxClient(options, currentPane)
	if err != nil {
		return err
	}
	hostSessionID := ""
	transient := noTransientSession
	if found {
		hostSessionID, err = destinationSessionID(service, destination.ID)
		if err != nil {
			return err
		}
	} else {
		hostSessionID, err = ensureTransientFinishSession(options)
		if err != nil {
			return err
		}
		transient = finishTransientSession
	}
	workerArgs := []string{finishWorkerArgument, project.ID, finishBoolArg("keep", keep), finishBoolArg("allow-unpublished", opts.AllowUnpublished), transient}
	helper, err := startRelocationHelper(options, hostSessionID, clientName, workerArgs)
	if err != nil {
		return err
	}
	verb := "Finishing"
	retry := "twt2 finish " + project.ID
	if keep {
		verb = "Archiving"
		retry = "twt2 archive " + project.ID
	}
	out := command.OutOrStdout()
	if found {
		if _, err := fmt.Fprintf(out, "%s Project %q; switching the client to Project %q\n", verb, project.Name, destination.Name); err != nil {
			helper.cancel()
			return err
		}
		if err := switchTmuxClient(options, clientName, hostSessionID); err != nil {
			helper.cancel()
			return err
		}
	} else {
		if _, err := fmt.Fprintln(out, "No other active Project exists. twt2 detached the client."); err != nil {
			helper.cancel()
			return err
		}
		if err := runCommand("tmux", tmuxCommandArgs(options, "detach-client", "-t", clientName)...); err != nil {
			helper.cancel()
			return fmt.Errorf("detach the tmux client: %w", err)
		}
	}
	if err := helper.commit(); err != nil {
		return fmt.Errorf("the finish signal failed: %w; Project %q did not change; run '%s' if the failure window appears", err, project.Name, retry)
	}
	return nil
}

// RunFinishWorker runs the private __twt2_finish_worker argv mode. It waits
// for the relocation signal, archives the Project, and removes it unless the
// keep flag is set. On failure it keeps its remain-on-exit window visible.
func RunFinishWorker(options Options, args []string) error {
	if len(args) != 6 {
		return fmt.Errorf("invalid finish worker request")
	}
	projectID, keepArg, allowArg, transient, channel, clientName := args[0], args[1], args[2], args[3], args[4], args[5]
	keep, err := parseFinishBoolArg("keep", keepArg)
	if err != nil {
		return err
	}
	allowUnpublished, err := parseFinishBoolArg("allow-unpublished", allowArg)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(channel, "twt2-finish-") {
		return fmt.Errorf("invalid finish signal")
	}
	retry := "twt2 finish " + projectID
	if keep {
		retry = "twt2 archive " + projectID
	}
	if err := waitForRelocationSignal(options, channel); err != nil {
		showRelocationFailureWindow(options, clientName, "finish-failed")
		return fmt.Errorf("finish signal timed out: %w; the Project did not change; run '%s' to retry", err, retry)
	}
	service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
	result := projectservice.ArchiveResult{}
	_, err = service.Find(projectID)
	if err == nil {
		result, err = service.Archive(projectID, os.Getenv("TMUX_PANE"))
	}
	if err != nil {
		showRelocationFailureWindow(options, clientName, "finish-failed")
		return fmt.Errorf("archive Project: %w; run '%s' to retry", err, retry)
	}
	message := fmt.Sprintf("Archived Project %s", result.Project.Name)
	if !keep {
		plan, removeErr := service.Remove(projectID, os.Getenv("TMUX_PANE"), projectservice.RemovalOptions{AllowUnpublished: allowUnpublished})
		if removeErr != nil {
			showRelocationFailureWindow(options, clientName, "finish-failed")
			return fmt.Errorf("%w; Project %q stays archived; run 'twt2 finish %s' to retry", removeErr, result.Project.Name, projectID)
		}
		message = fmt.Sprintf("Finished Project %s; reclaimed %s", plan.ProjectName, formatBytes(plan.Bytes))
	}
	_ = runCommand("tmux", tmuxCommandArgs(options, "display-message", "-c", clientName, message)...)
	if transient != noTransientSession && transient != "" {
		return runCommand("tmux", tmuxCommandArgs(options, "kill-session", "-t", "="+transient)...)
	}
	return runCommand("tmux", tmuxCommandArgs(options, "set-option", "-w", "-t", os.Getenv("TMUX_PANE"), "remain-on-exit", "off")...)
}

func finishBoolArg(name string, value bool) string {
	return fmt.Sprintf("%s=%t", name, value)
}

func parseFinishBoolArg(name, value string) (bool, error) {
	switch value {
	case name + "=true":
		return true, nil
	case name + "=false":
		return false, nil
	}
	return false, fmt.Errorf("invalid finish worker request")
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

func ensureTransientFinishSession(options Options) (string, error) {
	sessionID, err := commandOutput("tmux", tmuxCommandArgs(options, "display-message", "-p", "-t", "="+finishTransientSession, "#{session_id}")...)
	if err == nil && sessionID != "" {
		return sessionID, nil
	}
	sessionID, err = commandOutput("tmux", tmuxCommandArgs(options, "new-session", "-d", "-P", "-F", "#{session_id}", "-s", finishTransientSession)...)
	if err != nil {
		return "", fmt.Errorf("create the transient finish session: %w", err)
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

// terminalWriter reports whether the writer is an interactive terminal.
func terminalWriter(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
