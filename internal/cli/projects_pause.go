package cli

import (
	"fmt"
	"io"

	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

func newProjectsPauseCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "pause NAME",
		Short: "Pause a Project",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return pauseProject(command, service, args[0])
		},
	}
	setArguments(command, requiredArgument("name"))
	command.ValidArgsFunction = ticketProjectNameCompletion(options)
	return command
}

func newProjectsResumeCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "resume NAME",
		Short: "Resume a paused Project",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return resumeProject(command, service, args[0])
		},
	}
	setArguments(command, requiredArgument("name"))
	command.ValidArgsFunction = pausedProjectNameCompletion(options)
	return command
}

func pauseProject(command *cobra.Command, service ticketservice.Store, name string) error {
	return runMutation(command, "projects.pause",
		func() (string, string, error) {
			project, err := service.PauseProject(name, true)
			return project.Name, project.Name, err
		},
		func() (string, string, error) {
			project, err := service.PauseProject(name, false)
			return project.Name, project.Name, err
		},
		func(out io.Writer, _, projectName string) error {
			_, err := fmt.Fprintf(out, "Paused Project %q\n", projectName)
			return err
		})
}

func resumeProject(command *cobra.Command, service ticketservice.Store, name string) error {
	return runMutation(command, "projects.resume",
		func() (string, string, error) {
			project, err := service.ResumeProject(name, true)
			return project.Name, project.Name, err
		},
		func() (string, string, error) {
			project, err := service.ResumeProject(name, false)
			return project.Name, project.Name, err
		},
		func(out io.Writer, _, projectName string) error {
			_, err := fmt.Fprintf(out, "Resumed Project %q\n", projectName)
			return err
		})
}
