package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentProjectRoundTrip(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")

	name, err := LoadCurrentProject(stateDir)
	if err != nil {
		t.Fatalf("LoadCurrentProject() before save error = %v", err)
	}
	if name != "" {
		t.Fatalf("LoadCurrentProject() before save = %q", name)
	}

	if err := SaveCurrentProject(stateDir, "twt"); err != nil {
		t.Fatal(err)
	}
	name, err = LoadCurrentProject(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if name != "twt" {
		t.Fatalf("LoadCurrentProject() = %q, want twt", name)
	}

	if err := SaveCurrentProject(stateDir, "core"); err != nil {
		t.Fatal(err)
	}
	name, err = LoadCurrentProject(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if name != "core" {
		t.Fatalf("LoadCurrentProject() after second save = %q", name)
	}

	info, err := os.Stat(filepath.Join(stateDir, currentProjectFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("current-project.json mode = %v", info.Mode().Perm())
	}

	if err := ClearCurrentProject(stateDir); err != nil {
		t.Fatal(err)
	}
	name, err = LoadCurrentProject(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if name != "" {
		t.Fatalf("LoadCurrentProject() after clear = %q", name)
	}
}

func TestSaveCurrentProjectRejectsAnInvalidName(t *testing.T) {
	if err := SaveCurrentProject(t.TempDir(), "../outside"); err == nil {
		t.Fatal("SaveCurrentProject() accepted an invalid name")
	}
}
