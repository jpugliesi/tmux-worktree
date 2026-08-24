package workspace

import (
	"slices"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestWorkspaceEnvironmentKeepsLegacyTemplateVariables(t *testing.T) {
	values := workspaceEnvironment(domain.Workspace{ID: "workspace-one", Name: "auth-fix", Root: "/tmp/auth-fix"})
	for _, want := range []string{
		"TWT_WORKSPACE_ID=workspace-one",
		"TWT_WORKSPACE_NAME=auth-fix",
		"TWT_WORKSPACE_ROOT=/tmp/auth-fix",
		"TWT_PROJECT_ID=workspace-one",
		"TWT_PROJECT_NAME=auth-fix",
		"TWT_PROJECT_ROOT=/tmp/auth-fix",
	} {
		if !slices.Contains(values, want) {
			t.Fatalf("Workspace environment does not contain %q: %v", want, values)
		}
	}
}
