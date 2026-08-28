package cli_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

// completeArgs runs the hidden completion command of twt and returns the
// candidate lines. The directive line and the empty lines are not candidates.
// A completion must never fail, so the command error must stay nil.
func completeArgs(t *testing.T, options cli.Options, args ...string) []string {
	t.Helper()
	stdout, _, err := executeRaw(t, options, append([]string{cobra.ShellCompRequestCmd}, args...)...)
	if err != nil {
		t.Fatalf("twt __complete %s: %v", strings.Join(args, " "), err)
	}
	candidates := []string{}
	for _, line := range strings.Split(stdout, "\n") {
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		candidates = append(candidates, line)
	}
	return candidates
}

// saveCompletionWorkspace saves one active Workspace with one repository directory
// and returns it.
func saveCompletionWorkspace(t *testing.T, stateDir, root, name string) domain.Workspace {
	t.Helper()
	repository := filepath.Join(root, name, "app")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: name, Name: name,
		Status: domain.WorkspaceActive, Root: filepath.Dir(repository), TmuxSession: name,
		Repositories: []domain.WorkspaceRepository{{Name: "app", Path: repository}},
		CreatedAt:    now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	return workspace
}

// writeCodexSession writes one codex provider session that the Workspace
// discovers.
func writeCodexSession(t *testing.T, home string, workspace domain.Workspace, sessionID string) {
	t.Helper()
	writeTestLines(t, filepath.Join(home, ".codex", "sessions", "rollout-"+sessionID+".jsonl"),
		`{"type":"session_meta","payload":{"id":"`+sessionID+`","cwd":`+quoteJSON(t, workspace.Repositories[0].Path)+`}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"A question"}]}}
`)
}

func TestAgentReferenceCompletionOffersRegisteredAndDiscoveredSessions(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("TMUX_PANE", "")
	stateDir := filepath.Join(root, "state")
	workspace := saveCompletionWorkspace(t, stateDir, root, "workspace-one")
	t.Setenv("TWT_WORKSPACE_ID", workspace.ID)
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}
	executeWithOptions(t, options, nil, "agents", "register", "--workspace", workspace.ID,
		"--label", "reviewer", "--", "codex", "resume", "registered-one")
	registered, err := store.NewAgentStore(stateDir).List(workspace.ID)
	if err != nil || len(registered) != 1 {
		t.Fatalf("registered Agent Sessions = %+v, %v", registered, err)
	}
	writeCodexSession(t, home, workspace, "codex-two")

	// focus has no --workspace flag: it completes the Agent Sessions of the
	// current Workspace, with the label of a registered session and the provider
	// of a discovered one.
	candidates := completeArgs(t, options, "agents", "focus", "")
	want := []string{registered[0].ID + "\treviewer", "codex-two\tdiscovered codex"}
	if strings.Join(candidates, "|") != strings.Join(want, "|") {
		t.Fatalf("agents focus completion = %q, want %q", candidates, want)
	}

	// The typed prefix keeps only the candidates that start with it.
	if candidates := completeArgs(t, options, "agents", "focus", "codex-"); len(candidates) != 1 || candidates[0] != "codex-two\tdiscovered codex" {
		t.Fatalf("agents focus completion of a prefix = %q", candidates)
	}

	// Every other command that takes an AGENT reference completes the same
	// candidates.
	for _, path := range [][]string{
		{"agents", "resume"}, {"agents", "open"}, {"agents", "send"}, {"agents", "get"}, {"agents", "rm"},
		{"agents", "transcript", "get"}, {"agents", "transcript", "snapshot"},
		{"agents", "transcript", "link"},
	} {
		// send and transcript link have a required flag, and cobra offers that
		// flag next to the argument candidates.
		candidates := []string{}
		for _, candidate := range completeArgs(t, options, append(path, "")...) {
			if !strings.HasPrefix(candidate, "-") {
				candidates = append(candidates, candidate)
			}
		}
		if strings.Join(candidates, "|") != strings.Join(want, "|") {
			t.Fatalf("twt %s completion = %q, want %q", strings.Join(path, " "), candidates, want)
		}
	}

	// A completion reads only: it never adopts the discovered session.
	after, err := store.NewAgentStore(stateDir).List(workspace.ID)
	if err != nil || len(after) != 1 {
		t.Fatalf("Agent Sessions after the completions = %+v, %v", after, err)
	}
}

func TestAgentReferenceCompletionScopesToTheWorkspace(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("TMUX_PANE", "")
	stateDir := filepath.Join(root, "state")
	first := saveCompletionWorkspace(t, stateDir, root, "workspace-one")
	second := saveCompletionWorkspace(t, stateDir, root, "workspace-two")
	writeCodexSession(t, home, first, "codex-first")
	writeCodexSession(t, home, second, "codex-second")
	t.Setenv("TWT_WORKSPACE_ID", first.ID)
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}

	// The current Workspace applies when --workspace stays at its default. A
	// Workspace with discovered sessions only still completes them.
	if candidates := completeArgs(t, options, "agents", "transcript", "get", ""); len(candidates) != 1 || candidates[0] != "codex-first\tdiscovered codex" {
		t.Fatalf("transcript show completion of the current Workspace = %q", candidates)
	}

	// A set --workspace flag selects that Workspace.
	candidates := completeArgs(t, options, "agents", "transcript", "get", "--workspace", second.ID, "")
	if len(candidates) != 1 || candidates[0] != "codex-second\tdiscovered codex" {
		t.Fatalf("transcript show completion of --workspace %s = %q", second.ID, candidates)
	}

	// An unknown Workspace completes nothing, and reports no error.
	if candidates := completeArgs(t, options, "agents", "get", "--workspace", "absent", ""); len(candidates) != 0 {
		t.Fatalf("agents show completion of an unknown Workspace = %q", candidates)
	}

	// Outside a Workspace, focus completes the registered Agent Sessions of every
	// Workspace. It scans no provider, because one scan for each Workspace is too
	// slow for a key press.
	t.Setenv("TWT_WORKSPACE_ID", "")
	executeWithOptions(t, options, nil, "agents", "register", "--workspace", first.ID,
		"--label", "first", "--", "codex", "resume", "one")
	executeWithOptions(t, options, nil, "agents", "register", "--workspace", second.ID,
		"--label", "second", "--", "codex", "resume", "two")
	labels := []string{}
	for _, candidate := range completeArgs(t, options, "agents", "focus", "") {
		labels = append(labels, strings.SplitN(candidate, "\t", 2)[1])
	}
	sort.Strings(labels)
	if strings.Join(labels, "|") != "first|second" {
		t.Fatalf("agents focus completion outside a Workspace = %q", labels)
	}

	// A --workspace flag that resolves to no Workspace completes nothing, also
	// when Workspaces exist.
	if candidates := completeArgs(t, options, "agents", "get", ""); len(candidates) != 0 {
		t.Fatalf("agents show completion outside a Workspace = %q", candidates)
	}
}
