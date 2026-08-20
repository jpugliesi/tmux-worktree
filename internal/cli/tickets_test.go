package cli_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

// ticketTestOptions builds Options with a temporary Tickets home. Tests never
// touch a personal vault.
func ticketTestOptions(t *testing.T) (cli.Options, string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "tickets")
	return cli.Options{
		ConfigDir:   filepath.Join(root, "config"),
		StateDir:    filepath.Join(root, "state"),
		DataDir:     filepath.Join(root, "data"),
		TicketsHome: home,
	}, home
}

// ticketMutation is the mutation envelope of one tickets command.
type ticketMutation struct {
	SchemaVersion int    `json:"schemaVersion"`
	Operation     string `json:"operation"`
	Status        string `json:"status"`
	ID            string `json:"id"`
	Name          string `json:"name"`
}

func decodeTicketMutation(t *testing.T, output string) ticketMutation {
	t.Helper()
	var result ticketMutation
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode mutation envelope: %v\n%s", err, output)
	}
	return result
}

func readTicketFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ticket file: %v", err)
	}
	return string(data)
}

// homeFiles lists every file under the Tickets home.
func homeFiles(t *testing.T, home string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk Tickets home: %v", err)
	}
	return files
}

func TestTicketsInitScaffoldsOnceAndKeepsNotes(t *testing.T) {
	options, home := ticketTestOptions(t)
	stdout, _, err := executeCollectingInput(t, options, nil, "tickets", "init")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(stdout, "Wrote") != 2 {
		t.Fatalf("first init output = %q", stdout)
	}
	indexPath := filepath.Join(home, "index.md")
	if err := os.WriteFile(indexPath, []byte("# my custom hub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, err = executeCollectingInput(t, options, nil, "tickets", "init")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(stdout, "Kept") != 2 {
		t.Fatalf("second init output = %q", stdout)
	}
	if readTicketFile(t, indexPath) != "# my custom hub\n" {
		t.Fatal("init overwrote an existing note")
	}
	jsonOut, _, err := executeCollectingInput(t, options, nil, "tickets", "init", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	result := decodeTicketMutation(t, jsonOut)
	if result.SchemaVersion != 1 || result.Operation != "tickets.init" || result.Status != "applied" {
		t.Fatalf("init envelope = %+v", result)
	}
}

func TestTicketsCreateWritesAnObsidianNote(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "boards", "create", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "fix the vfs tools", "--board", "change-monitor", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	result := decodeTicketMutation(t, stdout)
	if result.SchemaVersion != 1 || result.Operation != "tickets.create" || result.Status != "applied" || result.ID != "fix-the-vfs-tools" {
		t.Fatalf("create envelope = %+v", result)
	}
	content := readTicketFile(t, filepath.Join(home, "change-monitor", "fix-the-vfs-tools.md"))
	for _, want := range []string{
		"---\n",
		"title: \"fix the vfs tools\"",
		"status: needs-triage",
		"priority: 2",
		"board: change-monitor",
		"blocked_by: []",
		"# fix the vfs tools",
		"## Comments",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("ticket file misses %q:\n%s", want, content)
		}
	}

	_, _, err = executeCollectingInput(t, options, nil, "tickets", "create", "fix the vfs tools")
	if err == nil || clierr.CodeOf(err) != clierr.AlreadyExists || !strings.Contains(clierr.HintOf(err), "--slug") {
		t.Fatalf("duplicate slug = %v (code %q, hint %q)", err, clierr.CodeOf(err), clierr.HintOf(err))
	}

	_, _, err = executeCollectingInput(t, options, nil, "tickets", "create", "another one", "--board", "missing")
	if err == nil || clierr.CodeOf(err) != clierr.NotFound || !strings.Contains(clierr.HintOf(err), "boards create") {
		t.Fatalf("missing Board = %v (code %q, hint %q)", err, clierr.CodeOf(err), clierr.HintOf(err))
	}
}

func TestTicketsCreateWithoutInputNeedsATerminal(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	// A nil stdin keeps os.Stdin, which is not a terminal under go test.
	_, _, err := executeCollectingInput(t, options, nil, "tickets", "create")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || clierr.ExitCode(err) != 2 {
		t.Fatalf("bare create without a terminal = %v (code %q, exit %d)", err, clierr.CodeOf(err), clierr.ExitCode(err))
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "--stdin") {
		t.Fatalf("bare create hint = %q", hint)
	}
}

func TestTicketsCreateFromStdin(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, strings.NewReader("body"), "tickets", "create", "--stdin")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(clierr.HintOf(err), "--title") {
		t.Fatalf("--stdin without --title = %v (hint %q)", err, clierr.HintOf(err))
	}
	body := "Steps:\n\n1. Reconnect the tools\n"
	if _, _, err := executeCollectingInput(t, options, strings.NewReader(body),
		"tickets", "create", "--title", "Fix auth", "--stdin"); err != nil {
		t.Fatal(err)
	}
	content := readTicketFile(t, filepath.Join(home, "fix-auth.md"))
	if !strings.Contains(content, "# Fix auth") || !strings.Contains(content, "1. Reconnect the tools") {
		t.Fatalf("stdin ticket file:\n%s", content)
	}
	if strings.Count(content, "# Fix auth") != 1 {
		t.Fatalf("the title line is duplicated:\n%s", content)
	}
}

