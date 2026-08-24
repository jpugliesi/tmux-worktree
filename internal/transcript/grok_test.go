package transcript_test

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/transcript"
)

func TestReadAndDiscoverGrokSessions(t *testing.T) {
	home := t.TempDir()
	workspace, repository := discoverWorkspace(t)
	other := filepath.Join(t.TempDir(), "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGrokSession(t, home, "01a02626-4685-7c72-9679-5dbf6dec43ce", repository, "Grok question", "Grok answer")
	writeGrokSession(t, home, "01a02626-4685-7c72-9679-aaaaaaaaaaaa", other, "Outside question", "Outside answer")

	found, err := transcript.New(home, "").Discover(workspace, transcript.DiscoverOptions{Provider: "grok"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].SessionID != "01a02626-4685-7c72-9679-5dbf6dec43ce" || found[0].Provider != "grok" || found[0].RepositoryName != "app" {
		t.Fatalf("Discover(grok) = %+v", found)
	}

	got, err := transcript.New(home, "").Read("grok", "01a02626-4685-7c72-9679-5dbf6dec43ce", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got.Provider != "grok" || got.RepositoryName != "app" {
		t.Fatalf("Read(grok) metadata = %+v", got)
	}
	if !strings.Contains(got.Markdown, "Grok question") || !strings.Contains(got.Markdown, "Grok answer") || strings.Contains(got.Markdown, "system-reminder") {
		t.Fatalf("Read(grok) Markdown = %q", got.Markdown)
	}

	if _, err := transcript.New(home, "").Read("grok", "01a02626-4685-7c72-9679-aaaaaaaaaaaa", workspace); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("Read(grok) outside Workspace error = %v", err)
	}
}

func TestGrokDiscoveryIgnoresSiblingJSONLFiles(t *testing.T) {
	home := t.TempDir()
	workspace, repository := discoverWorkspace(t)
	sessionID := "01a02626-4685-7c72-9679-bbbbbbbbbbbb"
	chat := writeGrokSession(t, home, sessionID, repository, "Keep this", "Keep that")
	writeLines(t, filepath.Join(filepath.Dir(chat), "events.jsonl"), []string{`{"type":"user","content":"noise"}`})

	found, err := transcript.New(home, "").Discover(workspace, transcript.DiscoverOptions{Provider: "grok"})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].SessionID != sessionID {
		t.Fatalf("Discover(grok) with sibling files = %+v", found)
	}
}

func writeGrokSession(t *testing.T, home, sessionID, repository, question, answer string) string {
	t.Helper()
	dir := filepath.Join(home, ".grok", "sessions", strings.ReplaceAll(url.PathEscape(repository), "/", "%2F"), sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	summary := `{"info":{"id":"` + sessionID + `","cwd":` + quoted(repository) + `}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "chat_history.jsonl")
	writeLines(t, path, []string{
		`{"type":"system","content":"You are Grok."}`,
		`{"type":"user","content":[{"type":"text","text":"<system-reminder>\nskills\n</system-reminder>"}],"synthetic_reason":"system_reminder"}`,
		`{"type":"user","content":[{"type":"text","text":` + quoted(question) + `}]}`,
		`{"type":"assistant","content":[{"type":"text","text":` + quoted(answer) + `}]}`,
	})
	return path
}
