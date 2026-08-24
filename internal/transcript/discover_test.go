package transcript_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/jpugliesi/tmux-worktree/internal/transcript"
)

func TestDiscoverFindsWorkspaceSessionsFromTheNewestToTheOldest(t *testing.T) {
	home := t.TempDir()
	workspace, repository := discoverWorkspace(t)
	otherRepository := filepath.Join(t.TempDir(), "other")
	if err := os.MkdirAll(otherRepository, 0o755); err != nil {
		t.Fatal(err)
	}
	codexPath := writeCodexSession(t, home, "codex-old", repository)
	claudePath := writeClaudeSession(t, home, "claude-new", repository)
	outsidePath := writeCodexSession(t, home, "codex-outside", otherRepository)
	setModTime(t, codexPath, time.Now().Add(-2*time.Hour))
	setModTime(t, claudePath, time.Now().Add(-1*time.Hour))
	setModTime(t, outsidePath, time.Now())

	found, err := transcript.New(home, "").Discover(workspace, transcript.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("Discover() = %+v", found)
	}
	if found[0].SessionID != "claude-new" || found[0].Provider != "claude" || found[0].RepositoryName != "app" {
		t.Fatalf("Discover() newest = %+v", found[0])
	}
	if found[1].SessionID != "codex-old" || found[1].Provider != "codex" {
		t.Fatalf("Discover() oldest = %+v", found[1])
	}
	if found[0].LastActivity.Before(found[1].LastActivity) {
		t.Fatalf("Discover() is not sorted by last activity: %+v", found)
	}
}

func TestDiscoverSkipsLinkedSessionsAndOtherProviders(t *testing.T) {
	home := t.TempDir()
	workspace, repository := discoverWorkspace(t)
	writeCodexSession(t, home, "codex-linked", repository)
	writeCodexSession(t, home, "codex-free", repository)
	writeClaudeSession(t, home, "claude-free", repository)
	linked := []domain.AgentSession{{ID: "agent-one", WorkspaceID: workspace.ID, Provider: "codex", ProviderSessionID: "codex-linked"}}

	found, err := transcript.New(home, "").Discover(workspace, transcript.DiscoverOptions{Provider: "codex", Linked: linked})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].SessionID != "codex-free" {
		t.Fatalf("Discover() = %+v", found)
	}
}