func TestTicketsCreateDryRunWritesNothing(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	before := homeFiles(t, home)
	jsonOut, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "preview only", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	result := decodeTicketMutation(t, jsonOut)
	if result.Operation != "tickets.create" || result.Status != "valid" || result.ID != "preview-only" {
		t.Fatalf("dry-run envelope = %+v", result)
	}
	textOut, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "preview only", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textOut, "title: \"preview only\"") || !strings.HasPrefix(textOut, "---\n") {
		t.Fatalf("text dry-run does not print the file:\n%s", textOut)
	}
	after := homeFiles(t, home)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("dry-run changed the Tickets home:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestTicketsCreateInEditor(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	options.OpenEditor = func(path string) error {
		content := "---\n" +
			"title: \"Editor ticket\"\n" +
			"status: ready-for-agent\n" +
			"priority: 1\n" +
			"board:\n" +
			"---\n\n" +
			"# Editor ticket\n\n" +
			"## What to build\n\n" +
			"Details from the editor.\n"
		return os.WriteFile(path, []byte(content), 0o644)
	}
	// A replaced stdin and a buffered stdout count as an interactive session.
	if _, _, err := executeCollectingInput(t, options, strings.NewReader(""), "tickets", "create"); err != nil {
		t.Fatal(err)
	}
	content := readTicketFile(t, filepath.Join(home, "editor-ticket.md"))
	for _, want := range []string{"status: ready-for-agent", "priority: 1", "Details from the editor."} {
		if !strings.Contains(content, want) {
			t.Fatalf("editor ticket misses %q:\n%s", want, content)
		}
	}
	if strings.Count(content, "# Editor ticket") != 1 {
		t.Fatalf("editor ticket duplicated the title line:\n%s", content)
	}

	// An unchanged save is invalid usage.
	options.OpenEditor = func(string) error { return nil }
	_, _, err := executeCollectingInput(t, options, strings.NewReader(""), "tickets", "create")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("unchanged editor save = %v (code %q)", err, clierr.CodeOf(err))
	}
}

func TestTicketsClaimIsACompareAndSet(t *testing.T) {
	t.Setenv("TWT_CLAIMANT", "")
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "shared work"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "shared-work.md")

	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "claim", "shared-work", "--as", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTicketFile(t, path), "claimed_by: agent-a") {
		t.Fatalf("claim did not write the claimant:\n%s", readTicketFile(t, path))
	}

	_, _, err := executeCollectingInput(t, options, nil, "tickets", "claim", "shared-work", "--as", "agent-b")
	if err == nil || clierr.CodeOf(err) != clierr.Locked || !strings.Contains(err.Error(), "agent-a") {
		t.Fatalf("second claimant = %v (code %q)", err, clierr.CodeOf(err))
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "claim", "shared-work", "--as", "agent-a"); err != nil {
		t.Fatalf("same-claimant claim = %v", err)
	}

	_, _, err = executeCollectingInput(t, options, nil, "tickets", "unclaim", "shared-work", "--as", "agent-b")
	if err == nil || clierr.CodeOf(err) != clierr.Locked {
		t.Fatalf("unclaim by another claimant = %v (code %q)", err, clierr.CodeOf(err))
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "unclaim", "shared-work", "--as", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readTicketFile(t, path), "claimed_by: agent-a") {
		t.Fatalf("unclaim kept the claimant:\n%s", readTicketFile(t, path))
	}
}

