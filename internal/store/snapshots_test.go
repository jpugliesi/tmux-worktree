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
		{
			name: "unexpected Agent Session item",
			setup: func(t *testing.T, directory string) {
				writeSnapshotFixture(t, directory, ".twt2-snapshot.json", `{"version":1,"owner":"twt2","projectId":"project-one"}`)
				agents := makeSnapshotAgentsDir(t, directory)
				writeSnapshotFixture(t, agents, "evil.txt", "evil")
			},
			want: "unexpected item",
		},
		{
			name: "Agent Session snapshot symlink",
			setup: func(t *testing.T, directory string) {
				writeSnapshotFixture(t, directory, ".twt2-snapshot.json", `{"version":1,"owner":"twt2","projectId":"project-one"}`)
				agents := makeSnapshotAgentsDir(t, directory)
				outside := filepath.Join(t.TempDir(), "outside.md")
				if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(agents, "aa11bb22.md")); err != nil {
					t.Fatal(err)
				}
			},
			want: "not a safe regular file",
		},
		{
			name: "Agent Session directory is a file",
			setup: func(t *testing.T, directory string) {
				writeSnapshotFixture(t, directory, ".twt2-snapshot.json", `{"version":1,"owner":"twt2","projectId":"project-one"}`)
				writeSnapshotFixture(t, directory, "agents", "not a directory")
			},
			want: "not a safe directory",
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
			_, err := store.NewSnapshotStore(stateDir).Save("project-one", "aa11bb22", "private\n")
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

func TestSnapshotStoreWritesOneFilePerAgentSessionAndOneLatestCopy(t *testing.T) {
	stateDir := t.TempDir()
	snapshots := store.NewSnapshotStore(stateDir)
	directory, err := snapshots.ProjectDir("project-one")
	if err != nil {
		t.Fatal(err)
	}

	first, err := snapshots.Save("project-one", "aa11bb22", "first agent\n")
	if err != nil {
		t.Fatal(err)
	}
	second, err := snapshots.Save("project-one", "cc33dd44", "second agent\n")
	if err != nil {
		t.Fatal(err)
	}
	wantFirst := filepath.Join(directory, "agents", "aa11bb22.md")
	wantSecond := filepath.Join(directory, "agents", "cc33dd44.md")
	if first.Agent != wantFirst || second.Agent != wantSecond {
		t.Fatalf("Agent Session snapshot paths = %q and %q", first.Agent, second.Agent)
	}
	if second.Latest != filepath.Join(directory, "latest.md") {
		t.Fatalf("latest Transcript Snapshot path = %q", second.Latest)
	}
	for path, want := range map[string]string{
		wantFirst:     "first agent\n",
		wantSecond:    "second agent\n",
		second.Latest: "second agent\n",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("file %q = %q; want %q", path, data, want)
		}
	}
	info, err := os.Lstat(wantSecond)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("Agent Session snapshot permissions = %v", info.Mode().Perm())
	}

	if exists, err := snapshots.ValidateProject("project-one", false); err != nil || !exists {
		t.Fatalf("ValidateProject() = %v, %v", exists, err)
	}
	if _, err := snapshots.Save("project-one", "not-hex", "third agent\n"); err == nil || !strings.Contains(err.Error(), "invalid Agent Session ID") {
		t.Fatalf("Save() with an invalid Agent Session ID error = %v", err)
	}

	if err := snapshots.DeleteProject("project-one", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "agents")); !os.IsNotExist(err) {
		t.Fatalf("Agent Session snapshot directory still exists: %v", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("Transcript Snapshot directory still exists: %v", err)
	}
}

func TestSnapshotStoreListMeasuresAgentSessionSnapshots(t *testing.T) {
	stateDir := t.TempDir()
	snapshots := store.NewSnapshotStore(stateDir)
	if _, err := snapshots.Save("project-one", "aa11bb22", "first agent\n"); err != nil {
		t.Fatal(err)
	}
	listed, err := snapshots.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ProjectID != "project-one" {
		t.Fatalf("List() = %+v", listed)
	}
	if listed[0].Bytes < int64(2*len("first agent\n")) {
		t.Fatalf("Transcript Snapshot bytes = %d", listed[0].Bytes)
	}
}

func makeSnapshotAgentsDir(t *testing.T, directory string) string {
	t.Helper()
	agents := filepath.Join(directory, "agents")
	if err := os.Mkdir(agents, 0o700); err != nil {
		t.Fatal(err)
	}
	return agents
}

func writeSnapshotFixture(t *testing.T, directory, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
