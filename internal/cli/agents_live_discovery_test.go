package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func TestAgentsListDiscoversAndPreviewsLiveProviderPanesWithoutWritingState(t *testing.T) {
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
	repository := filepath.Join(root, "workspace", "app")
	for _, directory := range []string{home, repository} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("GOCACHE", goCache)
	t.Setenv("TMUX_PANE", "")

	helperSource := filepath.Join(root, "fake-agent")
	writeTestLines(t, filepath.Join(helperSource, "go.mod"), "module fakeagent\n\ngo 1.21\n")
	writeTestLines(t, filepath.Join(helperSource, "main.go"), `package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "host" {
		command := exec.Command(os.Args[2], os.Args[3:]...)
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := command.Run(); err != nil {
			fmt.Println("host error:", err)
			time.Sleep(time.Minute)
		}
		return
	}
	fmt.Println("preview from " + filepath.Base(os.Args[0]))
	time.Sleep(time.Minute)
}
`)
	helper := filepath.Join(root, "fake-agent-host")
	runCommand(t, helperSource, goBinary, "build", "-o", helper, ".")

	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"codex", "claude", "grok"} {
		copyExecutable(t, helper, filepath.Join(bin, provider))
	}
	cursorVersion := filepath.Join(root, "cursor-agent", "versions", "test-version")
	if err := os.MkdirAll(cursorVersion, 0o755); err != nil {
		t.Fatal(err)
	}
	cursorExecutable := filepath.Join(cursorVersion, "cursor-agent")
	copyExecutable(t, helper, cursorExecutable)

	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-live-agents", Name: "live-agents",
		Status: domain.WorkspaceActive, Root: filepath.Dir(repository), TmuxSession: "live-agents",
		Repositories: []domain.WorkspaceRepository{{Name: "app", Path: repository, WindowName: "app"}},
		CreatedAt:    now, UpdatedAt: now,
	}
	stateDir := filepath.Join(root, "state")
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TWT_WORKSPACE_ID", workspace.ID)

	socket := fmt.Sprintf("twt-live-agents-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	startPane := func(first bool, window string, command ...string) {
		args := []string{"-L", socket, "-f", "/dev/null"}
		if first {
			args = append(args, "new-session", "-d", "-s", workspace.TmuxSession, "-n", window, "-c", repository, "--")
		} else {
			args = append(args, "new-window", "-d", "-t", "="+workspace.TmuxSession, "-n", window, "-c", repository, "--")
		}
		args = append(args, command...)
		runCommand(t, "", "tmux", args...)
	}
	startPane(true, "codex", filepath.Join(bin, "codex"))
	startPane(false, "claude", filepath.Join(bin, "claude"))
	startPane(false, "grok", filepath.Join(bin, "grok"))
	startPane(false, "cursor", cursorExecutable)
	runCommand(t, "", "tmux", "-L", socket, "set-option", "-t", workspace.TmuxSession, "@twt_workspace_id", workspace.ID)
	deadline := time.Now().Add(3 * time.Second)
	for {
		capture := exec.Command("tmux", "-L", socket, "capture-pane", "-p", "-t", workspace.TmuxSession+":cursor")
		output, _ := capture.Output()
		if strings.Contains(string(output), "preview from cursor-agent") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Cursor Agent fixture did not start:\n%s", output)
		}
		time.Sleep(20 * time.Millisecond)
	}

	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
	before := directorySnapshot(t, stateDir)
	listed := executeWithOptions(t, options, nil, "agents", "list", "--workspace", workspace.ID, "--output", "json")
	entries := decodeAgentsList(t, listed)
	if len(entries) != 4 {
		processOutput, _ := exec.Command("ps", "-A", "-ww", "-o", "pid=,ppid=,pgid=,tpgid=,tty=,lstart=,comm=,args=").Output()
		fixtureProcesses := []string{}
		for _, row := range strings.Split(string(processOutput), "\n") {
			if strings.Contains(row, root) || strings.Contains(row, "cursor-agent/versions/test-version") {
				fixtureProcesses = append(fixtureProcesses, row)
			}
		}
		cursorPane := runCommand(t, "", "tmux", "-L", socket, "capture-pane", "-p", "-t", "="+workspace.TmuxSession+":cursor")
		t.Fatalf("agents list entries = %+v, want one live pane for each provider\nfixture processes:\n%s\ncursor pane:\n%s", entries, strings.Join(fixtureProcesses, "\n"), cursorPane)
	}
	providers := make([]string, 0, len(entries))
	cursorReference := ""
	for _, entry := range entries {
		providers = append(providers, entry.Provider)
		if entry.Provider == "cursor" {
			cursorReference = entry.ID
		}
		if !entry.Capabilities.CanPreview || !entry.Capabilities.CanSend || !entry.Capabilities.CanFocus {
			t.Fatalf("live %s capabilities = %+v", entry.Provider, entry.Capabilities)
		}
		var preview struct {
			WorkspaceID string `json:"workspaceId"`
			AgentID     string `json:"agentId"`
			Source      string `json:"source"`
			Untrusted   bool   `json:"untrusted"`
			Markdown    string `json:"markdown"`
		}
		output := executeWithOptions(t, options, nil, "agents", "open", "--preview", entry.ID, "--workspace", workspace.ID, "--output", "json")
		if err := json.Unmarshal([]byte(output), &preview); err != nil {
			t.Fatalf("decode %s preview: %v\n%s", entry.Provider, err, output)
		}
		if preview.WorkspaceID != workspace.ID || preview.AgentID != entry.ID || preview.Source != "livePane" || !preview.Untrusted {
			t.Fatalf("%s preview metadata = %+v", entry.Provider, preview)
		}
		if !strings.Contains(preview.Markdown, "Live pane preview") || !strings.Contains(preview.Markdown, "preview from") {
			t.Fatalf("%s preview = %q", entry.Provider, preview.Markdown)
		}
	}
	sort.Strings(providers)
	if got, want := strings.Join(providers, ","), "claude,codex,cursor,grok"; got != want {
		t.Fatalf("providers = %q, want %q", got, want)
	}
	if after := directorySnapshot(t, stateDir); !reflect.DeepEqual(before, after) {
		t.Fatalf("list or preview changed twt state: %+v != %+v", before, after)
	}
	markers := runCommand(t, "", "tmux", "-L", socket, "list-panes", "-s", "-t", "="+workspace.TmuxSession, "-F", "#{@twt_agent_id}")
	if strings.TrimSpace(markers) != "" {
		t.Fatalf("list or preview claimed an Agent Session pane: %q", markers)
	}
	_, _, err = executeRaw(t, options, "agents", "send", cursorReference, "--workspace", workspace.ID, "--stdin")
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("empty live-pane send error = %v", err)
	}
	if after := directorySnapshot(t, stateDir); !reflect.DeepEqual(before, after) {
		t.Fatalf("invalid send adopted an Agent Session: %+v != %+v", before, after)
	}
	executeWithOptions(t, options, nil, "agents", "adopt", cursorReference, "--workspace", workspace.ID, "--dry-run", "--output", "json")
	if after := directorySnapshot(t, stateDir); !reflect.DeepEqual(before, after) {
		t.Fatalf("dry-run adoption changed twt state: %+v != %+v", before, after)
	}
	markers = runCommand(t, "", "tmux", "-L", socket, "list-panes", "-s", "-t", "="+workspace.TmuxSession, "-F", "#{@twt_agent_id}")
	if strings.TrimSpace(markers) != "" {
		t.Fatalf("dry-run adoption claimed an Agent Session pane: %q", markers)
	}

	var sent struct {
		AgentID string `json:"agentId"`
		Status  string `json:"status"`
	}
	sendOutput := executeWithOptions(t, options, strings.NewReader("review note"), "agents", "send", cursorReference, "--workspace", workspace.ID, "--stdin", "--output", "json")
	if err := json.Unmarshal([]byte(sendOutput), &sent); err != nil {
		t.Fatalf("decode send result: %v\n%s", err, sendOutput)
	}
	if sent.AgentID == "" || sent.AgentID == cursorReference || sent.Status != "sent" {
		t.Fatalf("send result = %+v", sent)
	}
	saved, err := store.NewAgentStore(stateDir).List(workspace.ID)
	if err != nil || len(saved) != 1 {
		t.Fatalf("saved Agent Sessions = %+v, %v", saved, err)
	}
	if saved[0].ID != sent.AgentID || saved[0].Provider != "cursor" || saved[0].RuntimeReference != cursorReference ||
		saved[0].PaneRootProcessID <= 0 || saved[0].PaneRootStarted == "" || saved[0].ProcessID <= 0 || saved[0].ProcessEvidence == "" {
		t.Fatalf("adopted Cursor Agent Session = %+v", saved[0])
	}
	markers = runCommand(t, "", "tmux", "-L", socket, "list-panes", "-s", "-t", "="+workspace.TmuxSession, "-F", "#{@twt_agent_id}")
	if strings.TrimSpace(markers) != sent.AgentID {
		t.Fatalf("adopted pane marker = %q, want %q", markers, sent.AgentID)
	}
	resumeOutput := executeWithOptions(t, options, nil, "agents", "resume", sent.AgentID, "--output", "json")
	if !strings.Contains(resumeOutput, `"id":"`+sent.AgentID+`"`) || !strings.Contains(resumeOutput, `"status":"live"`) {
		t.Fatalf("resume adopted Cursor Agent Session = %s", resumeOutput)
	}

	// The temporary reference remains resolvable after another CLI process
	// adopts it, and a retry does not create a second record.
	executeWithOptions(t, options, nil, "agents", "adopt", cursorReference, "--workspace", workspace.ID, "--output", "json")
	saved, err = store.NewAgentStore(stateDir).List(workspace.ID)
	if err != nil || len(saved) != 1 || saved[0].ID != sent.AgentID {
		t.Fatalf("adoption retry saved Agent Sessions = %+v, %v", saved, err)
	}
}

func copyExecutable(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o755); err != nil {
		t.Fatal(err)
	}
}
