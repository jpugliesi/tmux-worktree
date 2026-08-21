package cli

import (
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
				if project, err := service.FindByDirectory(directory); err == nil {
					return writeContext(command, project, lookupDirectory)
				}
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
			return writeContext(command, project, lookupDirectory)
		},
	}
	command.Flags().StringVar(&directory, "directory", "", "Resolve context from this directory before tmux or environment context")
	addFieldsFlag(command, contextOutput{})
	return command
}

func writeContext(command *cobra.Command, project domain.Project, directory string) error {
	if WantsJSON(command) {
		return writeReadJSON(command, contextOutput{SchemaVersion: jsonSchemaVersion, Project: toProjectOutput(project), RepositoryName: repositoryForDirectory(project, directory)}, "")
	}
	fields := [][2]string{{"Project", project.Name}}
	if repository := repositoryForDirectory(project, directory); repository != "" {
		fields = append(fields, [2]string{"Repository", repository})
	}
	return writeFields(command.OutOrStdout(), fields)
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
