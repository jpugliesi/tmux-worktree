package transcript_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/jpugliesi/tmux-worktree/internal/transcript"
)

func TestReadReturnsLinkedProviderTranscriptInsideProject(t *testing.T) {
	home := t.TempDir()
	repository := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-1", Repositories: []domain.ProjectRepository{{Name: "app", Path: repository}}}

	tests := []struct {
		provider string
		session  string
		path     string
		lines    []string
	}{
		{
			provider: "codex", session: "codex-session",
			path: filepath.Join(home, ".codex", "sessions", "2026", "08", "20", "rollout-codex-session.jsonl"),
			lines: []string{
				`{"type":"session_meta","payload":{"id":"codex-session","cwd":` + quoted(repository) + `}}`,
				`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Codex question"}]}}`,
				`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Codex answer"}]}}`,
			},
		},
		{
			provider: "claude", session: "claude-session",
			path: filepath.Join(home, ".claude", "projects", "-Users-alex-code-app", "claude-session.jsonl"),
			lines: []string{
				`{"sessionId":"claude-session","cwd":` + quoted(repository) + `,"type":"user","message":{"role":"user","content":"Claude question"}}`,
				`{"sessionId":"claude-session","cwd":` + quoted(repository) + `,"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Claude answer"}]}}`,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			writeLines(t, test.path, test.lines)
			got, err := transcript.New(home).Read(test.provider, test.session, project)
			if err != nil {
				t.Fatal(err)
			}
			if got.Provider != test.provider || got.SessionID != test.session || got.RepositoryName != "app" {
				t.Fatalf("Read() metadata = %+v", got)
			}
			if !strings.Contains(got.Markdown, "## User") || !strings.Contains(got.Markdown, "question") || !strings.Contains(got.Markdown, "## Assistant") || !strings.Contains(got.Markdown, "answer") {
				t.Fatalf("Read() Markdown = %q", got.Markdown)
			}
		})
	}
}

func TestReadFindsClaudeTranscriptLaunchedFromRepositorySubdirectory(t *testing.T) {
	home := t.TempDir()
	repository := filepath.Join(t.TempDir(), "app")
	subdirectory := filepath.Join(repository, "internal", "cli")
	if err := os.MkdirAll(subdirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-1", Repositories: []domain.ProjectRepository{{Name: "app", Path: repository}}}
	path := filepath.Join(home, ".claude", "projects", "-Users-alex-code-app-internal-cli", "claude-subdir-session.jsonl")
	writeLines(t, path, []string{
		`{"sessionId":"claude-subdir-session","cwd":` + quoted(subdirectory) + `,"type":"user","message":{"role":"user","content":"Claude question"}}`,
		`{"sessionId":"claude-subdir-session","cwd":` + quoted(subdirectory) + `,"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Claude answer"}]}}`,
	})
	got, err := transcript.New(home).Read("claude", "claude-subdir-session", project)
	if err != nil {
		t.Fatal(err)
	}
	if got.RepositoryName != "app" {
		t.Fatalf("Read() RepositoryName = %q", got.RepositoryName)
	}
}

func TestReadRejectsClaudePathEncodingCollisionAndUnverifiableCursorTranscript(t *testing.T) {
	home := t.TempDir()
	firstRepository := filepath.Join(t.TempDir(), "a-b", "c")
	secondRepository := filepath.Join(filepath.Dir(filepath.Dir(firstRepository)), "a", "b-c")
	for _, repository := range []string{firstRepository, secondRepository} {
		if err := os.MkdirAll(repository, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(home, ".claude", "projects", "-Users-alex-code-a-b-c", "shared-session.jsonl")
	writeLines(t, path, []string{
		`{"sessionId":"shared-session","cwd":` + quoted(firstRepository) + `,"type":"user","message":{"role":"user","content":"private"}}`,
	})
	secondProject := domain.Project{ID: "project-two", Name: "project-two", Repositories: []domain.ProjectRepository{{Name: "app", Path: secondRepository}}}
	if _, err := transcript.New(home).Read("claude", "shared-session", secondProject); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("Claude collision error = %v", err)
	}
	if _, err := transcript.New(home).Read("cursor", "cursor-session", secondProject); err == nil || !strings.Contains(err.Error(), "cannot verify") {
		t.Fatalf("Cursor ownership error = %v", err)
	}
}

func TestReadRejectsCodexTranscriptFromAnotherProject(t *testing.T) {
	home := t.TempDir()
	repository := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".codex", "sessions", "2026", "08", "20", "rollout-wrong-project.jsonl")
	writeLines(t, path, []string{
		`{"type":"session_meta","payload":{"id":"wrong-project","cwd":"/different/project"}}`,
		`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"secret"}]}}`,
	})
	project := domain.Project{ID: "project-1", Repositories: []domain.ProjectRepository{{Name: "app", Path: repository}}}
	if _, err := transcript.New(home).Read("codex", "wrong-project", project); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("Read() error = %v", err)
	}
}