func TestTicketsClaimWithoutAsNeedsATerminal(t *testing.T) {
	t.Setenv("TWT_CLAIMANT", "")
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "agent work"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, nil, "tickets", "claim", "agent-work")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || clierr.ExitCode(err) != 2 {
		t.Fatalf("non-terminal claim without --as = %v (code %q)", err, clierr.CodeOf(err))
	}
	if hint := clierr.HintOf(err); hint != "Pass --as NAME when twt runs without a terminal." {
		t.Fatalf("claim hint = %q", hint)
	}

	t.Setenv("TWT_CLAIMANT", "env-agent")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "claim", "agent-work"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTicketFile(t, filepath.Join(home, "agent-work.md")), "claimed_by: env-agent") {
		t.Fatal("TWT_CLAIMANT did not set the claimant")
	}
}

func TestTicketsListReadyFiltersPickableWork(t *testing.T) {
	t.Setenv("TWT_CLAIMANT", "")
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "ready work", "--status", "ready-for-agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "claimed work", "--status", "ready-for-agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "claim", "claimed-work", "--as", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "triage work"); err != nil {
		t.Fatal(err)
	}
	blocked := "---\n" +
		"title: \"Blocked work\"\n" +
		"status: ready-for-agent\n" +
		"priority: 2\n" +
		"blocked_by:\n" +
		"  - \"[[triage-work]]\"\n" +
		"---\n\n# Blocked work\n"
	if err := os.WriteFile(filepath.Join(home, "blocked-work.md"), []byte(blocked), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := executeCollectingInput(t, options, nil, "tickets", "list", "--ready", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		SchemaVersion int              `json:"schemaVersion"`
		Tickets       []map[string]any `json:"tickets"`
		TotalCount    int              `json:"totalCount"`
		Truncated     bool             `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(stdout), &list); err != nil {
		t.Fatalf("decode list: %v\n%s", err, stdout)
	}
	if list.SchemaVersion != 1 || list.TotalCount != 1 || len(list.Tickets) != 1 || list.Truncated {
		t.Fatalf("ready list = %s", stdout)
	}
	if list.Tickets[0]["slug"] != "ready-work" {
		t.Fatalf("ready list slug = %v", list.Tickets[0]["slug"])
	}
	keys := make([]string, 0, len(list.Tickets[0]))
	for key := range list.Tickets[0] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	want := "blockedBy,board,claimedBy,created,path,priority,slug,status,title,updated"
	if strings.Join(keys, ",") != want {
		t.Fatalf("ticket object keys = %v, want %s", keys, want)
	}

	_, _, err = executeCollectingInput(t, options, nil, "tickets", "list", "--ready", "--status", "done")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("--ready with --status = %v (code %q)", err, clierr.CodeOf(err))
	}
}

func TestTicketsReferencesResolveThroughTheCLI(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "fix the vfs tools"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "reconnect the monitor"); err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{"[[fix-the-vfs-tools]]", "fix-the", "fix the vfs tools"} {
		stdout, _, err := executeCollectingInput(t, options, nil, "tickets", "show", reference, "--output", "json")
		if err != nil {
			t.Fatalf("show %q: %v", reference, err)
		}
		if !strings.Contains(stdout, `"slug":"fix-the-vfs-tools"`) {
			t.Fatalf("show %q = %s", reference, stdout)
		}
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "fix the docs"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, nil, "tickets", "show", "fix-the")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("ambiguous prefix = %v (code %q)", err, clierr.CodeOf(err))
	}
	hint := clierr.HintOf(err)
	if !strings.Contains(hint, "fix-the-vfs-tools") || !strings.Contains(hint, "fix-the-docs") {
		t.Fatalf("ambiguous prefix hint = %q", hint)
	}
}

func TestTicketsShowRendersOpenBlockersAndBody(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	blocked := "---\n" +
		"title: \"Blocked work\"\n" +
		"status: ready-for-agent\n" +
		"priority: 2\n" +
		"blocked_by:\n" +
		"  - \"[[missing-blocker]]\"\n" +
		"---\n\n# Blocked work\n\nThe body text.\n"
	if err := os.WriteFile(filepath.Join(home, "blocked-work.md"), []byte(blocked), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := executeCollectingInput(t, options, nil, "tickets", "show", "blocked-work")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "missing-blocker (missing)") || !strings.Contains(stdout, "The body text.") {
		t.Fatalf("show text = %s", stdout)
	}
	jsonOut, _, err := executeCollectingInput(t, options, nil, "tickets", "show", "blocked-work", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var show struct {
		SchemaVersion int `json:"schemaVersion"`
		Ticket        struct {
			Slug          string `json:"slug"`
			Body          string `json:"body"`
			Ready         bool   `json:"ready"`
			BlockedByOpen []struct {
				Slug    string `json:"slug"`
				Missing bool   `json:"missing"`
			} `json:"blockedByOpen"`
		} `json:"ticket"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &show); err != nil {
		t.Fatalf("decode show: %v\n%s", err, jsonOut)
	}
	if show.SchemaVersion != 1 || show.Ticket.Slug != "blocked-work" || show.Ticket.Ready ||
		!strings.Contains(show.Ticket.Body, "The body text.") ||
		len(show.Ticket.BlockedByOpen) != 1 || !show.Ticket.BlockedByOpen[0].Missing {
		t.Fatalf("show envelope = %s", jsonOut)
	}
}

