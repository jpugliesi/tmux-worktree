package cli

import (
	"fmt"

	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

type ticketsUseOutput struct {
	SchemaVersion int    `json:"schemaVersion"`
	Project       string `json:"project,omitempty"`
}

func newTicketsUseCommand(options Options) *cobra.Command {
	var unset bool
	command := &cobra.Command{
		Use:   "use [PROJECT]",
		Short: "Set or show the saved current Project",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if unset && len(args) > 0 {
				return invalidUsage(command, "pass PROJECT or --unset, not both")
			}
			if unset {
				return unsetCurrentProject(command, options)
			}
			if len(args) == 0 {
				return showCurrentProject(command, options)
			}
			return saveCurrentProject(command, options, args[0])
		},
	}
	command.Flags().BoolVar(&unset, "unset", false, "Clear the saved current Project")
	setArguments(command, optionalArgument("project", "the Project to save as the current Project"))
	command.ValidArgsFunction = ticketProjectNameCompletion(options)
	return command
}

func saveCurrentProject(command *cobra.Command, options Options, project string) error {
	service, err := options.ticketService()
	if err != nil {
		return err
	}
	if _, err := service.Project(project); err != nil {
		return err
	}
	if isDryRun(command) {
		if WantsJSON(command) {
			return writeMutation(command, "tickets.use", statusValid, project, project)
		}
		_, err := fmt.Fprintf(command.OutOrStdout(), "Would set the current Project to %q\n", project)
		return err
	}
	if err := store.SaveCurrentProject(options.StateDir, project); err != nil {
		return err
	}
	if WantsJSON(command) {
		return writeMutation(command, "tickets.use", statusApplied, project, project)
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "Current Project is %q\n", project)
	return err
}

func unsetCurrentProject(command *cobra.Command, options Options) error {
	if isDryRun(command) {
		if WantsJSON(command) {
			return writeMutation(command, "tickets.use", statusValid, "", "")
		}
		_, err := fmt.Fprintln(command.OutOrStdout(), "Would clear the saved current Project")
		return err
	}
	if err := store.ClearCurrentProject(options.StateDir); err != nil {
		return err
	}
	if WantsJSON(command) {
		return writeMutation(command, "tickets.use", statusApplied, "", "")
	}
	_, err := fmt.Fprintln(command.OutOrStdout(), "Cleared the saved current Project")
	return err
}

func showCurrentProject(command *cobra.Command, options Options) error {
	project, err := store.LoadCurrentProject(options.StateDir)
	if err != nil {
		return err
	}
	if WantsJSON(command) {
		return writeReadJSON(command, ticketsUseOutput{SchemaVersion: jsonSchemaVersion, Project: project}, "")
	}
	if project == "" {
		_, err := fmt.Fprintln(command.OutOrStdout(), "No current Project is saved.")
		return err
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "Current Project is %q\n", project)
	return err
}
