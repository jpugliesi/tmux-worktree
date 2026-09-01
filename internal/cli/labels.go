package cli

import (
	"fmt"
	"io"
	"sort"

	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

type labelCount struct {
	Name    string `json:"name"`
	Tickets int    `json:"tickets"`
}

type labelsListOutput struct {
	SchemaVersion int          `json:"schemaVersion"`
	Labels        []labelCount `json:"labels"`
	TotalCount    int          `json:"totalCount"`
	Truncated     bool         `json:"truncated,omitempty"`
}

func newLabelsCommand(options Options) *cobra.Command {
	labels := groupCommand(&cobra.Command{Use: "labels", Short: "Manage Ticket labels"})
	labels.AddCommand(newLabelsListCommand(options))
	labels.AddCommand(newLabelsAddCommand(options))
	labels.AddCommand(newLabelsRemoveCommand(options))
	labels.AddCommand(newLabelsRenameCommand(options))
	return labels
}

func newLabelsListCommand(options Options) *cobra.Command {
	var all bool
	var limit, offset int
	command := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List labels derived from Tickets",
		Args:    noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			tickets, err := service.List(ticketservice.ListFilter{All: all})
			if err != nil {
				return err
			}
			counts := map[string]int{}
			for _, ticket := range tickets {
				for _, label := range ticket.Labels {
					counts[label]++
				}
			}
			names := make([]string, 0, len(counts))
			for name := range counts {
				names = append(names, name)
			}
			sort.Strings(names)
			labels := make([]labelCount, 0, len(names))
			for _, name := range names {
				labels = append(labels, labelCount{Name: name, Tickets: counts[name]})
			}
			labels, total, truncated, err := applyWindow(labels, offset, limit)
			if err != nil {
				return err
			}
			if format := resolvedOutputFormat(command); format != outputText {
				if format == outputNDJSON {
					return writeNDJSONList(command, labels, total, truncated)
				}
				return writeReadJSON(command, labelsListOutput{
					SchemaVersion: jsonSchemaVersion,
					Labels:        labels,
					TotalCount:    total,
					Truncated:     truncated,
				}, "labels")
			}
			if total == 0 {
				_, err = fmt.Fprintln(command.ErrOrStderr(), "No labels exist. Run 'twt labels add NAME --ticket TICKET'.")
				return err
			}
			rows := make([][]string, 0, len(labels))
			for _, label := range labels {
				rows = append(rows, []string{label.Name, fmt.Sprintf("%d", label.Tickets)})
			}
			return writeTable(command.OutOrStdout(), []string{"NAME", "TICKETS"}, rows)
		},
	}
	command.Flags().BoolVar(&all, "all", false, "Include labels that appear only on closed Tickets")
	addListReadFlags(command, &limit, &offset, labelCount{})
	return command
}

func newLabelsAddCommand(options Options) *cobra.Command {
	var tickets []string
	command := &cobra.Command{
		Use:   "add NAME",
		Short: "Add a label to Tickets",
		Args:  exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return mutateLabel(command, "labels.add",
				func() (ticketservice.LabelChangeResult, error) {
					return service.AddLabel(args[0], tickets, true)
				},
				func() (ticketservice.LabelChangeResult, error) {
					return service.AddLabel(args[0], tickets, false)
				},
				func(out io.Writer, result ticketservice.LabelChangeResult) error {
					_, err := fmt.Fprintf(out, "Added label %q to %d Tickets\n", result.Name, len(result.Tickets))
					return err
				})
		},
	}
	command.Flags().StringArrayVar(&tickets, "ticket", nil, "Add the label to this Ticket; repeat for more")
	_ = command.MarkFlagRequired("ticket")
	setArguments(command, requiredArgument("name"))
	_ = command.RegisterFlagCompletionFunc("ticket", ticketFlagCompletion(options))
	return command
}

func newLabelsRemoveCommand(options Options) *cobra.Command {
	var tickets []string
	command := &cobra.Command{
		Use:     "remove NAME",
		Aliases: []string{"rm"},
		Short:   "Remove a label from Tickets",
		Args:    exactArgs("NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return mutateLabel(command, "labels.remove",
				func() (ticketservice.LabelChangeResult, error) {
					return service.RemoveLabel(args[0], tickets, true)
				},
				func() (ticketservice.LabelChangeResult, error) {
					return service.RemoveLabel(args[0], tickets, false)
				},
				func(out io.Writer, result ticketservice.LabelChangeResult) error {
					_, err := fmt.Fprintf(out, "Removed label %q from %d Tickets\n", result.Name, len(result.Tickets))
					return err
				})
		},
	}
	command.Flags().StringArrayVar(&tickets, "ticket", nil, "Remove the label from this Ticket only; repeat for more; omit to remove it from every Ticket")
	setArguments(command, requiredArgument("name"))
	command.ValidArgsFunction = ticketLabelNameCompletion(options)
	_ = command.RegisterFlagCompletionFunc("ticket", ticketFlagCompletion(options))
	return command
}

func newLabelsRenameCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "rename NAME NEW_NAME",
		Short: "Rename a label on every Ticket that carries it",
		Args:  exactArgs("NAME", "NEW_NAME"),
		RunE: func(command *cobra.Command, args []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			return mutateLabel(command, "labels.rename",
				func() (ticketservice.LabelChangeResult, error) {
					return service.RenameLabel(args[0], args[1], true)
				},
				func() (ticketservice.LabelChangeResult, error) {
					return service.RenameLabel(args[0], args[1], false)
				},
				func(out io.Writer, result ticketservice.LabelChangeResult) error {
					_, err := fmt.Fprintf(out, "Renamed label %q to %q on %d Tickets\n", result.Name, result.NewName, len(result.Tickets))
					return err
				})
		},
	}
	setArguments(command, requiredArgument("name"), requiredArgument("new_name"))
	command.ValidArgsFunction = ticketLabelNameCompletion(options)
	return command
}

func mutateLabel(command *cobra.Command, operation string, validate, apply func() (ticketservice.LabelChangeResult, error), text func(io.Writer, ticketservice.LabelChangeResult) error) error {
	if isDryRun(command) {
		result, err := validate()
		if err != nil {
			return err
		}
		return writeMutation(command, operation, statusValid, result.Name, resultName(result))
	}
	result, err := apply()
	if err != nil {
		return err
	}
	if WantsJSON(command) {
		return writeMutation(command, operation, statusApplied, result.Name, resultName(result))
	}
	return text(command.OutOrStdout(), result)
}

func resultName(result ticketservice.LabelChangeResult) string {
	if result.NewName != "" {
		return result.NewName
	}
	return result.Name
}

func ticketLabelNameCompletion(options Options) completionFunc {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, noFileCompletion
		}
		return ticketLabelNames(options, toComplete), noFileCompletion
	}
}
