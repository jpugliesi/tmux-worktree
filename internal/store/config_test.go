package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func writeConfig(t *testing.T, configDir, content string) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadConfigReadsTicketsHome(t *testing.T) {
	configDir := t.TempDir()
	writeConfig(t, configDir, "ticketsHome: /vault/tickets\n")
	config, err := store.LoadConfig(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if config.TicketsHome != "/vault/tickets" {
		t.Fatalf("TicketsHome = %q, want %q", config.TicketsHome, "/vault/tickets")
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	configDir := t.TempDir()
	writeConfig(t, configDir, "ticketsHome: /vault/tickets\nclaimant: me\n")
	if _, err := store.LoadConfig(configDir); err == nil || !strings.Contains(err.Error(), "claimant") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestLoadConfigRejectsMultipleDocuments(t *testing.T) {
	configDir := t.TempDir()
	writeConfig(t, configDir, "ticketsHome: /vault/tickets\n---\nticketsHome: /other\n")
	if _, err := store.LoadConfig(configDir); err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("multi-document error = %v", err)
	}
}

func TestLoadConfigReturnsTheZeroConfigForAMissingFile(t *testing.T) {
	config, err := store.LoadConfig(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if config != (store.Config{}) {
		t.Fatalf("missing file Config = %#v, want the zero Config", config)
	}
}

func TestLoadConfigTreatsAnEmptyFileAsTheZeroConfig(t *testing.T) {
	configDir := t.TempDir()
	writeConfig(t, configDir, "")
	config, err := store.LoadConfig(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if config != (store.Config{}) {
		t.Fatalf("empty file Config = %#v, want the zero Config", config)
	}
}
