package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/spf13/cobra"
)

func newProjectsArchiveCommand(options Options, service *projectservice.Service) *cobra.Command {
	command := &cobra.Command{
		Use:   "archive PROJECT",
		Short: "Archive a Project without removing its data",
		Args:  exactArgs("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			reference, err := resolveProjectReference(service, args[0])
			if err != nil {
				return err
			}
			return archiveProject(command, options, service, reference)
		},
	}
	setArguments(command, requiredArgument("project"))
	command.ValidArgsFunction = projectNameCompletion(service)
	return command
}

func newArchiveCommand(options Options) *cobra.Command {
	service := options.projectService()
	command := &cobra.Command{
		Use:   "archive [PROJECT]",
		Short: "Archive the current Project or a specified Project",
		Args:  optionalArg("PROJECT"),
		RunE: func(command *cobra.Command, args []string) error {
			reference := currentProjectReference
			if len(args) == 1 {
				reference = args[0]
			}
			reference, err := resolveProjectReference(service, reference)
			if err != nil {
				return err
			}
			return archiveProject(command, options, service, reference)
		},
	}
	setArguments(command, optionalArgument("project", "the current Project when absent"))
	command.ValidArgsFunction = projectNameCompletion(service)
	return command
}

// archiveProject archives one Project. Inside the Project tmux session it
// relocates the calling client first and behaves like done --keep.
func archiveProject(command *cobra.Command, options Options, service *projectservice.Service, reference string) error {
	if isDryRun(command) {
		return archiveProjectRecord(command, service, reference)
	}
	project, err := service.Find(reference)
	if err != nil {
		return err
	}
	currentPane := os.Getenv("TMUX_PANE")
	relocate, err := relocationNeeded(command, options, service, project.ID, currentPane)
	if err != nil {
		return err
	}
	if relocate {
		return relocateAndComplete(command, options, service, project, currentPane, true, projectservice.RemovalOptions{}, doneTicketPlan{})
	}
	return archiveProjectRecord(command, service, project.ID)
}

// archiveProjectRecord archives one Project without tmux client relocation.
// The apply command uses it directly.
func archiveProjectRecord(command *cobra.Command, service *projectservice.Service, reference string) error {
	result := projectservice.ArchiveResult{}
	return runMutation(command, "projects.archive",
		func() (string, string, error) {
			return "", reference, service.ValidateArchive(reference, os.Getenv("TMUX_PANE"))
		},
		func() (string, string, error) {
			var err error
			result, err = service.Archive(reference, os.Getenv("TMUX_PANE"))
			return result.Project.ID, result.Project.Name, err
		},
		func(out io.Writer, _, name string) error {
			if err := printStoppedAgents(out, result.StoppedAgents); err != nil {
				return err
			}
			_, err := fmt.Fprintf(out, "Archived Project %q\n", name)
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
