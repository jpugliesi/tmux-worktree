package cli

import (
	"fmt"

	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

type ticketDoctorOutput struct {
	SchemaVersion int                              `json:"schemaVersion"`
	Report        ticketservice.TicketDoctorReport `json:"report"`
}

type ticketRepairOutput struct {
	SchemaVersion int                              `json:"schemaVersion"`
	Operation     string                           `json:"operation"`
	Status        string                           `json:"status"`
	Result        ticketservice.TicketRepairResult `json:"result"`
}

func newTicketsDoctorCommand(options Options) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check Ticket files and locations",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			report, err := service.Doctor()
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, ticketDoctorOutput{SchemaVersion: jsonSchemaVersion, Report: report})
			}
			if len(report.Issues) == 0 {
				_, err := fmt.Fprintf(command.OutOrStdout(), "%d Ticket files are healthy.\n", report.TicketCount)
				return err
			}
			rows := make([][]string, 0, len(report.Issues))
			for _, issue := range report.Issues {
				status := "blocked"
				if issue.Repairable {
					status = "repairable"
				}
				rows = append(rows, []string{status, issue.Code, issue.Slug, issue.Path, issue.Message})
			}
			return writeTable(command.OutOrStdout(), []string{"STATUS", "CODE", "TICKET", "PATH", "MESSAGE"}, rows)
		},
	}
}

func newTicketsRepairCommand(options Options) *cobra.Command {
	return &cobra.Command{
		Use:   "repair",
		Short: "Move Tickets to their correct locations",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return repairTickets(command, service)
		},
	}
}

// repairTickets runs the shared repair mutation for the command and apply.
func repairTickets(command *cobra.Command, service *ticketservice.Service) error {
	result, repairErr := service.Repair(isDryRun(command))
	status := statusApplied
	if isDryRun(command) {
		status = statusValid
	}
	if repairErr != nil {
		status = "blocked"
	}
	if WantsJSON(command) {
		if err := writeJSONOutput(command, ticketRepairOutput{
			SchemaVersion: jsonSchemaVersion, Operation: "tickets.repair", Status: status, Result: result,
		}); err != nil {
			return err
		}
	} else {
		for _, blocker := range result.Plan.Blockers {
			if _, err := fmt.Fprintf(command.OutOrStdout(), "Blocked %s: %s\n", blocker.Code, blocker.Message); err != nil {
				return err
			}
		}
		verb := "Would move"
		if !isDryRun(command) {
			verb = "Moved"
		}
		for _, move := range result.Plan.Moves {
			if _, err := fmt.Fprintf(command.OutOrStdout(), "%s Ticket %q from %q to %q\n", verb, move.Slug, move.Source, move.Destination); err != nil {
				return err
			}
		}
		if len(result.Plan.Blockers) == 0 && len(result.Plan.Moves) == 0 {
			if _, err := fmt.Fprintln(command.OutOrStdout(), "No Ticket repairs are needed."); err != nil {
				return err
			}
		}
	}
	return repairErr
}