func TestTicketsSetChangesFields(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "boards", "create", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "move me"); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCollectingInput(t, options, nil, "tickets", "set", "move-me")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("set without a flag = %v (code %q)", err, clierr.CodeOf(err))
	}
	_, _, err = executeCollectingInput(t, options, nil, "tickets", "set", "move-me", "--board", "missing")
	if err == nil || clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("set to a missing Board = %v (code %q)", err, clierr.CodeOf(err))
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "set", "move-me", "--status", "done", "--board", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(home, "change-monitor", "move-me.md")
	content := readTicketFile(t, moved)
	if !strings.Contains(content, "status: done") || !strings.Contains(content, "board: change-monitor") {
		t.Fatalf("set result:\n%s", content)
	}
	if _, err := os.Stat(filepath.Join(home, "move-me.md")); !os.IsNotExist(err) {
		t.Fatalf("set kept the old file: %v", err)
	}
}

func TestTicketsEditAndComment(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "edit me"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "edit-me.md")

	_, _, err := executeCollectingInput(t, options, nil, "tickets", "edit", "edit-me")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("non-terminal edit without --stdin = %v (code %q)", err, clierr.CodeOf(err))
	}
	newBody := "# edit me\n\nThe replaced body.\n"
	if _, _, err := executeCollectingInput(t, options, strings.NewReader(newBody), "tickets", "edit", "edit-me", "--stdin"); err != nil {
		t.Fatal(err)
	}
	content := readTicketFile(t, path)
	if !strings.Contains(content, "The replaced body.") || strings.Contains(content, "## What to build") {
		t.Fatalf("edit result:\n%s", content)
	}

	_, _, err = executeCollectingInput(t, options, nil, "tickets", "comment", "edit-me")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("comment without --stdin = %v (code %q)", err, clierr.CodeOf(err))
	}
	if _, _, err := executeCollectingInput(t, options, strings.NewReader("A first note."),
		"tickets", "comment", "edit-me", "--stdin"); err != nil {
		t.Fatal(err)
	}
	content = readTicketFile(t, path)
	commentsAt := strings.Index(content, "## Comments")
	noteAt := strings.Index(content, "A first note.")
	if commentsAt < 0 || noteAt < commentsAt {
		t.Fatalf("comment result:\n%s", content)
	}

	// An interactive edit that saves an invalid file reports unsafe_state and
	// keeps the file.
	options.OpenEditor = func(editPath string) error {
		return os.WriteFile(editPath, []byte("---\ntitle: [broken\n---\nbody\n"), 0o644)
	}
	_, _, err = executeCollectingInput(t, options, strings.NewReader(""), "tickets", "edit", "edit-me")
	if err == nil || clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("invalid editor edit = %v (code %q)", err, clierr.CodeOf(err))
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, path) {
		t.Fatalf("invalid editor edit hint = %q", hint)
	}
	if !strings.Contains(readTicketFile(t, path), "title: [broken") {
		t.Fatal("the invalid edit did not stay on disk")
	}
}

