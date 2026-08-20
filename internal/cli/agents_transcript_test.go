package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestAgentTranscriptUsesExplicitProviderSessionAndProject(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	repository := filepath.Join(root, "project", "app")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "project-one", Name: "project-one",
		Status: domain.ProjectActive, Root: filepath.Dir(repository), TmuxSession: "project-one",
		Repositories: []domain.ProjectRepository{{Name: "app", Path: repository}},
		CreatedAt:    time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	stateDir := filepath.Join(root, "state")
	if err := store.NewProjectStore(stateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	transcriptPath := filepath.Join(home, ".codex", "sessions", "2026", "08", "20", "rollout-session-one.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := `{"type":"session_meta","payload":{"id":"session-one","cwd":` + quoteJSON(t, repository) + `}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"Project question"}]}}
{"type":"response_item","payload":{"role":"assistant","content":[{"type":"output_text","text":"Project answer"}]}}
`
	if err := os.WriteFile(transcriptPath, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}
	registration := executeWithOptions(t, options, nil,
		"agents", "register", "--project", project.ID, "--provider", "codex", "--label", "review",
		"--session", "session-one", "--", "codex", "resume", "session-one",
	)
	fields := strings.Fields(registration)
	if len(fields) < 4 {
		t.Fatalf("registration output = %q", registration)
	}
	agentID := fields[3]

	list := executeWithOptions(t, options, nil, "agents", "list", "--project", project.ID, "--output", "json")
	if strings.Contains(list, transcriptPath) || strings.Contains(list, "tmuxPane") {
		t.Fatalf("Agent JSON exposes an internal path or tmux target: %s", list)
	}
	var listed struct {
		Agents []struct {
			ID           string `json:"id"`
			Capabilities struct {
				CanReadTranscript bool `json:"canReadTranscript"`
			} `json:"capabilities"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(list), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Agents) != 1 || listed.Agents[0].ID != agentID || !listed.Agents[0].Capabilities.CanReadTranscript {
		t.Fatalf("agents list = %+v", listed)
	}

	shown := executeWithOptions(t, options, nil, "agents", "transcript", "show", agentID, "--project", project.ID, "--output", "json")
	if strings.Contains(shown, transcriptPath) {
		t.Fatalf("transcript JSON exposes its source path: %s", shown)
	}
	var result struct {
		SchemaVersion int    `json:"schemaVersion"`
		ProjectID     string `json:"projectId"`
		AgentID       string `json:"agentId"`
		Markdown      string `json:"markdown"`
	}
	if err := json.Unmarshal([]byte(shown), &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != 1 || result.ProjectID != project.ID || result.AgentID != agentID || !strings.Contains(result.Markdown, "Project question") {
		t.Fatalf("transcript show = %+v", result)
	}

	snapshotDirectory := filepath.Join(stateDir, "snapshots", "projects", project.ID)
	drySnapshot := executeWithOptions(t, options, nil, "agents", "transcript", "snapshot", agentID, "--project", project.ID, "--dry-run", "--output", "json")
	if !strings.Contains(drySnapshot, `"status":"valid"`) || strings.Contains(drySnapshot, "Project question") {
		t.Fatalf("dry-run transcript snapshot output = %s", drySnapshot)
	}
	if _, err := os.Stat(snapshotDirectory); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a Transcript Snapshot: %v", err)
	}

	snapshot := executeWithOptions(t, options, nil, "agents", "transcript", "snapshot", agentID, "--project", project.ID, "--output", "json")
	if !strings.Contains(snapshot, `"status":"applied"`) || strings.Contains(snapshot, "Project question") {
		t.Fatalf("transcript snapshot output = %s", snapshot)
	}
	saved, err := os.ReadFile(filepath.Join(snapshotDirectory, "latest.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "Project question") {
		t.Fatalf("saved Transcript Snapshot = %q", saved)
	}
	marker, err := os.ReadFile(filepath.Join(snapshotDirectory, ".twt2-snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(marker), `"projectId":"project-one"`) {
		t.Fatalf("Transcript Snapshot marker = %s", marker)
	}
}

func TestAgentTranscriptLinkUpdatesAnExistingAgentSession(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	now := time.Now().UTC()
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "project-one", Name: "project-one", Status: domain.ProjectActive,
		Root: filepath.Join(root, "project"), TmuxSession: "project-one", CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewProjectStore(stateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	agent := domain.AgentSession{
		Version: domain.AgentVersion, ID: "agent-one", ProjectID: project.ID, Provider: "codex",
		Label: "review", ResumeCommand: []string{"codex", "resume", "session-one"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewAgentStore(stateDir).Save(agent); err != nil {
		t.Fatal(err)
	}
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}
	executeWithOptions(t, options, nil,
		"agents", "transcript", "link", agent.ID, "--project", project.ID, "--session", "session-one",
	)
	linked, err := store.NewAgentStore(stateDir).Find(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if linked.ProviderSessionID != "session-one" {
		t.Fatalf("linked provider session ID = %q", linked.ProviderSessionID)
	}
}

func TestAgentTranscriptRejectsUnsupportedCursorLink(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	now := time.Now().UTC()
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "project-one", Name: "project-one", Status: domain.ProjectActive,
		Root: filepath.Join(root, "project"), TmuxSession: "project-one", CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewProjectStore(stateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	agent := domain.AgentSession{
		Version: domain.AgentVersion, ID: "cursor-agent", ProjectID: project.ID, Provider: "cursor",
		Label: "cursor", ResumeCommand: []string{"cursor-agent", "resume"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewAgentStore(stateDir).Save(agent); err != nil {
		t.Fatal(err)
	}
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}
	command := cli.New(options)
	command.SetArgs([]string{"agents", "transcript", "link", agent.ID, "--project", project.ID, "--session", "cursor-session"})
	if _, err := command.ExecuteC(); err == nil || !strings.Contains(err.Error(), "does not support verifiable linked transcripts") {
		t.Fatalf("Cursor transcript link error = %v", err)
	}

	stored, err := store.NewAgentStore(stateDir).Find(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ProviderSessionID != "" {
		t.Fatalf("Cursor transcript link was saved: %q", stored.ProviderSessionID)
	}
}

func quoteJSON(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
