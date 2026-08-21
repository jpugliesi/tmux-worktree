package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
	"github.com/spf13/cobra"
)

func newProjectsAdoptCommand(service *projectservice.Service) *cobra.Command {
	var name string
	command := &cobra.Command{
		Use:   "adopt [SESSION]",
		Short: "Adopt an existing tmux session as a Project",
		Args:  optionalArg("SESSION"),
		RunE: func(command *cobra.Command, args []string) error {
			session := ""
			if len(args) == 1 {
				session = args[0]
			}
			pane := os.Getenv("TMUX_PANE")
			var project domain.Project
			return runMutation(command, "projects.adopt",
				func() (string, string, error) {
					validated, err := service.ValidateAdopt(session, pane, name)
					return "", validated.Name, err
				},
				func() (string, string, error) {
					var err error
					project, err = service.Adopt(session, pane, name)
					return project.ID, project.Name, err
				},
				func(out io.Writer, _, _ string) error {
					_, err := fmt.Fprintf(out, "Adopted Project %q (%s)\nRepositories: %d\n", project.Name, project.ID, len(project.Repositories))
					return err
				})
		},
	}
	command.Flags().StringVar(&name, "name", "", "Set the Project name. The default name is the tmux session name")
	setArguments(command, optionalArgument("session", "the default is the tmux session of the caller"))
	return command
}
