package cli

import (
	"github.com/jpugliesi/tmux-worktree/internal/prstate"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

type ticketTreeOutput struct {
	SchemaVersion int                        `json:"schemaVersion"`
	Tree          ticketservice.QueueResult  `json:"tree"`
	PRStates      map[string]prstate.PRState `json:"prStates,omitempty"`
}

func newTicketsTreeCommand(options Options) *cobra.Command {
	var project string
	var all, noFetch, fresh bool
	command := &cobra.Command{
		Use:   "tree --project PROJECT [--all] [--no-fetch]",
		Short: "Render the Ticket dependency tree with PR state",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			service, err := options.ticketService()
			if err != nil {
				return err
			}
			scope, err := resolveTicketProject(command, options, project, command.Flags().Changed("project"), false)
			if err != nil {
				return err
			}
			if scope.Project == "" {
				return invalidUsageWithHint(command, ticketProjectScopeHint(command),
					"tree needs a Project name")
			}
			freshenTicketStore(command, service, fresh)
			result, err := service.Tree(scope.Project, all)
			if err != nil {
				return err
			}
			urls := []string{}
			for _, node := range result.Graph {
				urls = append(urls, node.PullRequests...)
			}
			var prStates map[string]prstate.PRState
			if len(urls) > 0 {
				prStates = options.prStateService().GetAll(command.Context(), urls, noFetch)
			}
			if WantsJSON(command) {
				return writeReadJSON(command, ticketTreeOutput{
					SchemaVersion: jsonSchemaVersion, Tree: result, PRStates: prStates,
				}, "tree")
			}
			return renderTicketTree(command.OutOrStdout(), result, prStates)
		},
	}
	command.Flags().StringVar(&project, "project", "", "Render this Project's tree")
	command.Flags().BoolVar(&all, "all", false, "Include done and wontfix Tickets")
	command.Flags().BoolVar(&noFetch, "no-fetch", false, "Use only cached PR state; never call the forge")
	addFreshFlag(command, &fresh)
	registerProjectFlagCompletion(command, options)
	addFieldsFlag(command, ticketTreeOutput{})
	return command
}
