package agent

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/transcript"
)

func TestBoundedPanePreviewLimitsLinesAndTotalBytes(t *testing.T) {
	text := strings.Repeat("a", maxPanePreviewLineBytes+100) + "\n" + strings.Repeat("界", maxPanePreviewBytes)
	got, truncated := boundedPanePreview(text)
	if !truncated {
		t.Fatal("boundedPanePreview() did not report truncation")
	}
	if len(got) > maxPanePreviewBytes {
		t.Fatalf("boundedPanePreview() returned %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("boundedPanePreview() returned invalid UTF-8")
	}
	for _, line := range strings.Split(got, "\n") {
		if len(line) > maxPanePreviewLineBytes {
			t.Fatalf("boundedPanePreview() returned a %d-byte line", len(line))
		}
	}
}

func TestTranscriptReferencesAreProviderQualified(t *testing.T) {
	codex := CatalogEntry{Reference: TranscriptReference("codex", "same-session"), Provider: "codex", ProviderSessionID: "same-session"}
	claude := CatalogEntry{Reference: TranscriptReference("claude", "same-session"), Provider: "claude", ProviderSessionID: "same-session"}
	if codex.Reference == claude.Reference {
		t.Fatal("provider-qualified transcript references are equal")
	}
	if _, err := findCatalogEntry([]CatalogEntry{codex, claude}, "same-session"); clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("raw provider-scoped session ID error = %v", err)
	}
	got, err := findCatalogEntry([]CatalogEntry{codex, claude}, codex.Reference)
	if err != nil || got.Provider != "codex" {
		t.Fatalf("qualified transcript lookup = %+v, %v", got, err)
	}
}

func TestPanePreviewSanitizerRemovesTerminalControls(t *testing.T) {
	text := transcript.SanitizeUntrusted("safe\x1b]52;c;secret\a\x1b[31m red\x1b[0m\x00\n")
	got, _ := boundedPanePreview(text)
	if strings.ContainsAny(got, "\x00\x1b\a") || strings.Contains(got, "secret") {
		t.Fatalf("sanitized preview = %q", got)
	}
	if !strings.Contains(got, "safe") || !strings.Contains(got, "red") {
		t.Fatalf("sanitized preview lost visible text: %q", got)
	}
}