func TestTicketsNeedAConfiguredHome(t *testing.T) {
	t.Setenv("TWT_TICKETS_HOME", "")
	root := t.TempDir()
	options := cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}
	_, _, err := executeCollectingInput(t, options, nil, "tickets", "list")
	if err == nil || clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("tickets without a home = %v (code %q)", err, clierr.CodeOf(err))
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "TWT_TICKETS_HOME") {
		t.Fatalf("tickets home hint = %q", hint)
	}

	// The config file sets the home when the Options value and the
	// environment are empty.
	home := filepath.Join(root, "vault-tickets")
	if err := os.MkdirAll(options.ConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(options.ConfigDir, "config.yaml"),
		[]byte("ticketsHome: "+home+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "index.md")); statErr != nil {
		t.Fatalf("config-configured init: %v", statErr)
	}

	// A broken config file surfaces at command time.
	if err := os.WriteFile(filepath.Join(options.ConfigDir, "config.yaml"),
		[]byte("ticketsHome: x\nunknownField: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "list"); err == nil || !strings.Contains(err.Error(), "unknownField") {
		t.Fatalf("broken config = %v", err)
	}
}

func TestTicketsCompletionsReadTheHome(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "boards", "create", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "fix the vfs tools"); err != nil {
		t.Fatal(err)
	}
	command := cli.New(options)
	show := findCommand(command, "tickets", "show")
	if show == nil || show.ValidArgsFunction == nil {
		t.Fatal("twt tickets show has no argument completion")
	}
	names, _ := show.ValidArgsFunction(show, nil, "")
	if strings.Join(names, ",") != "fix-the-vfs-tools" {
		t.Fatalf("ticket completion = %v", names)
	}
	create := findCommand(command, "tickets", "create")
	boardFlag, found := create.GetFlagCompletionFunc("board")
	if !found {
		t.Fatal("--board has no completion function")
	}
	names, _ = boardFlag(create, nil, "")
	if strings.Join(names, ",") != "change-monitor" {
		t.Fatalf("--board completion = %v", names)
	}
	boardsShow := findCommand(command, "tickets", "boards", "show")
	names, _ = boardsShow.ValidArgsFunction(boardsShow, nil, "")
	if strings.Join(names, ",") != "change-monitor" {
		t.Fatalf("Board completion = %v", names)
	}

	// Completions are silent when no Tickets home is set.
	t.Setenv("TWT_TICKETS_HOME", "")
	unset := cli.New(cli.Options{ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir()})
	unsetShow := findCommand(unset, "tickets", "show")
	if names, _ = unsetShow.ValidArgsFunction(unsetShow, nil, ""); len(names) != 0 {
		t.Fatalf("completion without a home = %v", names)
	}
}

