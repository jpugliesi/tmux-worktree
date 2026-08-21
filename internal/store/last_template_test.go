package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLastTemplateRoundTrip(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")

	name, err := LoadLastTemplate(stateDir)
	if err != nil {
		t.Fatalf("LoadLastTemplate() before save error = %v", err)
	}
	if name != "" {
		t.Fatalf("LoadLastTemplate() before save = %q", name)
	}

	if err := SaveLastTemplate(stateDir, "everysphere"); err != nil {
		t.Fatal(err)
	}
	name, err = LoadLastTemplate(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if name != "everysphere" {
		t.Fatalf("LoadLastTemplate() = %q, want %q", name, "everysphere")
	}

	if err := SaveLastTemplate(stateDir, "other"); err != nil {
		t.Fatal(err)
	}
	name, err = LoadLastTemplate(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if name != "other" {
		t.Fatalf("LoadLastTemplate() after second save = %q", name)
	}

	info, err := os.Stat(filepath.Join(stateDir, "last-template.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("last-template.json mode = %v", info.Mode().Perm())
	}
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("state directory entries = %d, want only the record", len(entries))
	}
}

func TestSaveLastTemplateRejectsAnInvalidName(t *testing.T) {
	if err := SaveLastTemplate(t.TempDir(), "../outside"); err == nil {
		t.Fatal("SaveLastTemplate() accepted an invalid name")
	}
}
