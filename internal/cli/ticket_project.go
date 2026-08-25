package cli

import (
	"os"

	"github.com/spf13/cobra"
)

const ticketProjectEnv = "TWT_PROJECT"

type ticketProjectScope struct {
	Project string
	Set     bool
}

// resolveTicketProject picks the Project for a tickets command. The order is
// --project, then TWT_PROJECT, then the current Workspace Project. With no
// Project in scope, the result is unset so a list includes every Project.
func resolveTicketProject(command *cobra.Command, options Options, flagProject string, flagSet, allProjects bool) (ticketProjectScope, error) {
	if allProjects && flagSet {
		return ticketProjectScope{}, invalidUsageWithHint(command,
			"Pass --project, or pass --all-projects.",
			"--project and --all-projects cannot be used together")
	}
	if allProjects {
		return ticketProjectScope{}, nil
	}
	if flagSet {
		return ticketProjectScope{Project: flagProject, Set: true}, nil
	}
	if project := os.Getenv(ticketProjectEnv); project != "" {
		return ticketProjectScope{Project: project, Set: true}, nil
	}
	if project := currentWorkspaceProject(options); project != "" {
		return ticketProjectScope{Project: project, Set: true}, nil
	}
	return ticketProjectScope{}, nil
}

func ticketProjectScopeHint(command *cobra.Command) string {
	if command.Flags().Lookup("all-projects") != nil {
		return "Pass --project PROJECT, set TWT_PROJECT, or pass --all-projects."
	}
	return "Pass --project PROJECT, or set TWT_PROJECT."
}

func currentWorkspaceProject(options Options) string {
	directory, err := os.Getwd()
	if err != nil {
		return ""
	}
	workspace, err := options.workspaceService().Current(directory, workspaceIDFromEnvironment(), os.Getenv("TMUX_PANE"))
	if err != nil {
		return ""
	}
	return workspace.Project
}
