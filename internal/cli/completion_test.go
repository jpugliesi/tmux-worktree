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

// saveCompletionProject saves one active Project with one repository directory
// and returns it.
func saveCompletionProject(t *testing.T, stateDir, root, name string) domain.Project {
	t.Helper()
	repository := filepath.Join(root, name, "app")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	project := domain.Project{
		Version: domain.ProjectVersion, ID: name, Name: name,
		Status: domain.ProjectActive, Root: filepath.Dir(repository), TmuxSession: name,
		Repositories: []domain.ProjectRepository{{Name: "app", Path: repository}},
		CreatedAt:    now, UpdatedAt: now,
	}
	if err := store.NewProjectStore(stateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	return project
}

// writeCodexSession writes one codex provider session that the Project
// discovers.
func writeCodexSession(t *testing.T, home string, project domain.Project, sessionID string) {
	t.Helper()
	writeTestLines(t, filepath.Join(home, ".codex", "sessions", "rollout-"+sessionID+".jsonl"),
		`{"type":"session_meta","payload":{"id":"`+sessionID+`","cwd":`+quoteJSON(t, project.Repositories[0].Path)+`}}
{"type":"response_item","payload":{"role":"user","content":[{"type":"input_text","text":"A question"}]}}
`)
}

func TestAgentReferenceCompletionOffersRegisteredAndDiscoveredSessions(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("TMUX_PANE", "")
	stateDir := filepath.Join(root, "state")
	project := saveCompletionProject(t, stateDir, root, "project-one")
	t.Setenv("TWT_PROJECT_ID", project.ID)
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}
	executeWithOptions(t, options, nil, "agents", "register", "--project", project.ID,
		"--label", "reviewer", "--", "codex", "resume", "registered-one")
	registered, err := store.NewAgentStore(stateDir).List(project.ID)
	if err != nil || len(registered) != 1 {
		t.Fatalf("registered Agent Sessions = %+v, %v", registered, err)
	}
	writeCodexSession(t, home, project, "codex-two")

	// focus has no --project flag: it completes the Agent Sessions of the
	// current Project, with the label of a registered session and the provider
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
		{"agents", "resume"}, {"agents", "send"}, {"agents", "show"}, {"agents", "rm"},
		{"agents", "transcript", "show"}, {"agents", "transcript", "snapshot"},
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
	after, err := store.NewAgentStore(stateDir).List(project.ID)
	if err != nil || len(after) != 1 {
		t.Fatalf("Agent Sessions after the completions = %+v, %v", after, err)
	}
}

func TestAgentReferenceCompletionScopesToTheProject(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("TMUX_PANE", "")
	stateDir := filepath.Join(root, "state")
	first := saveCompletionProject(t, stateDir, root, "project-one")
	second := saveCompletionProject(t, stateDir, root, "project-two")
	writeCodexSession(t, home, first, "codex-first")
	writeCodexSession(t, home, second, "codex-second")
	t.Setenv("TWT_PROJECT_ID", first.ID)
	options := cli.Options{StateDir: stateDir, DataDir: filepath.Join(root, "data")}

	// The current Project applies when --project stays at its default. A
	// Project with discovered sessions only still completes them.
	if candidates := completeArgs(t, options, "agents", "transcript", "show", ""); len(candidates) != 1 || candidates[0] != "codex-first\tdiscovered codex" {
		t.Fatalf("transcript show completion of the current Project = %q", candidates)
	}

	// A set --project flag selects that Project.
	candidates := completeArgs(t, options, "agents", "transcript", "show", "--project", second.ID, "")
	if len(candidates) != 1 || candidates[0] != "codex-second\tdiscovered codex" {
		t.Fatalf("transcript show completion of --project %s = %q", second.ID, candidates)
	}

	// An unknown Project completes nothing, and reports no error.
	if candidates := completeArgs(t, options, "agents", "show", "--project", "absent", ""); len(candidates) != 0 {
		t.Fatalf("agents show completion of an unknown Project = %q", candidates)
	}

	// Outside a Project, focus completes the registered Agent Sessions of every
	// Project. It scans no provider, because one scan for each Project is too
	// slow for a key press.
	t.Setenv("TWT_PROJECT_ID", "")
	executeWithOptions(t, options, nil, "agents", "register", "--project", first.ID,
		"--label", "first", "--", "codex", "resume", "one")
	executeWithOptions(t, options, nil, "agents", "register", "--project", second.ID,
		"--label", "second", "--", "codex", "resume", "two")
	labels := []string{}
	for _, candidate := range completeArgs(t, options, "agents", "focus", "") {
		labels = append(labels, strings.SplitN(candidate, "\t", 2)[1])
	}
	sort.Strings(labels)
	if strings.Join(labels, "|") != "first|second" {
		t.Fatalf("agents focus completion outside a Project = %q", labels)
	}

	// A --project flag that resolves to no Project completes nothing, also
	// when Projects exist.
	if candidates := completeArgs(t, options, "agents", "show", ""); len(candidates) != 0 {
		t.Fatalf("agents show completion outside a Project = %q", candidates)
	}
}
