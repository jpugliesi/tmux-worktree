package cli

import (
	"fmt"
	"io"
	"strings"

	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

func newProjectsCloseCommand(options Options) *cobra.Command {
	var force bool
	command := &cobra.Command{
		Use:   "close NAME",
		Short: "Close a Project",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return closeProject(command, service, args[0], force, true)
		},
	}
	command.Flags().BoolVar(&force, "force", false, "Set open Tickets to wontfix")
	setArguments(command, requiredArgument("name"))
	command.ValidArgsFunction = ticketProjectNameCompletion(options)
	return command
}

func closeProject(command *cobra.Command, service ticketservice.Store, name string, force, allowPrompt bool) error {
	if !force && allowPrompt && !isDryRun(command) && resolvedOutputFormat(command) == outputText &&
		interactiveTicketSession(command) {
		preview, err := service.CloseProject(name, true, true)
		if err != nil {
			return err
		}
		if len(preview.WontfixTickets) > 0 {
			confirmed, confirmErr := confirmProjectClose(command, name, len(preview.WontfixTickets))
			if confirmErr != nil {
				return confirmErr
			}
			if !confirmed {
				_, err := fmt.Fprintf(command.OutOrStdout(), "Kept Project %q\n", name)
				return err
			}
			force = true
		}
	}
	changedCount := 0
	return runMutation(command, "projects.close",
		func() (string, string, error) {
			result, err := service.CloseProject(name, force, true)
			changedCount = len(result.WontfixTickets)
			return result.Project.Name, result.Project.Name, err
		},
		func() (string, string, error) {
			result, err := service.CloseProject(name, force, false)
			changedCount = len(result.WontfixTickets)
			return result.Project.Name, result.Project.Name, err
		},
		func(out io.Writer, _, projectName string) error {
			if changedCount == 0 {
				_, err := fmt.Fprintf(out, "Closed Project %q\n", projectName)
				return err
			}
			noun := "Tickets"
			if changedCount == 1 {
				noun = "Ticket"
			}
			_, err := fmt.Fprintf(out, "Closed Project %q and set %d open %s to wontfix\n", projectName, changedCount, noun)
			return err
		})
}

func confirmProjectClose(command *cobra.Command, name string, openTickets int) (bool, error) {
	if _, err := fmt.Fprintf(command.ErrOrStderr(),
		"Project %q has %d open Tickets. Set them to wontfix and close the Project? [y/N] ", name, openTickets); err != nil {
		return false, err
	}
	line, err := readTicketPromptLine(command)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	case "", "n", "no":
		return false, nil
	default:
		return false, invalidUsageWithHint(command, "Answer y or n.", "unrecognized confirm answer %q", line)
	}
}
