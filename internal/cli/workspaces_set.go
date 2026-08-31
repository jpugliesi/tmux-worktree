package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

func newWorkspacesSetCommand(options Options, service *workspaceservice.Service) *cobra.Command {
	var projectName string
	command := &cobra.Command{
		Use:   "set WORKSPACE",
		Short: "Set Workspace configuration",
		Args:  exactArgs("WORKSPACE"),
		RunE: func(command *cobra.Command, args []string) error {
			if strings.TrimSpace(projectName) == "" {
				return invalidUsage(command, "pass --project PROJECT")
			}
			return setWorkspaceProject(command, options, service, args[0], projectName)
		},
	}
	command.Flags().StringVar(&projectName, "project", "", "Set the Ticket Project for this Workspace")
	_ = command.MarkFlagRequired("project")
	setArguments(command, requiredArgument("workspace"))
	command.ValidArgsFunction = workspaceNameCompletion(service)
	registerProjectFlagCompletion(command, options)
	return command
}

func setWorkspaceProject(command *cobra.Command, options Options, service *workspaceservice.Service, reference, projectName string) error {
	workspace, err := resolveWorkspace(service, reference)
	if err != nil {
		return err
	}
	if err := validateWorkspaceProjectTickets(options, workspace.Tickets, projectName); err != nil {
		return err
	}
	return runMutation(command, "workspaces.set",
		func() (string, string, error) {
			return workspace.ID, workspace.Name, service.ValidateSetProject(workspace.ID, projectName)
		},
		func() (string, string, error) {
			updated, err := service.SetProject(workspace.ID, projectName)
			return updated.ID, updated.Name, err
		},
		func(out io.Writer, _, workspaceName string) error {
			_, err := fmt.Fprintf(out, "Set Project %q on Workspace %q\n", projectName, workspaceName)
			return err
		})
}

func validateWorkspaceProjectTickets(options Options, slugs []string, projectName string) error {
	tickets, err := options.ticketService()
	if err != nil {
		return err
	}
	project, err := tickets.Project(projectName)
	if err != nil {
		return err
	}
	if project.Closed {
		return clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Project %q is closed", project.Name),
			"Pick an active Project, or create one with 'twt projects create NAME'.")
	}
	for _, slug := range slugs {
		ticket, err := tickets.Resolve(slug)
		if err != nil {
			return err
		}
		if ticket.Project != projectName {
			return clierr.WithHint(
				clierr.New(clierr.PreconditionFailed, "Ticket %q belongs to Project %q", ticket.Slug, ticket.Project),
				"Move the Ticket with 'twt tickets set %s --project %s', or pick a Project that matches the linked Tickets.", ticket.Slug, projectName)
		}
	}
	return nil
}
