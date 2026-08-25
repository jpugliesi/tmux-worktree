package cli

import (
	"fmt"
	"io"
	"strings"

	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

type ticketsQueueOutput struct {
	SchemaVersion int                       `json:"schemaVersion"`
	Queue         ticketservice.QueueResult `json:"queue"`
}

func newTicketsQueueCommand(options Options) *cobra.Command {
	var project string
	var limit int
	command := &cobra.Command{
		Use:   "queue",
		Short: "Show a Project Ticket queue",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			result, err := service.Queue(project, limit)
			if err != nil {
				return err
			}
			if resolvedOutputFormat(command) != outputText {
				return writeReadJSON(command, ticketsQueueOutput{SchemaVersion: jsonSchemaVersion, Queue: result}, "queue")
			}
			return writeTicketsQueue(command.OutOrStdout(), result)
		},
	}
	command.Flags().StringVar(&project, "project", "", "Show the queue for this Project")
	command.Flags().IntVar(&limit, "limit", 0, "Limit the number of ready Tickets; zero returns all ready Tickets")
	addFieldsFlag(command, ticketservice.QueueResult{})
	_ = command.MarkFlagRequired("project")
	registerProjectFlagCompletion(command, options)
	return command
}

func writeTicketsQueue(out io.Writer, result ticketservice.QueueResult) error {
	if _, err := fmt.Fprintf(out, "Project: %s\n", result.Project); err != nil {
		return err
	}
	if len(result.Cycles) > 0 {
		for _, cycle := range result.Cycles {
			if _, err := fmt.Fprintf(out, "Dependency cycle: %s\n", strings.Join(cycle, ", ")); err != nil {
				return err
			}
		}
	}
	if len(result.Ready) == 0 {
		_, err := fmt.Fprintln(out, "No Tickets are ready.")
		return err
	}
	rows := make([][]string, 0, len(result.Ready))
	for _, ticket := range result.Ready {
		rows = append(rows, []string{ticket.Slug, fmt.Sprintf("p%d", ticket.Priority), ticket.Title})
	}
	return writeTable(out, []string{"TICKET", "PRIORITY", "TITLE"}, rows)
}
