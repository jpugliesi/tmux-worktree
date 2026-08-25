package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	ticketservice "github.com/jpugliesi/tmux-worktree/internal/ticket"
	"github.com/spf13/cobra"
)

type contextOutput struct {
	SchemaVersion  int             `json:"schemaVersion"`
	Workspace      workspaceOutput `json:"workspace"`
	TmuxSession    string          `json:"tmuxSession,omitempty"`
	RepositoryName string          `json:"repositoryName,omitempty"`
	Tickets        []domain.Ticket `json:"tickets,omitempty"`
	Ready          []domain.Ticket `json:"ready,omitempty"`
}

func newContextCommand(options Options) *cobra.Command {
	service := options.workspaceService()
	var directory string
	command := &cobra.Command{
		Use:   "context",
		Short: "Show the current twt context",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			lookupDirectory := directory
			workspaceID := workspaceIDFromEnvironment()
			tmuxPane := os.Getenv("TMUX_PANE")
			if command.Flags().Changed("directory") {
				if workspace, err := service.FindByDirectory(directory); err == nil {
					return writeContext(command, options, workspace, lookupDirectory)
				}
			} else {
				var err error
				lookupDirectory, err = os.Getwd()
				if err != nil {
					return err
				}
			}
			workspace, err := service.Current(lookupDirectory, workspaceID, tmuxPane)
			if err != nil {
				return err
			}
			return writeContext(command, options, workspace, lookupDirectory)
		},
	}
	command.Flags().StringVar(&directory, "directory", "", "Resolve context from this directory before tmux or environment context")
	addFieldsFlag(command, contextOutput{})
	return command
}

func writeContext(command *cobra.Command, options Options, workspace domain.Workspace, directory string) error {
	tickets, ready := contextTickets(options, workspace)
	if WantsJSON(command) {
		return writeReadJSON(command, contextOutput{
			SchemaVersion:  jsonSchemaVersion,
			Workspace:      toWorkspaceOutput(workspace),
			TmuxSession:    workspace.TmuxSession,
			RepositoryName: repositoryForDirectory(workspace, directory),
			Tickets:        tickets,
			Ready:          ready,
		}, "")
	}
	fields := [][2]string{{"Workspace", workspace.Name}}
	if workspace.TmuxSession != "" {
		fields = append(fields, [2]string{"Tmux session", workspace.TmuxSession})
	}
	if repository := repositoryForDirectory(workspace, directory); repository != "" {
		fields = append(fields, [2]string{"Repository", repository})
	}
	if len(tickets) > 0 {
		fields = append(fields, [2]string{"Tickets", strings.Join(workspace.Tickets, ", ")})
	}
	if len(ready) > 0 {
		slugs := make([]string, 0, len(ready))
		for _, ticket := range ready {
			slugs = append(slugs, ticket.Slug)
		}
		fields = append(fields, [2]string{"Ready", strings.Join(slugs, ", ")})
	}
	return writeFields(command.OutOrStdout(), fields)
}

func contextTickets(options Options, workspace domain.Workspace) ([]domain.Ticket, []domain.Ticket) {
	service, err := options.ticketService()
	if err != nil {
		return nil, nil
	}
	linked := resolveWorkspaceTickets(service, workspace.Tickets)
	var ready []domain.Ticket
	if workspace.Project != "" {
		ready, _ = service.List(ticketservice.ListFilter{
			Project: workspace.Project, ProjectSet: true, Ready: true,
		})
	}
	return linked, ready
}

func resolveWorkspaceTickets(service *ticketservice.Service, slugs []string) []domain.Ticket {
	tickets := make([]domain.Ticket, 0, len(slugs))
	for _, slug := range slugs {
		ticket, err := service.Resolve(slug)
		if err != nil {
			continue
		}
		tickets = append(tickets, ticket)
	}
	return tickets
}

func repositoryForDirectory(workspace domain.Workspace, directory string) string {
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		return ""
	}
	for _, repository := range workspace.Repositories {
		relative, err := filepath.Rel(repository.Path, absDirectory)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return repository.Name
		}
	}
	return ""
}
