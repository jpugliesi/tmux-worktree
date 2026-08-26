package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureCodexTrustWritesOneEntry(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), ".codex", "config.toml")
	dir := "/work/space one"
	if err := ensureCodexTrust(configPath, dir); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := ensureCodexTrust(configPath, dir); err != nil {
		t.Fatalf("idempotent write: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), `[projects."/work/space one"]`); got != 1 {
		t.Fatalf("trust entries = %d, want 1\n%s", got, data)
	}
	if !strings.Contains(string(data), "trust_level = \"trusted\"") {
		t.Fatalf("missing trust level:\n%s", data)
	}
	// A second directory appends without touching the first.
	if err := ensureCodexTrust(configPath, "/work/other"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(configPath)
	if strings.Count(string(data), "trust_level") != 2 {
		t.Fatalf("expected two entries:\n%s", data)
	}
}