func TestDiscoverKeepsOtherProviderResultsWhenOneStoreFails(t *testing.T) {
	home := t.TempDir()
	workspace, repository := discoverWorkspace(t)
	writeCodexSession(t, home, "codex-valid", repository)
	if err := os.WriteFile(filepath.Join(home, ".claude"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	found, err := transcript.New(home, "").Discover(workspace, transcript.DiscoverOptions{})
	if err == nil || !strings.Contains(err.Error(), "claude transcript discovery") {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(found) != 1 || found[0].Provider != "codex" || found[0].SessionID != "codex-valid" {
		t.Fatalf("Discover() partial result = %+v", found)
	}
}

func TestDiscoverIncludesALinkOutsideTheNewestDiscoveryWindowInOneScan(t *testing.T) {
	home := t.TempDir()
	workspace, repository := discoverWorkspace(t)
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	old := writeCodexSession(t, home, "linked-old", repository)
	now := time.Now()
	setModTime(t, old, now.Add(-time.Hour))
	for index := 0; index < 256; index++ {
		path := writeCodexSession(t, home, fmt.Sprintf("outside-%03d", index), outside)
		setModTime(t, path, now.Add(time.Duration(index)*time.Second))
	}

	found, err := transcript.New(home, "").Discover(workspace, transcript.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("Discover() included a session outside its newest-file window: %+v", found)
	}
	linked := []domain.AgentSession{{Provider: "codex", ProviderSessionID: "linked-old"}}
	verified, err := transcript.New(home, "").Discover(workspace, transcript.DiscoverOptions{Linked: linked, IncludeLinked: true})
	if err != nil || len(verified) != 1 || verified[0].SessionID != "linked-old" || verified[0].RepositoryName != "app" {
		t.Fatalf("Discover(IncludeLinked) = %+v, %v", verified, err)
	}
}

func TestReadLinkedSavesTheOnlyNewProviderSession(t *testing.T) {
	home := t.TempDir()
	stateDir := t.TempDir()
	workspace, repository := discoverWorkspace(t)
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	agent := domain.AgentSession{
		Version: domain.AgentVersion, ID: "agent-one", WorkspaceID: workspace.ID, Provider: "codex",
		Label: "codex", CreatedAt: time.Now().Add(-time.Hour).UTC(), UpdatedAt: time.Now().Add(-time.Hour).UTC(),
	}
	if err := store.NewAgentStore(stateDir).Save(agent); err != nil {
		t.Fatal(err)
	}
	writeCodexSession(t, home, "codex-one", repository)

	value, err := transcript.New(home, stateDir).ReadLinked(agent, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if value.SessionID != "codex-one" || !strings.Contains(value.Markdown, "question") {
		t.Fatalf("ReadLinked() = %+v", value)
	}
	saved, err := store.NewAgentStore(stateDir).Find(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ProviderSessionID != "codex-one" {
		t.Fatalf("saved provider session ID = %q", saved.ProviderSessionID)
	}
}

func TestReadLinkedExplainsZeroAndSeveralCandidates(t *testing.T) {
	home := t.TempDir()
	stateDir := t.TempDir()
	workspace, repository := discoverWorkspace(t)
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	created := time.Now().Add(-time.Hour).UTC()
	agent := domain.AgentSession{
		Version: domain.AgentVersion, ID: "agent-one", WorkspaceID: workspace.ID, Provider: "codex",
		Label: "codex", CreatedAt: created, UpdatedAt: created,
	}
	if err := store.NewAgentStore(stateDir).Save(agent); err != nil {
		t.Fatal(err)
	}
	service := transcript.New(home, stateDir)

	_, err := service.ReadLinked(agent, workspace)
	if err == nil || !strings.Contains(err.Error(), "has no linked provider session ID") {
		t.Fatalf("ReadLinked() without candidates error = %v", err)
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "twt agents discover --workspace "+workspace.ID) {
		t.Fatalf("ReadLinked() hint = %q", hint)
	}

	// An older provider session does not belong to this Agent Session.
	old := writeCodexSession(t, home, "codex-old", repository)
	setModTime(t, old, created.Add(-time.Hour))
	if _, err := service.ReadLinked(agent, workspace); err == nil || !strings.Contains(err.Error(), "has no linked provider session ID") {
		t.Fatalf("ReadLinked() with an older session error = %v", err)
	}

	writeCodexSession(t, home, "codex-one", repository)
	writeCodexSession(t, home, "codex-two", repository)
	_, err = service.ReadLinked(agent, workspace)
	if err == nil || !strings.Contains(err.Error(), "matches 2 provider sessions") {
		t.Fatalf("ReadLinked() with several candidates error = %v", err)
	}
	for _, want := range []string{"codex-one", "codex-two"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ReadLinked() error %q does not name %q", err, want)
		}
	}
	saved, err := store.NewAgentStore(stateDir).Find(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ProviderSessionID != "" {
		t.Fatalf("ReadLinked() saved a link for several candidates: %q", saved.ProviderSessionID)
	}
}

func TestDiscoverFindsSessionsWhenTheTranscriptBodyExceedsTheReadLimit(t *testing.T) {
	home := t.TempDir()
	workspace, repository := discoverWorkspace(t)
	codexPath := writeCodexSession(t, home, "codex-large", repository)
	claudePath := writeClaudeSession(t, home, "claude-large", repository)
	grokPath := writeGrokSession(t, home, "01a02626-4685-7c72-9679-cccccccccccc", repository, "Q", "A")
	events := filepath.Join(filepath.Dir(grokPath), "events.jsonl")
	writeLines(t, events, []string{`{"type":"user","content":"noise"}`})
	for _, path := range []string{codexPath, claudePath, grokPath, events} {
		extendFile(t, path, 32<<20+1)
	}

	found, err := transcript.New(home, "").Discover(workspace, transcript.DiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, session := range found {
		got[session.Provider] = session.SessionID
	}
	want := map[string]string{
		"codex":  "codex-large",
		"claude": "claude-large",
		"grok":   "01a02626-4685-7c72-9679-cccccccccccc",
	}
	if len(got) != len(want) {
		t.Fatalf("Discover() = %+v", found)
	}
	for provider, sessionID := range want {
		if got[provider] != sessionID {
			t.Fatalf("Discover() %s = %q, want %q from %+v", provider, got[provider], sessionID, found)
		}
	}
}

func TestResumeCommandUsesTheProviderFlag(t *testing.T) {
	if err := transcript.ValidateProviders(); err != nil {
		t.Fatal(err)
	}
	if got := transcript.ResumeCommand("codex", "session-one"); strings.Join(got, " ") != "codex resume session-one" {
		t.Fatalf("ResumeCommand(codex) = %v", got)
	}
	if got := transcript.ResumeCommand("claude", "session-one"); strings.Join(got, " ") != "claude --resume session-one" {
		t.Fatalf("ResumeCommand(claude) = %v", got)
	}
	if got := transcript.ResumeCommand("grok", "session-one"); strings.Join(got, " ") != "grok --resume session-one" {
		t.Fatalf("ResumeCommand(grok) = %v", got)
	}
	if got := transcript.ResumeCommand("cursor", "session-one"); got != nil {
		t.Fatalf("ResumeCommand(cursor) = %v", got)
	}
}

func discoverWorkspace(t *testing.T) (domain.Workspace, string) {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	return domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-one", Name: "workspace-one", Status: domain.WorkspaceActive,
		Root: filepath.Dir(repository), Repositories: []domain.WorkspaceRepository{{Name: "app", Path: repository}},
		CreatedAt: now, UpdatedAt: now,
	}, repository
}

func writeCodexSession(t *testing.T, home, sessionID, repository string) string {
	t.Helper()
	path := filepath.Join(home, ".codex", "sessions", "2026", "08", "20", "rollout-"+sessionID+".jsonl")
	writeLines(t, path, []string{
		`{"type":"session_meta","payload":{"id":"` + sessionID + `","cwd":` + quoted(repository) + `}}`,
		`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"Codex question"}]}}`,
	})
	return path
}

func writeClaudeSession(t *testing.T, home, sessionID, repository string) string {
	t.Helper()
	path := filepath.Join(home, ".claude", "projects", "-Users-alex-code-app", sessionID+".jsonl")
	writeLines(t, path, []string{
		`{"sessionId":"` + sessionID + `","cwd":` + quoted(repository) + `,"type":"user","message":{"role":"user","content":"Claude question"}}`,
	})
	return path
}

func setModTime(t *testing.T, path string, value time.Time) {
	t.Helper()
	if err := os.Chtimes(path, value, value); err != nil {
		t.Fatal(err)
	}
}

func extendFile(t *testing.T, path string, size int64) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
}
