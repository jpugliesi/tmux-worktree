package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestCurrentFindsOwnedWorkspaceWithoutListingOthers(t *testing.T) {
	stateDir := t.TempDir()
	root := t.TempDir()
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-owned", Name: "owned",
		Status: domain.WorkspaceActive, Root: root,
		Repositories: []domain.WorkspaceRepository{{Name: "app", Path: filepath.Join(root, "app")}},
		CreatedAt:    now, UpdatedAt: now,
	}
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".twt-owned.json"), []byte("{\"owner\":\"twt\",\"workspaceId\":\"workspace-owned\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "projects", "broken.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := NewService(Options{StateDir: stateDir, DataDir: t.TempDir()}).Current(filepath.Join(root, "app"), "", "")
	if err != nil || got.ID != workspace.ID {
		t.Fatalf("Current() = %+v, %v", got, err)
	}
}

func TestCurrentFindsAnAdoptedWorkspaceFromASecondRepository(t *testing.T) {
	stateDir := t.TempDir()
	first := t.TempDir()
	second := t.TempDir()
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-adopted", Name: "dev-env",
		Status: domain.WorkspaceActive, Adopted: true, Root: first,
		Repositories: []domain.WorkspaceRepository{
			{Name: "first", Path: first},
			{Name: "second", Path: second},
		},
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}

	got, err := NewService(Options{StateDir: stateDir, DataDir: t.TempDir()}).FindByDirectory(filepath.Join(second, "src"))
	if err != nil || got.ID != workspace.ID {
		t.Fatalf("FindByDirectory() in a second repository = %+v, %v", got, err)
	}
}

func TestCurrentAcceptsALegacyWorkspaceOwnershipMarker(t *testing.T) {
	stateDir := t.TempDir()
	root := t.TempDir()
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "legacy-workspace", Name: "legacy",
		Status: domain.WorkspaceActive, Root: root, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".twt-owned.json"), []byte("{\"owner\":\"twt\",\"projectId\":\"legacy-workspace\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkspaceMarker(root, workspace.ID); err != nil {
		t.Fatalf("ValidateWorkspaceMarker() = %v", err)
	}

	got, err := NewService(Options{StateDir: stateDir, DataDir: t.TempDir()}).Current(root, "", "")
	if err != nil || got.ID != workspace.ID {
		t.Fatalf("Current() with a legacy marker = %+v, %v", got, err)
	}
}