func TestReadRejectsMixedSessionAndDirectoryRecords(t *testing.T) {
	home := t.TempDir()
	repository := filepath.Join(t.TempDir(), "app")
	otherRepository := filepath.Join(t.TempDir(), "other")
	for _, path := range []string{repository, otherRepository} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	project := domain.Project{ID: "project-1", Name: "project-1", Repositories: []domain.ProjectRepository{{Name: "app", Path: repository}}}

	codexPath := filepath.Join(home, ".codex", "sessions", "rollout-codex-mixed.jsonl")
	writeLines(t, codexPath, []string{
		`{"type":"session_meta","payload":{"id":"codex-mixed","cwd":` + quoted(repository) + `}}`,
		`{"type":"session_meta","payload":{"id":"another-session","cwd":` + quoted(otherRepository) + `}}`,
		`{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"private"}]}}`,
	})
	if _, err := transcript.New(home).Read("codex", "codex-mixed", project); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("mixed Codex transcript error = %v", err)
	}

	claudePath := filepath.Join(home, ".claude", "projects", "-Users-alex-code-app", "claude-mixed.jsonl")
	writeLines(t, claudePath, []string{
		`{"sessionId":"claude-mixed","cwd":` + quoted(repository) + `,"type":"user","message":{"role":"user","content":"allowed"}}`,
		`{"sessionId":"another-session","cwd":` + quoted(otherRepository) + `,"type":"assistant","message":{"role":"assistant","content":"private"}}`,
	})
	if _, err := transcript.New(home).Read("claude", "claude-mixed", project); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("mixed Claude transcript error = %v", err)
	}
}

func TestReadRejectsUnsafeTranscriptSourcesAndSessionIDs(t *testing.T) {
	home := t.TempDir()
	repository := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project-1", Repositories: []domain.ProjectRepository{{Name: "app", Path: repository}}}
	service := transcript.New(home)
	if _, err := service.Read("codex", "../outside", project); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("traversal session ID error = %v", err)
	}

	outside := filepath.Join(t.TempDir(), "rollout-linked-session.jsonl")
	writeLines(t, outside, []string{`{"type":"session_meta","payload":{"id":"linked-session","cwd":` + quoted(repository) + `}}`})
	link := filepath.Join(home, ".codex", "sessions", "rollout-linked-session.jsonl")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Read("codex", "linked-session", project); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("linked transcript error = %v", err)
	}

	large := filepath.Join(home, ".codex", "sessions", "rollout-large-session.jsonl")
	if err := os.WriteFile(large, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(large, 33<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Read("codex", "large-session", project); err == nil || !strings.Contains(err.Error(), "safe regular file") {
		t.Fatalf("large transcript error = %v", err)
	}
}

func TestSnapshotDoesNotCommitAfterConcurrentProjectRemoval(t *testing.T) {
	home := t.TempDir()
	stateDir := t.TempDir()
	repository := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "project-concurrent", Name: "concurrent",
		Repositories: []domain.ProjectRepository{{Name: "app", Path: repository}}, CreatedAt: time.Now().UTC(),
	}
	agent := domain.AgentSession{
		Version: domain.AgentVersion, ID: "agent-concurrent", ProjectID: project.ID,
		Provider: "codex", ProviderSessionID: "session-concurrent", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	writeLines(t, filepath.Join(home, ".codex", "sessions", "rollout-session-concurrent.jsonl"), []string{
		`{"type":"session_meta","payload":{"id":"session-concurrent","cwd":` + quoted(repository) + `}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"private"}]}}`,
	})
	projects := store.NewProjectStore(stateDir)
	if err := projects.Save(project); err != nil {
		t.Fatal(err)
	}
	if err := store.NewAgentStore(stateDir).Save(agent); err != nil {
		t.Fatal(err)
	}
	removalLock, err := store.AcquireMutationLock(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, _, err := transcript.New(home).Snapshot(stateDir, agent.ID, project.ID, true)
		result <- err
	}()
	select {
	case err := <-result:
		removalLock.Release()
		t.Fatalf("snapshot returned while Project removal held the mutation lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := projects.Delete(project.ID); err != nil {
		removalLock.Release()
		t.Fatal(err)
	}
	if err := removalLock.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("snapshot after Project removal error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("snapshot did not finish after Project removal released the mutation lock")
	}
	directory, err := store.NewSnapshotStore(stateDir).ProjectDir(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("snapshot was committed after Project removal: %v", err)
	}
}

func quoted(value string) string {
	return `"` + strings.ReplaceAll(value, `\`, `\\`) + `"`
}

func writeLines(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
