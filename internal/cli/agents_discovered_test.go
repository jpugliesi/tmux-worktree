package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

// discoveredListEntry is the JSON shape of one agents list entry that this
// test reads.
type discoveredListEntry struct {
	ID                string `json:"id"`
	ProviderSessionID string `json:"providerSessionId"`
	Provider          string `json:"provider"`
	Label             string `json:"label"`
	Status            string `json:"status"`
	CreatedAt         string `json:"createdAt"`
	LastActivity      string `json:"lastActivity"`
	Capabilities      struct {
		CanResume         bool `json:"canResume"`
		CanSend           bool `json:"canSend"`
		CanFocus          bool `json:"canFocus"`
		CanReadTranscript bool `json:"canReadTranscript"`
	} `json:"capabilities"`
}

func decodeAgentsList(t *testing.T, output string) []discoveredListEntry {
	t.Helper()
	var listed struct {
		Agents []discoveredListEntry `json:"agents"`
	}
	if err := json.Unmarshal([]byte(output), &listed); err != nil {
		t.Fatalf("decode agents list: %v\n%s", err, output)
	}
	return listed.Agents
}

// directorySnapshot reads every file under the directory, so a test can
// assert that a read command wrote nothing.
func directorySnapshot(t *testing.T, directory string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot[path] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestAgentsListShowsDiscoveredSessionsAndAdoptsOnFirstTouch(t *testing.T) {
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
	t.Setenv("TWT_PROJECT_ID", project.ID)
	t.Setenv("TMUX_PANE", "")

	sessions := []struct {
		path string
		body string
		age  time.Duration
	}{
		{filepath.Join(home, ".codex", "sessions", "rollout-codex-one.jsonl"),
			`{"type":"session_meta","payload":{"id":"codex-one","cwd":` + quoteJSON(t, repository) + `}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"Codex question"}]}}
`, 0},
		{filepath.Join(home, ".claude", "projects", "-user-code-app", "claude-one.jsonl"),
			`{"sessionId":"claude-one","cwd":` + quoteJSON(t, repository) + `,"type":"user","message":{"role":"user","content":"Claude question"}}
`, time.Hour},
		{filepath.Join(home, ".codex", "sessions", "rollout-codex-two.jsonl"),
			`{"type":"session_meta","payload":{"id":"codex-two","cwd":` + quoteJSON(t, repository) + `}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"Second question"}]}}
`, 2 * time.Hour},
	}
	for _, session := range sessions {
		writeTestLines(t, session.path, session.body)
		modTime := now.Add(-session.age)
		if err := os.Chtimes(session.path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}

	// A bare list shows the discovered sessions, newest first, and writes
	// nothing.
	before := directorySnapshot(t, stateDir)
	listed := executeWithOptions(t, options, nil, "agents", "list", "--project", project.ID, "--output", "json")
	if strings.Contains(listed, home) {
		t.Fatalf("agents list exposes a provider path: %s", listed)
	}
	entries := decodeAgentsList(t, listed)
	if len(entries) != 3 {
		t.Fatalf("agents list entries = %+v", entries)
	}
	order := []string{"codex-one", "claude-one", "codex-two"}
	providers := []string{"codex", "claude", "codex"}
	for index, entry := range entries {
		if entry.ID != order[index] || entry.ProviderSessionID != order[index] {
			t.Fatalf("discovered entry %d = %+v, want ID %q", index, entry, order[index])
		}
		if entry.Status != "discovered" || entry.Provider != providers[index] || entry.Label != providers[index] {
			t.Fatalf("discovered entry = %+v", entry)
		}
		if entry.LastActivity == "" {
			t.Fatalf("discovered entry has no lastActivity: %+v", entry)
		}
		capabilities := entry.Capabilities
		if !capabilities.CanResume || capabilities.CanSend || capabilities.CanFocus || !capabilities.CanReadTranscript {
			t.Fatalf("discovered capabilities = %+v", capabilities)
		}
	}
	if after := directorySnapshot(t, stateDir); !reflect.DeepEqual(before, after) {
		t.Fatalf("agents list changed the state directory: %+v != %+v", before, after)
	}

	// --registered and --live=false skip the provider scan.
	registered := executeWithOptions(t, options, nil, "agents", "list", "--project", project.ID, "--registered", "--output", "json")
	if entries := decodeAgentsList(t, registered); len(entries) != 0 {
		t.Fatalf("agents list --registered = %+v", entries)
	}
	cheap := executeWithOptions(t, options, nil, "agents", "list", "--project", project.ID, "--live=false", "--output", "json")
	if entries := decodeAgentsList(t, cheap); len(entries) != 0 {
		t.Fatalf("agents list --live=false = %+v", entries)
	}

	// rm does not adopt a discovered session.
	_, _, err := executeRaw(t, options, "agents", "rm", "codex-one", "--project", project.ID)
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(err.Error(), "is not registered") {
		t.Fatalf("agents rm of a discovered session = %v", err)
	}

	// A dry run validates the adoption and writes nothing.
	before = directorySnapshot(t, stateDir)
	dryResume := executeWithOptions(t, options, nil, "agents", "resume", "codex-one", "--dry-run", "--output", "json")
	if !strings.Contains(dryResume, `"status":"valid"`) {
		t.Fatalf("dry-run resume of a discovered session = %s", dryResume)
	}
	drySnapshot := executeWithOptions(t, options, nil, "agents", "transcript", "snapshot", "codex-one", "--project", project.ID, "--dry-run", "--output", "json")
	if !strings.Contains(drySnapshot, `"status":"valid"`) {
		t.Fatalf("dry-run snapshot of a discovered session = %s", drySnapshot)
	}
	if after := directorySnapshot(t, stateDir); !reflect.DeepEqual(before, after) {
		t.Fatalf("a dry run changed the state directory: %+v != %+v", before, after)
	}

	// An ambiguous session ID prefix reports the candidates.
	_, _, err = executeRaw(t, options, "agents", "transcript", "show", "codex-", "--project", project.ID)
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous discovered prefix error = %v", err)
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "codex-one") || !strings.Contains(hint, "codex-two") {
		t.Fatalf("ambiguous discovered prefix hint = %q", hint)
	}

	// A unique session ID prefix adopts the session and reads the transcript.
	shown := executeWithOptions(t, options, nil, "agents", "transcript", "show", "codex-o", "--project", project.ID, "--output", "json")
	if !strings.Contains(shown, "Codex question") {
		t.Fatalf("transcript show of a discovered session = %s", shown)
	}
	adopted, err := store.NewAgentStore(stateDir).List(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(adopted) != 1 || adopted[0].Provider != "codex" || adopted[0].ProviderSessionID != "codex-one" || adopted[0].Label != "codex" {
		t.Fatalf("adopted Agent Session = %+v", adopted)
	}
	if strings.Join(adopted[0].ResumeCommand, " ") != "codex resume codex-one" {
		t.Fatalf("adopted resume command = %v", adopted[0].ResumeCommand)
	}

	// The second list shows the adopted session as registered, not as
	// discovered, before the remaining discovered sessions.
	entries = decodeAgentsList(t, executeWithOptions(t, options, nil, "agents", "list", "--project", project.ID, "--output", "json"))
	if len(entries) != 3 {
		t.Fatalf("agents list after adopt = %+v", entries)
	}
	if entries[0].ID != adopted[0].ID || entries[0].Status != "stopped" || entries[0].ProviderSessionID != "codex-one" {
		t.Fatalf("adopted list entry = %+v", entries[0])
	}
	if entries[1].ID != "claude-one" || entries[1].Status != "discovered" || entries[2].ID != "codex-two" {
		t.Fatalf("discovered entries after adopt = %+v", entries[1:])
	}

	// send adopts on first touch, then reports that the session is not live.
	command := cli.New(cli.Options{StateDir: stateDir, DataDir: options.DataDir})
	command.SetIn(strings.NewReader("Please look at the review note."))
	command.SetArgs(forceTextOutput([]string{"agents", "send", "claude-one", "--project", project.ID, "--stdin"}))
	_, err = command.ExecuteC()
	if err == nil || clierr.CodeOf(err) != clierr.PreconditionFailed || !strings.Contains(err.Error(), "not live") {
		t.Fatalf("agents send to a discovered session = %v", err)
	}
	if !strings.Contains(clierr.HintOf(err), "agents resume") {
		t.Fatalf("agents send hint = %q", clierr.HintOf(err))
	}
	adopted, err = store.NewAgentStore(stateDir).List(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	linked := map[string]string{}
	for _, agent := range adopted {
		linked[agent.Provider] = agent.ProviderSessionID
	}
	if len(adopted) != 2 || linked["claude"] != "claude-one" {
		t.Fatalf("Agent Sessions after send adopt = %+v", adopted)
	}
}

func TestAgentsResumeAdoptsADiscoveredProviderSession(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go is not installed")
	}
	goCache := strings.TrimSpace(runCommand(t, "", goBinary, "env", "GOCACHE"))

	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("GOCACHE", goCache)
	t.Setenv("TMUX_PANE", "")

	// The resume command starts a process named codex. The test builds one
	// that only sleeps, because the pane process name must match the
	// provider.
	fakeSource := filepath.Join(root, "fakeagent")
	writeTestLines(t, filepath.Join(fakeSource, "go.mod"), "module fakeagent\n\ngo 1.21\n")
	writeTestLines(t, filepath.Join(fakeSource, "main.go"), "package main\n\nimport \"time\"\n\nfunc main() { time.Sleep(time.Minute) }\n")
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	runCommand(t, fakeSource, goBinary, "build", "-o", filepath.Join(fakeBin, "codex"), ".")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: policy\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "policy.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	executeWithOptions(t, options, nil, "projects", "create", "adopt", "--template", "policy", "--no-open")
	project, err := store.NewProjectStore(options.StateDir).Find("adopt")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TWT_PROJECT_ID", project.ID)

	writeTestLines(t, filepath.Join(home, ".codex", "sessions", "rollout-codex-adopt.jsonl"),
		`{"type":"session_meta","payload":{"id":"codex-adopt","cwd":`+quoteJSON(t, project.Repositories[0].Path)+`}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"Adopt question"}]}}
`)

	resumed := executeWithOptions(t, options, nil, "agents", "resume", "codex-adopt", "--output", "json")
	if !strings.Contains(resumed, `"status":"live"`) || !strings.Contains(resumed, `"providerSessionId":"codex-adopt"`) {
		t.Fatalf("resume of a discovered session = %s", resumed)
	}
	adopted, err := store.NewAgentStore(options.StateDir).List(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(adopted) != 1 || adopted[0].Provider != "codex" || adopted[0].ProviderSessionID != "codex-adopt" || adopted[0].TmuxPane == "" {
		t.Fatalf("adopted Agent Session = %+v", adopted)
	}
	windows := runCommand(t, "", "tmux", "-L", socket, "list-windows", "-t", project.TmuxSession, "-F", "#{window_name}")
	if !strings.Contains(windows, "codex") {
		t.Fatalf("resume did not start an Agent Session window: %q", windows)
	}

	// The second list shows the session as a registered live Agent Session,
	// not as a discovered one.
	entries := decodeAgentsList(t, executeWithOptions(t, options, nil, "agents", "list", "--project", project.ID, "--output", "json"))
	if len(entries) != 1 || entries[0].ID != adopted[0].ID || entries[0].Status != "live" {
		t.Fatalf("agents list after resume adopt = %+v", entries)
	}
}

func TestAgentsListSortsByRecencyAndWritesAge(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("TMUX_PANE", "")
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
	t.Setenv("TWT_PROJECT_ID", project.ID)
	old := now.Add(-2 * time.Hour)
	if err := store.NewAgentStore(stateDir).Save(domain.AgentSession{
		Version: domain.AgentVersion, ID: "agent-old", ProjectID: project.ID,
		Provider: "codex", Label: "review", ProviderSessionID: "session-old",
		ResumeCommand: []string{"codex", "resume", "session-old"},
		CreatedAt:     old, UpdatedAt: old,
	}); err != nil {
		t.Fatal(err)
	}
	writeTestLines(t, filepath.Join(home, ".claude", "projects", "-user-code-app", "claude-new.jsonl"),
		`{"sessionId":"claude-new","cwd":`+quoteJSON(t, repository)+`,"type":"user","message":{"role":"user","content":"New question"}}
`)
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}

	listed := executeWithOptions(t, options, nil, "agents", "list", "--project", project.ID, "--output", "json")
	entries := decodeAgentsList(t, listed)
	if len(entries) != 2 || entries[0].ID != "claude-new" || entries[0].Status != "discovered" {
		t.Fatalf("newest session was not first: %+v", entries)
	}
	if entries[1].ID != "agent-old" || entries[1].Status != "stopped" || entries[1].CreatedAt == "" {
		t.Fatalf("older registered session = %+v", entries[1])
	}

	text := executeWithOptions(t, options, nil, "agents", "list", "--project", project.ID)
	if strings.Contains(text, "\t") {
		t.Fatalf("agents list text still contains tabs: %q", text)
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "PROVIDER") || !strings.Contains(lines[0], "ID") {
		t.Fatalf("agents list header = %q", text)
	}
	if !strings.HasPrefix(lines[1], "claude") || !strings.Contains(lines[1], "claude-new") || !strings.Contains(lines[1], "0m") {
		t.Fatalf("newest text line = %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "codex") || !strings.Contains(lines[2], "agent-old") || !strings.Contains(lines[2], "2h") {
		t.Fatalf("older text line = %q", lines[2])
	}

	var picked []string
	options.AgentPick = func(_ *cobra.Command, lines []string) (int, error) {
		picked = append([]string(nil), lines...)
		return 0, nil
	}
	executeWithOptions(t, options, nil, "agents", "open", "--dry-run")
	if len(picked) != 2 || picked[0] != "claude\tclaude-new\t0m" || picked[1] != "codex\tagent-old\t2h" {
		t.Fatalf("open picker lines = %v", picked)
	}
}