func TestTicketsBoardsCommands(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	jsonOut, _, err := executeCollectingInput(t, options, nil, "tickets", "boards", "create", "change-monitor", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	created := decodeTicketMutation(t, jsonOut)
	if created.Operation != "tickets.boards.create" || created.Status != "applied" || created.Name != "change-monitor" {
		t.Fatalf("boards create envelope = %+v", created)
	}
	if _, statErr := os.Stat(filepath.Join(home, "change-monitor", "index.md")); statErr != nil {
		t.Fatalf("boards create did not scaffold index.md: %v", statErr)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "grouped work", "--board", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	listOut, _, err := executeCollectingInput(t, options, nil, "tickets", "boards", "list", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var boards struct {
		SchemaVersion int `json:"schemaVersion"`
		Boards        []struct {
			Name    string `json:"name"`
			Tickets int    `json:"tickets"`
		} `json:"boards"`
		TotalCount int `json:"totalCount"`
	}
	if err := json.Unmarshal([]byte(listOut), &boards); err != nil {
		t.Fatalf("decode boards list: %v\n%s", err, listOut)
	}
	if boards.SchemaVersion != 1 || boards.TotalCount != 1 || len(boards.Boards) != 1 ||
		boards.Boards[0].Name != "change-monitor" || boards.Boards[0].Tickets != 1 {
		t.Fatalf("boards list = %s", listOut)
	}
	showOut, _, err := executeCollectingInput(t, options, nil, "tickets", "boards", "show", "change-monitor", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showOut, `"board":{`) || !strings.Contains(showOut, `"name":"change-monitor"`) {
		t.Fatalf("boards show = %s", showOut)
	}
}

func TestSchemaListsTicketCommandsAndApplyOperations(t *testing.T) {
	output, err := execute(t, t.TempDir(), "schema")
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		`"twt tickets init"`, `"twt tickets create"`, `"twt tickets list"`, `"twt tickets show"`,
		`"twt tickets edit"`, `"twt tickets set"`, `"twt tickets claim"`, `"twt tickets unclaim"`,
		`"twt tickets comment"`, `"twt tickets boards create"`, `"twt tickets boards list"`, `"twt tickets boards show"`,
	} {
		if !strings.Contains(output, command) {
			t.Fatalf("schema misses %s", command)
		}
	}
	for _, operation := range []string{
		`"tickets.create"`, `"tickets.set"`, `"tickets.claim"`, `"tickets.unclaim"`, `"tickets.comment"`, `"tickets.boards.create"`,
	} {
		if !strings.Contains(output, operation) {
			t.Fatalf("schema misses the apply operation %s", operation)
		}
	}
}

func TestApplySupportsTicketOperations(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.boards.create","board":{"name":"change-monitor"}}`),
		"apply", "--stdin", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.create","ticket":{"title":"apply work","board":"change-monitor","status":"ready-for-agent","priority":1,"body":"From apply."}}`),
		"apply", "--stdin", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	created := decodeTicketMutation(t, stdout)
	if created.Operation != "tickets.create" || created.Status != "applied" || created.ID != "apply-work" {
		t.Fatalf("apply tickets.create = %+v", created)
	}
	content := readTicketFile(t, filepath.Join(home, "change-monitor", "apply-work.md"))
	if !strings.Contains(content, "priority: 1") || !strings.Contains(content, "From apply.") {
		t.Fatalf("apply ticket file:\n%s", content)
	}

	// Apply is never a terminal: claim and unclaim require ticket.as.
	_, _, err = executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.claim","ticket":{"reference":"apply-work"}}`),
		"apply", "--stdin", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), "ticket.as") {
		t.Fatalf("apply claim without as = %v", err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.claim","ticket":{"reference":"apply-work","as":"apply-agent"}}`),
		"apply", "--stdin", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.comment","ticket":{"reference":"apply-work","text":"Done."}}`),
		"apply", "--stdin", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.set","ticket":{"reference":"apply-work","status":"done"}}`),
		"apply", "--stdin", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.unclaim","ticket":{"reference":"apply-work","as":"apply-agent"}}`),
		"apply", "--stdin", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	content = readTicketFile(t, filepath.Join(home, "change-monitor", "apply-work.md"))
	if !strings.Contains(content, "status: done") || !strings.Contains(content, "Done.") || strings.Contains(content, "claimed_by: apply-agent") {
		t.Fatalf("apply mutations result:\n%s", content)
	}

	// Strict payloads reject fields from other operations.
	_, _, err = executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.set","ticket":{"reference":"apply-work","title":"nope"}}`),
		"apply", "--stdin", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatalf("apply set with an unknown field = %v", err)
	}
}

func TestDoctorReportsTheTicketsHome(t *testing.T) {
	t.Setenv("TWT_TICKETS_HOME", "")
	root := t.TempDir()
	unset := cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}
	stdout, _, err := executeCollectingInput(t, unset, nil, "doctor")
	if err != nil {
		t.Fatalf("doctor without a home = %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "warn\ttickets-home\tNo Tickets home is set") {
		t.Fatalf("doctor output = %s", stdout)
	}

	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err = executeCollectingInput(t, options, nil, "doctor")
	if err != nil {
		t.Fatalf("doctor with a home = %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "pass\ttickets-home\t"+home) {
		t.Fatalf("doctor output = %s", stdout)
	}

	missing := options
	missing.TicketsHome = filepath.Join(home, "not-created")
	stdout, _, err = executeCollectingInput(t, missing, nil, "doctor")
	if err != nil {
		t.Fatalf("doctor with a missing home = %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "warn\ttickets-home\t") || !strings.Contains(stdout, "does not exist") {
		t.Fatalf("doctor output = %s", stdout)
	}
}
