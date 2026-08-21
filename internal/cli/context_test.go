package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestContextDirectoryUsesAnAdoptedRepositoryThenFallsBackToTheSession(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	outside := filepath.Join(root, "outside")
	for _, directory := range []string{first, second, outside} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "project-adopted", Name: "dev-env",
		Status: domain.ProjectActive, Adopted: true, Root: first,
		Repositories: []domain.ProjectRepository{
			{Name: "first", Path: first},
			{Name: "second", Path: second},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	stateDir := filepath.Join(root, "state")
	if err := store.NewProjectStore(stateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}
	t.Setenv("TWT_PROJECT_ID", "")
	t.Setenv("TMUX_PANE", "")

	fromSecond := executeWithOptions(t, options, nil, "context", "--directory", second, "--output", "json")
	if !strings.Contains(fromSecond, `"name":"dev-env"`) || !strings.Contains(fromSecond, `"repositoryName":"second"`) {
		t.Fatalf("context from a second adopted repository = %s", fromSecond)
	}

	t.Setenv("TWT_PROJECT_ID", project.ID)
	fromOutside := executeWithOptions(t, options, nil, "context", "--directory", outside, "--output", "json")
	if !strings.Contains(fromOutside, `"name":"dev-env"`) {
		t.Fatalf("context outside a repository did not use the session Project = %s", fromOutside)
	}
}
