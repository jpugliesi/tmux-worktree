package ticket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

// legacyFixture mirrors the shape of a real pre-twt2 vault ticket: legacy
// keys (id, type, category, project, parent), a quoted title, two-space alias
// indent, a flow-style empty blocked_by, and empty claim fields.
const legacyFixture = `---
title: "Reconnect Change Monitor VFS tools"
aliases:
  - Reconnect Change Monitor VFS tools
id: tkt-cm-001
tags:
  - tickets
type: task
category: enhancement
status: ready-for-agent
priority: 2
project: "[[Change Monitor Agent]]"
parent:
blocked_by: []
claimed_by:
claimed_at:
created: 2026-08-20
updated: 2026-08-20
---

# Reconnect Change Monitor VFS tools

## What to build

Reconnect the tools. See [[Change Monitor Agent]].

## Blocked by

None - can start immediately

## Comments
`

func TestGoldenRoundTripWithoutMutationIsByteIdentical(t *testing.T) {
	file, err := ParseTicketFile("tkt-cm-001.md", []byte(legacyFixture))
	if err != nil {
		t.Fatalf("ParseTicketFile: %v", err)
	}
	rendered, err := file.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(rendered) != legacyFixture {
		t.Fatalf("round trip changed the file.\n---got---\n%s\n---want---\n%s", rendered, legacyFixture)
	}
}

// TestGoldenClaimTouchesOnlyClaimLines pins the lossless mutation contract: a
// claim on a legacy file changes only claimed_by, claimed_at, updated, and
// board. Every other line stays byte-identical and in order.
func TestGoldenClaimTouchesOnlyClaimLines(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "change-monitor", "tkt-cm-001.md")
	writeFixture(t, path, legacyFixture)

	if _, err := service.Claim("tkt-cm-001", "codex-fix-auth", false); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	touched := func(line string) bool {
		for _, prefix := range []string{"claimed_by:", "claimed_at:", "updated:", "board:"} {
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
		return false
	}
	keep := func(content string) []string {
		var kept []string
		for _, line := range strings.Split(content, "\n") {
			if !touched(line) {
				kept = append(kept, line)
			}
		}
		return kept
	}
	before := keep(legacyFixture)
	got := keep(string(after))
	if len(before) != len(got) {
		t.Fatalf("untouched line count changed: %d -> %d\n---after---\n%s", len(before), len(got), after)
	}
	for i := range before {
		if before[i] != got[i] {
			t.Fatalf("untouched line %d changed: %q -> %q", i, before[i], got[i])
		}
	}
	for _, want := range []string{
		"\nclaimed_by: codex-fix-auth\n",
		"\nclaimed_at: 2026-08-20\n",
		"\nupdated: 2026-08-20\n",
		"\nboard: change-monitor\n",
	} {
		if !strings.Contains(string(after), want) {
			t.Fatalf("claimed file misses %q\n---after---\n%s", want, after)
		}
	}
}

func TestParseRejectsCRLF(t *testing.T) {
	raw := strings.ReplaceAll(legacyFixture, "\n", "\r\n")
	_, err := ParseTicketFile("crlf.md", []byte(raw))
	if clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("CRLF parse error = %v, want unsafe_state", err)
	}
	if !strings.Contains(err.Error(), "CRLF") {
		t.Fatalf("error %q does not name CRLF", err)
	}
	if !strings.Contains(clierr.HintOf(err), "LF") {
		t.Fatalf("hint %q does not tell the user to convert to LF", clierr.HintOf(err))
	}
}

func TestMutationRefusesCRLFFile(t *testing.T) {
	service, home := newTestService(t)
	path := filepath.Join(home, "crlf-ticket.md")
	writeFixture(t, path, strings.ReplaceAll(fixture{title: "CRLF ticket", status: "ready-for-agent"}.content(), "\n", "\r\n"))

	_, err := service.Claim("crlf-ticket", "agent-a", false)
	if clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("Claim on a CRLF file = %v, want unsafe_state", err)
	}
	if !strings.Contains(err.Error(), "CRLF") {
		t.Fatalf("error %q does not name CRLF", err)
	}
}

