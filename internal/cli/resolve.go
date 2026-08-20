package cli

import (
	"os"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
)

// currentProjectReference is the literal PROJECT value that selects the
// Project of the current directory, environment, or tmux pane.
const currentProjectReference = "current"

// resolveProject finds one Project by name or immutable ID. The literal
// value "current" uses the current directory, the TWT_PROJECT_ID value, and
// the current tmux pane, in that order.
func resolveProject(projects *projectservice.Service, reference string) (domain.Project, error) {
	if reference != currentProjectReference {
		return projects.Find(reference)
	}
	directory, err := os.Getwd()
	if err != nil {
		return domain.Project{}, err
	}
	return projects.Current(directory, os.Getenv("TWT_PROJECT_ID"), os.Getenv("TMUX_PANE"))
}

// resolveProjectReference maps a PROJECT argument to a stable reference. For
// the literal value "current" it returns the immutable ID of the current
// Project. Every other value stays unchanged.
func resolveProjectReference(projects *projectservice.Service, reference string) (string, error) {
	if reference != currentProjectReference {
		return reference, nil
	}
	project, err := resolveProject(projects, reference)
	if err != nil {
		return "", err
	}
	return project.ID, nil
}
