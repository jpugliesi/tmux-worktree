package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/spf13/cobra"
)

type contextOutput struct {
	SchemaVersion  int           `json:"schemaVersion"`
	Project        projectOutput `json:"project"`
	RepositoryName string        `json:"repositoryName,omitempty"`
}

func newContextCommand(options Options) *cobra.Command {
	service := options.projectService()
	var directory string
	command := &cobra.Command{
		Use:   "context",
		Short: "Show the current twt context",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			lookupDirectory := directory
			projectID := os.Getenv("TWT_PROJECT_ID")
			tmuxPane := os.Getenv("TMUX_PANE")
			if command.Flags().Changed("directory") {
				projectID = ""
				tmuxPane = ""
			} else {
				var err error
				lookupDirectory, err = os.Getwd()
				if err != nil {
					return err
				}
			}
			project, err := service.Current(lookupDirectory, projectID, tmuxPane)
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeJSONOutput(command, contextOutput{SchemaVersion: jsonSchemaVersion, Project: toProjectOutput(project), RepositoryName: repositoryForDirectory(project, lookupDirectory)})
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Project: %s\n", project.Name)
			return err
		},
	}
	command.Flags().StringVar(&directory, "directory", "", "Resolve context from this directory before tmux or environment context")
	return command
}

func repositoryForDirectory(project domain.Project, directory string) string {
	absDirectory, err := filepath.Abs(directory)
	if err != nil {
		return ""
	}
	for _, repository := range project.Repositories {
		relative, err := filepath.Rel(repository.Path, absDirectory)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return repository.Name
		}
	}
	return ""
}
