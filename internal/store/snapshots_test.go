package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestSnapshotStoreRejectsUnownedOrUnsafeProjectDirectories(t *testing.T) {
	projectID := "project-one"
	tests := []struct {
		name  string
		setup func(t *testing.T, directory string)
		want  string
	}{
		{
			name: "wrong Project marker",
			setup: func(t *testing.T, directory string) {
				writeSnapshotFixture(t, directory, ".twt2-snapshot.json", `{"version":1,"owner":"twt2","projectId":"project-two"}`)
			},
			want: "conflicting ownership marker",
		},
		{
			name: "marker symlink",
			setup: func(t *testing.T, directory string) {
				outside := filepath.Join(t.TempDir(), "marker.json")
				if err := os.WriteFile(outside, []byte(`{"version":1,"owner":"twt2","projectId":"project-one"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(directory, ".twt2-snapshot.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: "not a safe regular file",
		},
		{
			name: "unexpected file",
			setup: func(t *testing.T, directory string) {
				writeSnapshotFixture(t, directory, ".twt2-snapshot.json", `{"version":1,"owner":"twt2","projectId":"project-one"}`)
				writeSnapshotFixture(t, directory, "keep.txt", "keep")
			},
			want: "unexpected item",
		},
		{
			name: "non-regular marker",
			setup: func(t *testing.T, directory string) {
				if err := os.Mkdir(filepath.Join(directory, ".twt2-snapshot.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want: "not a safe regular file",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateDir := t.TempDir()
			snapshots := store.NewSnapshotStore(stateDir)
			directory, err := snapshots.ProjectDir(projectID)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			test.setup(t, directory)
			if _, err := snapshots.ValidateProject(projectID, false); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateProject() error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestSnapshotStoreRejectsProjectDirectorySymlink(t *testing.T) {
	stateDir := t.TempDir()
	snapshots := store.NewSnapshotStore(stateDir)
	directory, err := snapshots.ProjectDir("project-one")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(directory), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), directory); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshots.ValidateProject("project-one", false); err == nil || !strings.Contains(err.Error(), "not a safe directory") {
		t.Fatalf("ValidateProject() error = %v", err)
	}
}

func TestSnapshotStoreRejectsSymlinkedStateAncestors(t *testing.T) {
	for _, ancestor := range []string{"snapshots", filepath.Join("snapshots", "projects")} {
		t.Run(ancestor, func(t *testing.T) {
			stateDir := t.TempDir()
			outside := t.TempDir()
			path := filepath.Join(stateDir, ancestor)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
			err := store.NewSnapshotStore(stateDir).Save("project-one", "private\n")
			if err == nil || !strings.Contains(err.Error(), "not a safe directory") {
				t.Fatalf("Save() error = %v", err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("symlink target changed: entries=%v error=%v", entries, err)
			}
		})
	}
}

func writeSnapshotFixture(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
