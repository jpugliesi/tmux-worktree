package cli_test

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestWorkspacesCommandAndWAliasUseTheWorkspaceContract(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspace := domain.Workspace{
		Version:      domain.WorkspaceVersion,
		ID:           "workspace-one",
		Name:         "auth-fix",
		TemplateName: "example",
		Status:       domain.WorkspaceActive,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := store.NewWorkspaceStore(filepath.Join(root, "state")).Save(workspace); err != nil {
		t.Fatal(err)
	}
	options := cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}

	canonical := executeWithOptions(t, options, nil, "workspaces", "list", "--output", "json")
	short := executeWithOptions(t, options, nil, "w", "list", "--output", "json")
	if short != canonical {
		t.Fatalf("twt w list = %s, want %s", short, canonical)
	}

	var result struct {
		SchemaVersion int `json:"schemaVersion"`
		Workspaces    []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal([]byte(canonical), &result); err != nil {
		t.Fatalf("decode workspaces list: %v\n%s", err, canonical)
	}
	if result.SchemaVersion != 2 || len(result.Workspaces) != 1 || result.Workspaces[0].ID != workspace.ID {
		t.Fatalf("workspaces list = %#v", result)
	}
}
