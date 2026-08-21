package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func TestProjectFindByIDDoesNotReadOtherProjectFiles(t *testing.T) {
	stateDir := t.TempDir()
	projects := NewProjectStore(stateDir)
	now := time.Now().UTC()
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "project-one", Name: "alpha",
		Status: domain.ProjectActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := projects.Save(project); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "projects", "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := projects.Find("project-one")
	if err != nil {
		t.Fatalf("Find() by ID = %v", err)
	}
	if got.ID != project.ID || got.Name != project.Name {
		t.Fatalf("Find() = %+v", got)
	}

	if _, err := projects.List(); err == nil {
		t.Fatal("List() succeeded with a broken sibling file")
	}
}
