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

func TestAgentsDiscoverAdoptsProviderSessionsAndShowsLivenessChecks(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	repository := filepath.Join(root, "project", "app")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "project-one", Name: "project-one",
		Status: domain.ProjectActive, Root: filepath.Dir(repository), TmuxSession: "project-one",
		Repositories: []domain.ProjectRepository{{Name: "app", Path: repository}},
		CreatedAt:    now, UpdatedAt: now,
	}
	stateDir := filepath.Join(root, "state")
	if err := store.NewProjectStore(stateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	codexPath := filepath.Join(home, ".codex", "sessions", "2026", "08", "20", "rollout-codex-one.jsonl")
	writeTestLines(t, codexPath, `{"type":"session_meta","payload":{"id":"codex-one","cwd":`+quoteJSON(t, repository)+`}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"Codex question"}]}}
`)
	claudePath := filepath.Join(home, ".claude", "projects", "-Users-alex-code-app", "claude-one.jsonl")
	writeTestLines(t, claudePath, `{"sessionId":"claude-one","cwd":`+quoteJSON(t, repository)+`,"type":"user","message":{"role":"user","content":"Claude question"}}
`)
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}

	text := executeWithOptions(t, options, nil, "agents", "discover", "--project", project.ID)
	for _, want := range []string{"codex\tcodex-one\tapp", "claude\tclaude-one\tapp"} {
		if !strings.Contains(text, want) {
			t.Fatalf("agents discover text = %q, want %q", text, want)
		}
	}
	if strings.Contains(text, home) {
		t.Fatalf("agents discover text exposes a provider path: %q", text)
	}

	listed := executeWithOptions(t, options, nil, "agents", "discover", "--project", project.ID, "--limit", "1", "--output", "json")
	if strings.Contains(listed, home) {
		t.Fatalf("agents discover JSON exposes a provider path: %s", listed)
	}
	var discovered struct {
		SchemaVersion int  `json:"schemaVersion"`
		TotalCount    int  `json:"totalCount"`
		Truncated     bool `json:"truncated"`
		Sessions      []struct {
			Provider     string `json:"provider"`
			SessionID    string `json:"sessionId"`
			Repository   string `json:"repository"`
			LastActivity string `json:"lastActivity"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(listed), &discovered); err != nil {
		t.Fatal(err)
	}
	if discovered.SchemaVersion != 1 || discovered.TotalCount != 2 || !discovered.Truncated || len(discovered.Sessions) != 1 {
		t.Fatalf("agents discover JSON = %+v", discovered)
	}
	if discovered.Sessions[0].Repository != "app" || discovered.Sessions[0].LastActivity == "" {
		t.Fatalf("discovered session = %+v", discovered.Sessions[0])
	}

	dry := executeWithOptions(t, options, nil, "agents", "discover", "--project", project.ID, "--adopt", "--dry-run", "--output", "json")
	if !strings.Contains(dry, `"status":"valid"`) || strings.Contains(dry, `"adopted"`) {
		t.Fatalf("dry-run adopt output = %s", dry)
	}
	stored, err := store.NewAgentStore(stateDir).List(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("dry-run adopt registered Agent Sessions: %+v", stored)
	}

	adopted := executeWithOptions(t, options, nil, "agents", "discover", "--project", project.ID, "--adopt", "--output", "json")
	var adoption struct {
		Adopted []string `json:"adopted"`
		Status  string   `json:"status"`
	}
	if err := json.Unmarshal([]byte(adopted), &adoption); err != nil {
		t.Fatal(err)
	}
	if adoption.Status != "applied" || len(adoption.Adopted) != 2 {
		t.Fatalf("adopt output = %+v", adoption)
	}
	stored, err = store.NewAgentStore(stateDir).List(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("adopted Agent Sessions = %+v", stored)
	}
	sessions := map[string][]string{}
	for _, agent := range stored {
		sessions[agent.Provider] = agent.ResumeCommand
		if agent.ProviderSessionID == "" || agent.Label != agent.Provider {
			t.Fatalf("adopted Agent Session = %+v", agent)
		}
	}
	if strings.Join(sessions["codex"], " ") != "codex resume codex-one" {
		t.Fatalf("adopted Codex resume command = %v", sessions["codex"])
	}
	if strings.Join(sessions["claude"], " ") != "claude --resume claude-one" {
		t.Fatalf("adopted Claude resume command = %v", sessions["claude"])
	}

	empty := executeWithOptions(t, options, nil, "agents", "discover", "--project", project.ID, "--output", "json")
	if !strings.Contains(empty, `"totalCount":0`) {
		t.Fatalf("agents discover after adopt = %s", empty)
	}

	codexAgent := stored[0]
	if codexAgent.Provider != "codex" {
		codexAgent = stored[1]
	}
	shown := executeWithOptions(t, options, nil, "agents", "show", codexAgent.ID, "--project", project.ID)
	for _, want := range []string{"provider\tcodex", "provider session\tcodex-one", "project pane\tfail", "current command\tfail (advisory)", "can read transcript\tyes"} {
		if !strings.Contains(shown, want) {
			t.Fatalf("agents show text = %q, want %q", shown, want)
		}
	}
	showJSON := executeWithOptions(t, options, nil, "agents", "show", codexAgent.ID, "--project", project.ID, "--output", "json")
	if strings.Contains(showJSON, home) {
		t.Fatalf("agents show JSON exposes a provider path: %s", showJSON)
	}
	var show struct {
		Agent struct {
			ID                string `json:"id"`
			ProviderSessionID string `json:"providerSessionId"`
			Status            string `json:"status"`
			CreatedAt         string `json:"createdAt"`
		} `json:"agent"`
		Liveness []struct {
			Name     string `json:"name"`
			OK       bool   `json:"ok"`
			Advisory bool   `json:"advisory"`
		} `json:"liveness"`
	}
	if err := json.Unmarshal([]byte(showJSON), &show); err != nil {
		t.Fatal(err)
	}
	if show.Agent.ID != codexAgent.ID || show.Agent.ProviderSessionID != "codex-one" || show.Agent.Status != "stopped" || show.Agent.CreatedAt == "" {
		t.Fatalf("agents show JSON agent = %+v", show.Agent)
	}
	if len(show.Liveness) != 5 || !show.Liveness[len(show.Liveness)-1].Advisory {
		t.Fatalf("agents show JSON liveness = %+v", show.Liveness)
	}

	unknown := executeWithOptions(t, options, nil, "agents", "list", "--project", project.ID, "--live=false", "--output", "json")
	if !strings.Contains(unknown, `"status":"unknown"`) || !strings.Contains(unknown, `"providerSessionId":"codex-one"`) {
		t.Fatalf("agents list --live=false = %s", unknown)
	}
	if strings.Contains(unknown, home) || strings.Contains(unknown, "tmuxPane") {
		t.Fatalf("agents list JSON exposes an internal path or tmux target: %s", unknown)
	}

	removed := executeWithOptions(t, options, nil, "agents", "rm", codexAgent.ID, "--project", project.ID)
	if !strings.Contains(removed, "Removed Agent Session "+codexAgent.ID) {
		t.Fatalf("agents rm output = %q", removed)
	}
	stored, err = store.NewAgentStore(stateDir).List(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].ID == codexAgent.ID {
		t.Fatalf("Agent Sessions after rm = %+v", stored)
	}
	command := cli.New(cli.Options{StateDir: stateDir, DataDir: options.DataDir})
	command.SetArgs([]string{"agents", "rm", codexAgent.ID, "--project", project.ID})
	if _, err := command.ExecuteC(); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("agents rm of a missing record error = %v", err)
	}
}

func TestAgentsRegisterInfersTheProviderAndSessionFromTheResumeCommand(t *testing.T) {
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
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}
	executeWithOptions(t, options, nil, "agents", "register", "--project", project.ID, "--", "codex", "resume", "session-one")
	executeWithOptions(t, options, nil, "agents", "register", "--project", project.ID, "--", "claude", "--resume=session-two")
	stored, err := store.NewAgentStore(stateDir).List(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	labels := map[string]string{}
	for _, agent := range stored {
		found[agent.Provider] = agent.ProviderSessionID
		labels[agent.Provider] = agent.Label
	}
	if found["codex"] != "session-one" || found["claude"] != "session-two" {
		t.Fatalf("inferred provider sessions = %+v", found)
	}
	if labels["codex"] != "codex" || labels["claude"] != "claude" {
		t.Fatalf("default labels = %+v", labels)
	}

	command := cli.New(options)
	command.SetArgs([]string{"agents", "register", "--project", project.ID})
	if _, err := command.ExecuteC(); err == nil || !strings.Contains(err.Error(), "set --provider PROVIDER") {
		t.Fatalf("register without a provider or resume command error = %v", err)
	}
}

func TestAgentTranscriptShowLinksTheOnlyNewProviderSession(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	repository := filepath.Join(root, "project", "app")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project := domain.Project{
		Version: domain.ProjectVersion, ID: "project-one", Name: "project-one", Status: domain.ProjectActive,
		Root: filepath.Dir(repository), TmuxSession: "project-one",
		Repositories: []domain.ProjectRepository{{Name: "app", Path: repository}},
		CreatedAt:    now, UpdatedAt: now,
	}
	stateDir := filepath.Join(root, "state")
	if err := store.NewProjectStore(stateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}
	registration := executeWithOptions(t, options, nil, "agents", "register", "--project", project.ID, "--", "codex", "--continue")
	fields := strings.Fields(registration)
	if len(fields) < 4 {
		t.Fatalf("registration output = %q", registration)
	}
	agentID := fields[3]
	writeTestLines(t, filepath.Join(home, ".codex", "sessions", "rollout-late-session.jsonl"), `{"type":"session_meta","payload":{"id":"late-session","cwd":`+quoteJSON(t, repository)+`}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"Late question"}]}}
`)
	shown := executeWithOptions(t, options, nil, "agents", "transcript", "show", agentID, "--project", project.ID, "--output", "json")
	if !strings.Contains(shown, "Late question") {
		t.Fatalf("transcript show after the lazy link = %s", shown)
	}
	linked, err := store.NewAgentStore(stateDir).Find(agentID)
	if err != nil {
		t.Fatal(err)
	}
	if linked.ProviderSessionID != "late-session" {
		t.Fatalf("lazy provider session link = %q", linked.ProviderSessionID)
	}
}

func writeTestLines(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
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
