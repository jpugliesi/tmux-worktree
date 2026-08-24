package store

import "github.com/jpugliesi/tmux-worktree/internal/domain"

// normalizeLegacySetupSteps converts version-one Project setup names at the
// state-store boundary. Loading a record changes only the in-memory value. A
// later normal save writes the current Workspace names.
func normalizeLegacySetupSteps(steps []domain.SetupStep) {
	for index := range steps {
		step := &steps[index]
		switch step.Kind {
		case domain.StepKind("project_root"):
			step.Kind = domain.StepWorkspaceRoot
		case domain.StepKind("project_init"):
			step.Kind = domain.StepWorkspaceInit
		}
		switch step.ID {
		case "project_root":
			step.ID = "workspace_root"
		case "project_init":
			step.ID = "workspace_init"
		}
	}
}
