package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/spf13/cobra"
)

type contextOutput struct {
	SchemaVersion  int             `json:"schemaVersion"`
	Workspace      workspaceOutput `json:"workspace"`
	RepositoryName string          `json:"repositoryName,omitempty"`
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
					return writeContext(command, workspace, lookupDirectory)
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
			return writeContext(command, workspace, lookupDirectory)
		},
	}
	command.Flags().StringVar(&directory, "directory", "", "Resolve context from this directory before tmux or environment context")
	addFieldsFlag(command, contextOutput{})
	return command
}

func writeContext(command *cobra.Command, workspace domain.Workspace, directory string) error {
	if WantsJSON(command) {
		return writeReadJSON(command, contextOutput{SchemaVersion: jsonSchemaVersion, Workspace: toWorkspaceOutput(workspace), RepositoryName: repositoryForDirectory(workspace, directory)}, "")
	}
	fields := [][2]string{{"Workspace", workspace.Name}}
	if repository := repositoryForDirectory(workspace, directory); repository != "" {
		fields = append(fields, [2]string{"Repository", repository})
	}
	return writeFields(command.OutOrStdout(), fields)
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