func TestParseUnterminatedFence(t *testing.T) {
	_, err := ParseTicketFile("broken.md", []byte("---\ntitle: \"X\"\nstatus: needs-triage\n"))
	if clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("unterminated fence error = %v, want unsafe_state", err)
	}
	if !strings.Contains(err.Error(), "unterminated frontmatter fence") {
		t.Fatalf("error %q does not name the unterminated fence", err)
	}
}

func TestParseWithoutFrontmatter(t *testing.T) {
	raw := "# Just a note\n\nBody only.\n"
	file, err := ParseTicketFile("note.md", []byte(raw))
	if err != nil {
		t.Fatalf("ParseTicketFile: %v", err)
	}
	if file.Doc != nil {
		t.Fatal("Doc must be nil without frontmatter")
	}
	if file.Body != raw {
		t.Fatalf("Body = %q, want the whole file", file.Body)
	}
	if file.Mapping() != nil {
		t.Fatal("Mapping must be nil-safe")
	}
}

func TestParseKeepsBodyByteExact(t *testing.T) {
	raw := "---\ntitle: \"X\"\n---\nno leading blank line\n\ttab kept\n"
	file, err := ParseTicketFile("x.md", []byte(raw))
	if err != nil {
		t.Fatalf("ParseTicketFile: %v", err)
	}
	if file.Body != "no leading blank line\n\ttab kept\n" {
		t.Fatalf("Body = %q", file.Body)
	}
	rendered, err := file.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(rendered) != raw {
		t.Fatalf("render = %q, want %q", rendered, raw)
	}
}

func TestStripWikiLink(t *testing.T) {
	tests := map[string]string{
		"[[some-ticket]]":         "some-ticket",
		"[[some-ticket|Display]]": "some-ticket",
		"[[some-ticket#Heading]]": "some-ticket",
		"  [[some-ticket]]  ":     "some-ticket",
		"bare-slug":               "bare-slug",
		"[[a|b#c]]":               "a",
		"":                        "",
	}
	for input, want := range tests {
		if got := stripWikiLink(input); got != want {
			t.Errorf("stripWikiLink(%q) = %q, want %q", input, got, want)
		}
	}
}

// newTestService builds a Service on temporary directories with a fixed
// local clock.
func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	service := NewService(Options{Home: home, StateDir: t.TempDir()})
	service.now = func() time.Time { return time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local) }
	return service, home
}

// writeFixture writes one raw ticket file, creating the Board directory.
func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture builds a small v1-shaped ticket file.
type fixture struct {
	title     string
	status    string
	priority  string
	claimedBy string
	blocked   []string
	aliases   []string
	body      string
}

func (f fixture) content() string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("title: \"" + f.title + "\"\n")
	aliases := f.aliases
	if aliases == nil {
		aliases = []string{f.title}
	}
	b.WriteString("aliases:\n")
	for _, alias := range aliases {
		b.WriteString("  - " + alias + "\n")
	}
	b.WriteString("tags:\n  - tickets\n")
	b.WriteString("status: " + f.status + "\n")
	if f.priority != "" {
		b.WriteString("priority: " + f.priority + "\n")
	}
	if len(f.blocked) == 0 {
		b.WriteString("blocked_by: []\n")
	} else {
		b.WriteString("blocked_by:\n")
		for _, blocker := range f.blocked {
			b.WriteString("  - \"[[" + blocker + "]]\"\n")
		}
	}
	if f.claimedBy == "" {
		b.WriteString("claimed_by:\nclaimed_at:\n")
	} else {
		b.WriteString("claimed_by: " + f.claimedBy + "\nclaimed_at: 2026-08-02\n")
	}
	b.WriteString("created: 2026-08-01\nupdated: 2026-08-01\n---\n")
	if f.body != "" {
		b.WriteString(f.body)
	} else {
		b.WriteString("\n# " + f.title + "\n\n## Comments\n")
	}
	return b.String()
}
