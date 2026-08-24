package cli

import (
	"os"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
)

// currentWorkspaceReference is the literal WORKSPACE value that selects the
// Workspace of the current directory, environment, or tmux pane.
const currentWorkspaceReference = "current"

// workspaceIDFromEnvironment reads the current name first and the version 1
// Project name second. A new Workspace exports both during the transition.
func workspaceIDFromEnvironment() string {
	if workspaceID := os.Getenv("TWT_WORKSPACE_ID"); workspaceID != "" {
		return workspaceID
	}
	return os.Getenv("TWT_PROJECT_ID")
}

// resolveWorkspace finds one Workspace by name or immutable ID. The literal
// value "current" uses the current directory, the TWT_WORKSPACE_ID value, and
// the current tmux pane, in that order.
func resolveWorkspace(workspaces *workspaceservice.Service, reference string) (domain.Workspace, error) {
	if reference != currentWorkspaceReference {
		return workspaces.Find(reference)
	}
	directory, err := os.Getwd()
	if err != nil {
		return domain.Workspace{}, err
	}
	return workspaces.Current(directory, workspaceIDFromEnvironment(), os.Getenv("TMUX_PANE"))
}

// resolveWorkspaceReference maps a WORKSPACE argument to a stable reference. For
// the literal value "current" it returns the immutable ID of the current
// Workspace. Every other value stays unchanged.
func resolveWorkspaceReference(workspaces *workspaceservice.Service, reference string) (string, error) {
	if reference != currentWorkspaceReference {
		return reference, nil
	}
	workspace, err := resolveWorkspace(workspaces, reference)
	if err != nil {
		return "", err
	}
	return workspace.ID, nil
}
