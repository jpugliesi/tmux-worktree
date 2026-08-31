package cli

import (
	"fmt"
	"io"

	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

func newProjectsRenameCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "rename NAME NEW_NAME",
		Short: "Rename a Project",
		Args:  exactArgs("NAME", "NEW_NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return renameProject(command, options, service, args[0], args[1])
		},
	}
	setArguments(command, requiredArgument("name"), requiredArgument("new_name"))
	command.ValidArgsFunction = allProjectNameCompletion(options)
	return command
}

func renameProject(command *cobra.Command, options Options, service ticketservice.Store, name, newName string) error {
	return runMutation(command, "projects.rename",
		func() (string, string, error) {
			project, err := service.RenameProject(name, newName, true)
			return project.Name, project.Name, err
		},
		func() (string, string, error) {
			project, err := service.RenameProject(name, newName, false)
			if err != nil {
				return "", "", err
			}
			if err := options.workspaceService().RetargetProject(name, newName); err != nil && !WantsJSON(command) {
				_, _ = fmt.Fprintf(command.ErrOrStderr(), "Warning: twt renamed the Project but could not retarget Workspaces: %v\n", err)
			}
			return project.Name, project.Name, nil
		},
		func(out io.Writer, _, projectName string) error {
			_, err := fmt.Fprintf(out, "Renamed Project %q to %q\n", name, projectName)
			return err
		})
}
