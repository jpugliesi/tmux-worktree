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
	"github.com/spf13/cobra"
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
	if strings.Count(stdout, "Wrote") != 3 {
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
	if strings.Count(stdout, "Kept") != 3 {
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
	if result.SchemaVersion != 2 || result.Operation != "tickets.init" || result.Status != "applied" {
		t.Fatalf("init envelope = %+v", result)
	}
}

func TestTicketsCreateWritesAnObsidianNote(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "fix the vfs tools", "--project", "change-monitor", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	result := decodeTicketMutation(t, stdout)
	if result.SchemaVersion != 2 || result.Operation != "tickets.create" || result.Status != "applied" || result.ID != "fix-the-vfs-tools" {
		t.Fatalf("create envelope = %+v", result)
	}
	content := readTicketFile(t, filepath.Join(home, "change-monitor", "fix-the-vfs-tools.md"))
	for _, want := range []string{
		"---\n",
		"title: \"fix the vfs tools\"",
		"status: needs-triage",
		"priority: 2",
		"project: change-monitor",
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

	_, _, err = executeCollectingInput(t, options, nil, "tickets", "create", "another one", "--project", "missing")
	if err == nil || clierr.CodeOf(err) != clierr.NotFound || !strings.Contains(clierr.HintOf(err), "projects create") {
		t.Fatalf("missing Project = %v (code %q, hint %q)", err, clierr.CodeOf(err), clierr.HintOf(err))
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
	if hint := clierr.HintOf(err); !strings.Contains(hint, "or -.") {
		t.Fatalf("bare create hint = %q", hint)
	}
}

func TestTicketsCreateFromStdin(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, strings.NewReader("body"), "tickets", "create", "-")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(clierr.HintOf(err), "--title") {
		t.Fatalf("- without --title = %v (hint %q)", err, clierr.HintOf(err))
	}
	body := "Steps:\n\n1. Reconnect the tools\n"
	if _, _, err := executeCollectingInput(t, options, strings.NewReader(body),
		"tickets", "create", "--title", "Fix auth", "-"); err != nil {
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

// wizardOptions seeds an interactive tickets create: the Project picker and the
// editor are injected so tests never start fzf or VISUAL.
func wizardOptions(t *testing.T, projectChoice, body string) (cli.Options, string) {
	t.Helper()
	options, home := ticketTestOptions(t)
	options.PickTicketProject = func(_ *cobra.Command, _ []string) (string, error) {
		return projectChoice, nil
	}
	options.OpenEditor = func(path string) error {
		return os.WriteFile(path, []byte(body), 0o644)
	}
	return options, home
}

func TestTicketsCreateWizardWritesDescriptionOnly(t *testing.T) {
	options, home := wizardOptions(t, "(none)", "Reconnect the vfs tools.\n")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("Editor ticket\n"), "tickets", "create")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "Title: ") {
		t.Fatalf("wizard stderr = %q", stderr)
	}
	if !strings.Contains(stdout, `Created ticket "editor-ticket"`) {
		t.Fatalf("wizard stdout = %q", stdout)
	}
	content := readTicketFile(t, filepath.Join(home, "editor-ticket.md"))
	for _, want := range []string{
		"title: \"Editor ticket\"",
		"status: needs-triage",
		"priority: 2",
		"project:",
		"# Editor ticket",
		"Reconnect the vfs tools.",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("wizard ticket misses %q:\n%s", want, content)
		}
	}
	whatAt := strings.Index(content, "## What to build")
	bodyAt := strings.Index(content, "Reconnect the vfs tools.")
	if whatAt < 0 || bodyAt < whatAt {
		t.Fatalf("wizard description is not under '## What to build':\n%s", content)
	}
	if strings.Contains(content, "title: \"<title>\"") {
		t.Fatalf("wizard left the template title:\n%s", content)
	}
}

func TestTicketsCreateWizardEmptyTitleCancels(t *testing.T) {
	options, home := wizardOptions(t, "(none)", "body")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, strings.NewReader("\n"), "tickets", "create")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("empty title = %v (code %q)", err, clierr.CodeOf(err))
	}
	after := homeFiles(t, home)
	for _, path := range after {
		if strings.HasSuffix(path, ".md") && !strings.HasSuffix(path, "index.md") && !strings.Contains(path, "templates/") {
			t.Fatalf("empty title wrote a ticket: %s", path)
		}
	}
}

func TestTicketsCreateWizardEmptyEditorCancels(t *testing.T) {
	options, _ := wizardOptions(t, "(none)", "")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, strings.NewReader("Has a title\n"), "tickets", "create")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("empty editor = %v (code %q)", err, clierr.CodeOf(err))
	}
}

func TestTicketsCreateWizardPicksAnExistingProject(t *testing.T) {
	options, home := wizardOptions(t, "change-monitor", "Do the work.\n")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, strings.NewReader("Grouped work\n"), "tickets", "create"); err != nil {
		t.Fatal(err)
	}
	content := readTicketFile(t, filepath.Join(home, "change-monitor", "grouped-work.md"))
	if !strings.Contains(content, "project: change-monitor") || !strings.Contains(content, "Do the work.") {
		t.Fatalf("existing Project ticket:\n%s", content)
	}
}

func TestTicketsCreateWizardCreatesAProjectAfterConfirm(t *testing.T) {
	options, home := wizardOptions(t, "new-project", "From the editor.\n")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("New project work\n\n"), "tickets", "create")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, `Project "new-project" does not exist. Create it? [Y/n]`) {
		t.Fatalf("confirm stderr = %q", stderr)
	}
	if !strings.Contains(stdout, `Created Project "new-project"`) || !strings.Contains(stdout, `Created ticket "new-project-work"`) {
		t.Fatalf("create stdout = %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(home, "new-project", "index.md")); err != nil {
		t.Fatalf("missing Project index: %v", err)
	}
	content := readTicketFile(t, filepath.Join(home, "new-project", "new-project-work.md"))
	if !strings.Contains(content, "project: new-project") {
		t.Fatalf("new Project ticket:\n%s", content)
	}
}

func TestTicketsCreateWizardDryRunCreatesNeitherProjectNorTicket(t *testing.T) {
	options, home := wizardOptions(t, "preview-project", "Dry body.\n")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	before := homeFiles(t, home)
	stdout, _, err := executeCollectingInput(t, options, strings.NewReader("Preview work\n\n"),
		"tickets", "create", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `Would create Project "preview-project"`) || !strings.Contains(stdout, "title: \"Preview work\"") {
		t.Fatalf("dry-run stdout = %q", stdout)
	}
	jsonOut, _, err := executeCollectingInput(t, options, strings.NewReader("Preview work\n\n"),
		"tickets", "create", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	result := decodeTicketMutation(t, jsonOut)
	if result.Operation != "tickets.create" || result.Status != "valid" || result.ID != "preview-work" {
		t.Fatalf("dry-run envelope = %+v", result)
	}
	if strings.Count(jsonOut, `"operation"`) != 1 {
		t.Fatalf("dry-run emitted more than one envelope:\n%s", jsonOut)
	}
	after := homeFiles(t, home)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("dry-run changed the Tickets home:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestTicketsCreateWizardRejectsNewProjectConfirm(t *testing.T) {
	options, home := wizardOptions(t, "rejected-project", "body")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, strings.NewReader("Rejected\nn\n"), "tickets", "create")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("declined Project = %v (code %q)", err, clierr.CodeOf(err))
	}
	if _, err := os.Stat(filepath.Join(home, "rejected-project")); !os.IsNotExist(err) {
		t.Fatalf("declined Project still exists: %v", err)
	}
}

func TestTicketsCreateWizardRejectsInvalidNewProjectName(t *testing.T) {
	options, _ := wizardOptions(t, "../escape", "body")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := executeCollectingInput(t, options, strings.NewReader("Bad name\n"), "tickets", "create")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("invalid Project name = %v (code %q)", err, clierr.CodeOf(err))
	}
	if strings.Contains(stderr, "Create it?") {
		t.Fatalf("confirmed an invalid Project name: %q", stderr)
	}

	options, _ = wizardOptions(t, "templates", "body")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, stderr, err = executeCollectingInput(t, options, strings.NewReader("Reserved\n"), "tickets", "create")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("reserved Project = %v (code %q)", err, clierr.CodeOf(err))
	}
	if strings.Contains(stderr, "Create it?") {
		t.Fatalf("confirmed reserved Project: %q", stderr)
	}
}

func TestTicketsCreateWizardProjectFlagDoesNotCreate(t *testing.T) {
	options, _ := wizardOptions(t, "ignored", "From editor.\n")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, strings.NewReader("Flag missing\n"),
		"tickets", "create", "--project", "missing")
	if err == nil || clierr.CodeOf(err) != clierr.NotFound || !strings.Contains(clierr.HintOf(err), "projects create") {
		t.Fatalf("missing --project = %v (code %q, hint %q)", err, clierr.CodeOf(err), clierr.HintOf(err))
	}

	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	called := false
	options.PickTicketProject = func(_ *cobra.Command, _ []string) (string, error) {
		called = true
		return "ignored", nil
	}
	if _, _, err := executeCollectingInput(t, options, strings.NewReader("Flag existing\n"),
		"tickets", "create", "--project", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("--project still opened the Project picker")
	}
}

func TestTicketsCreateWizardSkipsTitleWhenFlagSet(t *testing.T) {
	options, home := wizardOptions(t, "(none)", "Flag title body.\n")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := executeCollectingInput(t, options, strings.NewReader(""),
		"tickets", "create", "--title", "From flag")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "Title: ") {
		t.Fatalf("--title still prompted: %q", stderr)
	}
	if _, err := os.Stat(filepath.Join(home, "from-flag.md")); err != nil {
		t.Fatalf("missing --title ticket: %v", err)
	}
}

func TestTicketsCreateDescriptionDoesNotOpenTheWizard(t *testing.T) {
	options, home := wizardOptions(t, "must-not-run", "must-not-write")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, strings.NewReader("SHOULD-NOT-BE-TITLE\n"),
		"tickets", "create", "unguarded description"); err != nil {
		t.Fatal(err)
	}
	content := readTicketFile(t, filepath.Join(home, "unguarded-description.md"))
	if !strings.Contains(content, "title: \"unguarded description\"") {
		t.Fatalf("DESCRIPTION used stdin as the title:\n%s", content)
	}
	if strings.Contains(content, "SHOULD-NOT-BE-TITLE") || strings.Contains(content, "must-not-write") {
		t.Fatalf("DESCRIPTION entered the wizard:\n%s", content)
	}
}

func TestTicketsCreateStdinDoesNotOpenTheWizard(t *testing.T) {
	options, home := wizardOptions(t, "must-not-run", "must-not-write")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	body := "stdin body\n"
	if _, _, err := executeCollectingInput(t, options, strings.NewReader(body),
		"tickets", "create", "--title", "Stdin ticket", "-"); err != nil {
		t.Fatal(err)
	}
	content := readTicketFile(t, filepath.Join(home, "stdin-ticket.md"))
	if !strings.Contains(content, "stdin body") || strings.Contains(content, "must-not-write") {
		t.Fatalf("stdin entered the wizard:\n%s", content)
	}
}

func TestTicketsCreateWizardJSONIsOneEnvelope(t *testing.T) {
	options, _ := wizardOptions(t, "(none)", "JSON body.\n")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("JSON ticket\n"),
		"tickets", "create", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "Title: ") {
		t.Fatalf("json wizard stderr = %q", stderr)
	}
	result := decodeTicketMutation(t, stdout)
	if result.Operation != "tickets.create" || result.Status != "applied" || result.ID != "json-ticket" {
		t.Fatalf("json wizard envelope = %+v\n%s", result, stdout)
	}
	if strings.Count(stdout, `"operation"`) != 1 {
		t.Fatalf("json wizard emitted more than one envelope:\n%s", stdout)
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

func TestTicketsLsMatchesList(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "ls alias work"); err != nil {
		t.Fatal(err)
	}
	listOut, _, err := executeCollectingInput(t, options, nil, "tickets", "list")
	if err != nil {
		t.Fatal(err)
	}
	lsOut, _, err := executeCollectingInput(t, options, nil, "tickets", "ls")
	if err != nil {
		t.Fatal(err)
	}
	if listOut != lsOut {
		t.Fatalf("ls output differs from list:\nlist=%q\nls=%q", listOut, lsOut)
	}
}

func TestTicketsListTextUsesAProjectColumnOnlyForAllProjects(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"change-monitor", "core"} {
		if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", name); err != nil {
			t.Fatal(err)
		}
	}
	creates := [][]string{
		{"tickets", "create", "monitor work", "--project", "change-monitor"},
		{"tickets", "create", "high monitor work", "--project", "change-monitor"},
		{"tickets", "create", "core work", "--project", "core"},
		{"tickets", "create", "inbox note"},
	}
	for _, args := range creates {
		if _, _, err := executeCollectingInput(t, options, nil, args...); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "set", "high-monitor-work", "--priority", "0"); err != nil {
		t.Fatal(err)
	}

	textOut, _, err := executeCollectingInput(t, options, nil, "tickets", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textOut, "PROJECT") {
		t.Fatalf("wide list has no PROJECT column:\n%s", textOut)
	}
	if got := ticketTableSlugs(t, textOut); strings.Join(got, ",") != "high-monitor-work,core-work,inbox-note,monitor-work" {
		t.Fatalf("wide list slugs = %v\n%s", got, textOut)
	}
	if !strings.Contains(textOut, "(none)") {
		t.Fatalf("wide list hides ungrouped Tickets:\n%s", textOut)
	}

	filtered, _, err := executeCollectingInput(t, options, nil, "tickets", "list", "--project", "change-monitor")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(filtered, "PROJECT") {
		t.Fatalf("scoped list still has a PROJECT column:\n%s", filtered)
	}
	if got := ticketTableSlugs(t, filtered); strings.Join(got, ",") != "high-monitor-work,monitor-work" {
		t.Fatalf("--project slugs = %v\n%s", got, filtered)
	}
	if strings.Contains(filtered, "core-work") || strings.Contains(filtered, "inbox-note") {
		t.Fatalf("--project list leaked other Projects:\n%s", filtered)
	}

	jsonOut, _, err := executeCollectingInput(t, options, nil, "tickets", "list", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Tickets []struct {
			Slug    string `json:"slug"`
			Project string `json:"project"`
		} `json:"tickets"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &list); err != nil {
		t.Fatalf("decode list: %v\n%s", err, jsonOut)
	}
	slugs := make([]string, 0, len(list.Tickets))
	for _, ticket := range list.Tickets {
		slugs = append(slugs, ticket.Slug)
	}
	if strings.Join(slugs, ",") != "high-monitor-work,core-work,inbox-note,monitor-work" {
		t.Fatalf("JSON list order = %v\n%s", slugs, jsonOut)
	}
	if list.Tickets[2].Project != "" || list.Tickets[0].Project != "change-monitor" {
		t.Fatalf("JSON list is not a flat Ticket array:\n%s", jsonOut)
	}
}

func ticketTableSlugs(t *testing.T, output string) []string {
	t.Helper()
	slugIndex := 0
	var slugs []string
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "PROJECT" || fields[0] == "SLUG" {
			for index, field := range fields {
				if field == "SLUG" {
					slugIndex = index
				}
			}
			continue
		}
		if slugIndex >= len(fields) {
			t.Fatalf("ticket row %q has no slug column:\n%s", line, output)
		}
		slugs = append(slugs, fields[slugIndex])
	}
	return slugs
}

func TestTicketsCloseResolvesInOneCommand(t *testing.T) {
	t.Setenv("TWT_CLAIMANT", "")
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "ship it", "--status", "ready-for-agent"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "ship-it.md")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "claim", "ship-it", "--as", "agent-a"); err != nil {
		t.Fatal(err)
	}

	// A dry run writes nothing.
	before := readTicketFile(t, path)
	stdout, _, err := executeCollectingInput(t, options, nil,
		"tickets", "close", "ship-it", "--as", "agent-a", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	preview := decodeTicketMutation(t, stdout)
	if preview.Operation != "tickets.close" || preview.Status != "valid" || preview.ID != "ship-it" {
		t.Fatalf("dry-run envelope = %+v\n%s", preview, stdout)
	}
	if readTicketFile(t, path) != before {
		t.Fatal("a dry-run close changed the file")
	}

	// Another claimant is locked out.
	_, _, err = executeCollectingInput(t, options, nil, "tickets", "close", "ship-it", "--as", "agent-b")
	if err == nil || clierr.CodeOf(err) != clierr.Locked || !strings.Contains(err.Error(), "agent-a") {
		t.Fatalf("close by another claimant = %v (code %q)", err, clierr.CodeOf(err))
	}

	// The text run confirms the close.
	stdout, _, err = executeCollectingInput(t, options, nil, "tickets", "close", "ship-it", "--as", "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout) != `Closed Ticket "ship-it"` {
		t.Fatalf("close text = %q", stdout)
	}
	content := readTicketFile(t, filepath.Join(home, "closed", "ship-it.md"))
	if !strings.Contains(content, "status: done") || strings.Contains(content, "claimed_by: agent-a") {
		t.Fatalf("close result:\n%s", content)
	}

	// The JSON envelope names the operation.
	stdout, _, err = executeCollectingInput(t, options, nil,
		"tickets", "close", "ship-it", "--as", "agent-a", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	applied := decodeTicketMutation(t, stdout)
	if applied.SchemaVersion != 2 || applied.Operation != "tickets.close" || applied.Status != "applied" || applied.ID != "ship-it" {
		t.Fatalf("close envelope = %+v\n%s", applied, stdout)
	}
}

func TestTicketsCloseWithoutAsNeedsATerminal(t *testing.T) {
	t.Setenv("TWT_CLAIMANT", "")
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "close me"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, nil, "tickets", "close", "close-me")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || clierr.ExitCode(err) != 2 {
		t.Fatalf("non-terminal close without --as = %v (code %q)", err, clierr.CodeOf(err))
	}
	if hint := clierr.HintOf(err); hint != "Pass --as NAME when twt runs without a terminal." {
		t.Fatalf("close hint = %q", hint)
	}
}

func TestTicketsDoctorAndRepairLocationMismatches(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "old shipped work"); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(home, "old-shipped-work.md")
	content := strings.Replace(readTicketFile(t, active), "status: needs-triage", "status: done", 1)
	if err := os.WriteFile(active, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	doctorJSON, _, err := executeCollectingInput(t, options, nil, "tickets", "doctor", "--output", "json")
	if err != nil {
		t.Fatalf("tickets doctor: %v", err)
	}
	var doctor struct {
		SchemaVersion int `json:"schemaVersion"`
		Report        struct {
			Healthy bool `json:"healthy"`
			Issues  []struct {
				Code        string `json:"code"`
				Destination string `json:"destination"`
			} `json:"issues"`
		} `json:"report"`
	}
	if err := json.Unmarshal([]byte(doctorJSON), &doctor); err != nil {
		t.Fatalf("decode doctor: %v\n%s", err, doctorJSON)
	}
	destination := filepath.Join(home, "closed", "old-shipped-work.md")
	if doctor.SchemaVersion != 2 || doctor.Report.Healthy || len(doctor.Report.Issues) != 1 ||
		doctor.Report.Issues[0].Code != "location_mismatch" || doctor.Report.Issues[0].Destination != destination {
		t.Fatalf("doctor output = %+v\n%s", doctor, doctorJSON)
	}

	dryJSON, _, err := executeCollectingInput(t, options, nil, "tickets", "repair", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("tickets repair --dry-run: %v", err)
	}
	if !strings.Contains(dryJSON, `"operation":"tickets.repair"`) || !strings.Contains(dryJSON, `"status":"valid"`) {
		t.Fatalf("repair dry-run output = %s", dryJSON)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("repair dry run moved the source: %v", err)
	}

	appliedJSON, _, err := executeCollectingInput(t, options, nil, "tickets", "repair", "--output", "json")
	if err != nil {
		t.Fatalf("tickets repair: %v", err)
	}
	if !strings.Contains(appliedJSON, `"status":"applied"`) || !strings.Contains(appliedJSON, `"movedCount":1`) {
		t.Fatalf("repair output = %s", appliedJSON)
	}
	if _, err := os.Stat(active); !os.IsNotExist(err) {
		t.Fatalf("repair kept the source: %v", err)
	}
	if readTicketFile(t, destination) != content {
		t.Fatal("repair changed the Ticket content")
	}
}

func TestTicketsListHidesClosedTicketsByDefault(t *testing.T) {
	t.Setenv("TWT_CLAIMANT", "")
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"open work", "shipped work", "dropped work"} {
		if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", title); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "close", "shipped-work", "--as", "agent-a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "set", "dropped-work", "--status", "wontfix"); err != nil {
		t.Fatal(err)
	}

	slugs := func(args ...string) []string {
		t.Helper()
		stdout, _, err := executeCollectingInput(t, options, nil, append([]string{"tickets", "list", "--output", "json"}, args...)...)
		if err != nil {
			t.Fatalf("tickets list %v: %v", args, err)
		}
		var list struct {
			Tickets []struct {
				Slug string `json:"slug"`
			} `json:"tickets"`
			TotalCount int `json:"totalCount"`
		}
		if err := json.Unmarshal([]byte(stdout), &list); err != nil {
			t.Fatalf("decode list: %v\n%s", err, stdout)
		}
		if list.TotalCount != len(list.Tickets) {
			t.Fatalf("totalCount = %d, tickets = %d", list.TotalCount, len(list.Tickets))
		}
		names := make([]string, 0, len(list.Tickets))
		for _, ticket := range list.Tickets {
			names = append(names, ticket.Slug)
		}
		sort.Strings(names)
		return names
	}

	if got := strings.Join(slugs(), ","); got != "open-work" {
		t.Fatalf("default list = %q, want only the open ticket", got)
	}
	if got := strings.Join(slugs("--all"), ","); got != "dropped-work,open-work,shipped-work" {
		t.Fatalf("--all list = %q", got)
	}
	if got := strings.Join(slugs("--status", "done"), ","); got != "shipped-work" {
		t.Fatalf("--status done list = %q", got)
	}
	if got := strings.Join(slugs("--status", "wontfix"), ","); got != "dropped-work" {
		t.Fatalf("--status wontfix list = %q", got)
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
	if list.SchemaVersion != 2 || list.TotalCount != 1 || len(list.Tickets) != 1 || list.Truncated {
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
	want := "blockedBy,claimedBy,created,path,priority,project,slug,status,title,updated"
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
		stdout, _, err := executeCollectingInput(t, options, nil, "tickets", "get", reference, "--output", "json")
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
	_, _, err := executeCollectingInput(t, options, nil, "tickets", "get", "fix-the")
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
	stdout, _, err := executeCollectingInput(t, options, nil, "tickets", "get", "blocked-work")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "missing-blocker (missing)") || !strings.Contains(stdout, "The body text.") {
		t.Fatalf("show text = %s", stdout)
	}
	jsonOut, _, err := executeCollectingInput(t, options, nil, "tickets", "get", "blocked-work", "--output", "json")
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
	if show.SchemaVersion != 2 || show.Ticket.Slug != "blocked-work" || show.Ticket.Ready ||
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
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "move me"); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCollectingInput(t, options, nil, "tickets", "set", "move-me")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("set without a flag = %v (code %q)", err, clierr.CodeOf(err))
	}
	_, _, err = executeCollectingInput(t, options, nil, "tickets", "set", "move-me", "--project", "missing")
	if err == nil || clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("set to a missing Project = %v (code %q)", err, clierr.CodeOf(err))
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "set", "move-me", "--status", "done", "--project", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(home, "closed", "change-monitor", "move-me.md")
	content := readTicketFile(t, moved)
	if !strings.Contains(content, "status: done") || !strings.Contains(content, "project: change-monitor") {
		t.Fatalf("set result:\n%s", content)
	}
	if _, err := os.Stat(filepath.Join(home, "move-me.md")); !os.IsNotExist(err) {
		t.Fatalf("set kept the old file: %v", err)
	}
}

func TestTicketsCreateAndSetBlockedBy(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "base work", "--status", "ready-for-agent"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "create", "follow-up work", "--status", "ready-for-agent",
		"--blocked-by", "[[base-work]]", "--blocked-by", "base-work"); err != nil {
		t.Fatal(err)
	}
	content := readTicketFile(t, filepath.Join(home, "follow-up-work.md"))
	if !strings.Contains(content, "blocked_by:\n  - \"[[base-work]]\"\n") {
		t.Fatalf("create --blocked-by:\n%s", content)
	}

	ready, _, err := executeCollectingInput(t, options, nil, "tickets", "list", "--ready", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ready, `"slug":"follow-up-work"`) {
		t.Fatalf("--ready listed a blocked ticket: %s", ready)
	}
	if !strings.Contains(ready, `"slug":"base-work"`) {
		t.Fatalf("--ready missed the startable ticket: %s", ready)
	}

	if _, _, err := executeCollectingInput(t, options, nil,
		"tickets", "set", "follow-up-work", "--blocked-by", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTicketFile(t, filepath.Join(home, "follow-up-work.md")), "blocked_by: []\n") {
		t.Fatalf("set --blocked-by empty:\n%s", readTicketFile(t, filepath.Join(home, "follow-up-work.md")))
	}
	ready, _, err = executeCollectingInput(t, options, nil, "tickets", "list", "--ready", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ready, `"slug":"follow-up-work"`) {
		t.Fatalf("--ready missed the unblocked ticket: %s", ready)
	}

	stdout, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.create","ticket":{"title":"apply blocked","status":"ready-for-agent","blockedBy":["base-work"]}}`),
		"apply", "-", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if created := decodeTicketMutation(t, stdout); created.ID != "apply-blocked" {
		t.Fatalf("apply create = %+v", created)
	}
	if !strings.Contains(readTicketFile(t, filepath.Join(home, "apply-blocked.md")), "[[base-work]]") {
		t.Fatalf("apply create blocked_by:\n%s", readTicketFile(t, filepath.Join(home, "apply-blocked.md")))
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.set","ticket":{"reference":"apply-blocked","blockedBy":[]}}`),
		"apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTicketFile(t, filepath.Join(home, "apply-blocked.md")), "blocked_by: []\n") {
		t.Fatalf("apply set cleared blocked_by:\n%s", readTicketFile(t, filepath.Join(home, "apply-blocked.md")))
	}
}

func TestTicketsComment(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "comment on me"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "comment-on-me.md")

	_, _, err := executeCollectingInput(t, options, nil, "tickets", "comment", "comment-on-me")
	if err == nil || !strings.Contains(err.Error(), "missing required argument -") {
		t.Fatalf("comment without - = %v", err)
	}
	if _, _, err := executeCollectingInput(t, options, strings.NewReader("A first note."),
		"tickets", "comment", "comment-on-me", "-"); err != nil {
		t.Fatal(err)
	}
	content := readTicketFile(t, path)
	commentsAt := strings.Index(content, "## Comments")
	noteAt := strings.Index(content, "A first note.")
	if commentsAt < 0 || noteAt < commentsAt {
		t.Fatalf("comment result:\n%s", content)
	}
}

func TestTicketsHomeOpensTheConfiguredDirectory(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCollectingInput(t, options, nil, "tickets", "home")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("tickets home without a terminal = %v (code %q)", err, clierr.CodeOf(err))
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "terminal") {
		t.Fatalf("tickets home hint = %q", hint)
	}

	var opened string
	options.OpenEditor = func(path string) error {
		opened = path
		return nil
	}
	stdout, _, err := executeCollectingInput(t, options, strings.NewReader(""), "tickets", "home")
	if err != nil {
		t.Fatal(err)
	}
	if opened != home {
		t.Fatalf("tickets home opened %q, want %q", opened, home)
	}
	if !strings.Contains(stdout, home) {
		t.Fatalf("tickets home stdout = %q", stdout)
	}

	opened = "should-not-open"
	jsonOut, _, err := executeCollectingInput(t, options, strings.NewReader(""), "tickets", "home", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if opened != "should-not-open" {
		t.Fatalf("tickets home dry-run opened %q", opened)
	}
	result := decodeTicketMutation(t, jsonOut)
	if result.Operation != "tickets.home" || result.Status != "valid" || result.Name != home {
		t.Fatalf("tickets home dry-run envelope = %+v", result)
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
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "fix the vfs tools"); err != nil {
		t.Fatal(err)
	}
	command := cli.New(options)
	show := findCommand(command, "tickets", "get")
	if show == nil || show.ValidArgsFunction == nil {
		t.Fatal("twt tickets get has no argument completion")
	}
	names, _ := show.ValidArgsFunction(show, nil, "")
	if strings.Join(names, ",") != "fix-the-vfs-tools" {
		t.Fatalf("ticket completion = %v", names)
	}
	closeCommand := findCommand(command, "tickets", "close")
	if closeCommand == nil || closeCommand.ValidArgsFunction == nil {
		t.Fatal("twt tickets close has no argument completion")
	}
	names, _ = closeCommand.ValidArgsFunction(closeCommand, nil, "")
	if strings.Join(names, ",") != "fix-the-vfs-tools" {
		t.Fatalf("close completion = %v", names)
	}
	create := findCommand(command, "tickets", "create")
	projectFlag, found := create.GetFlagCompletionFunc("project")
	if !found {
		t.Fatal("--project has no completion function")
	}
	names, _ = projectFlag(create, nil, "")
	if strings.Join(names, ",") != "change-monitor" {
		t.Fatalf("--project completion = %v", names)
	}
	blockedByFlag, found := create.GetFlagCompletionFunc("blocked-by")
	if !found {
		t.Fatal("create --blocked-by has no completion function")
	}
	names, _ = blockedByFlag(create, nil, "")
	if strings.Join(names, ",") != "fix-the-vfs-tools" {
		t.Fatalf("create --blocked-by completion = %v", names)
	}
	setCommand := findCommand(command, "tickets", "set")
	setBlockedBy, found := setCommand.GetFlagCompletionFunc("blocked-by")
	if !found {
		t.Fatal("set --blocked-by has no completion function")
	}
	names, _ = setBlockedBy(setCommand, nil, "")
	if strings.Join(names, ",") != "fix-the-vfs-tools" {
		t.Fatalf("set --blocked-by completion = %v", names)
	}
	projectsShow := findCommand(command, "projects", "get")
	names, _ = projectsShow.ValidArgsFunction(projectsShow, nil, "")
	if strings.Join(names, ",") != "change-monitor" {
		t.Fatalf("Project completion = %v", names)
	}

	// Completions are silent when no Tickets home is set.
	t.Setenv("TWT_TICKETS_HOME", "")
	unset := cli.New(cli.Options{ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir()})
	unsetShow := findCommand(unset, "tickets", "get")
	if names, _ = unsetShow.ValidArgsFunction(unsetShow, nil, ""); len(names) != 0 {
		t.Fatalf("completion without a home = %v", names)
	}
}

func TestTicketsProjectsCommands(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	jsonOut, _, err := executeCollectingInput(t, options, nil, "projects", "create", "change-monitor", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	created := decodeTicketMutation(t, jsonOut)
	if created.Operation != "projects.create" || created.Status != "applied" || created.Name != "change-monitor" {
		t.Fatalf("projects create envelope = %+v", created)
	}
	if _, statErr := os.Stat(filepath.Join(home, "change-monitor", "index.md")); statErr != nil {
		t.Fatalf("projects create did not scaffold index.md: %v", statErr)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "grouped work", "--project", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	listOut, _, err := executeCollectingInput(t, options, nil, "projects", "list", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var projects struct {
		SchemaVersion int `json:"schemaVersion"`
		Projects      []struct {
			Name    string `json:"name"`
			Tickets int    `json:"tickets"`
		} `json:"projects"`
		TotalCount int `json:"totalCount"`
	}
	if err := json.Unmarshal([]byte(listOut), &projects); err != nil {
		t.Fatalf("decode projects list: %v\n%s", err, listOut)
	}
	if projects.SchemaVersion != 2 || projects.TotalCount != 1 || len(projects.Projects) != 1 ||
		projects.Projects[0].Name != "change-monitor" || projects.Projects[0].Tickets != 1 {
		t.Fatalf("projects list = %s", listOut)
	}
	showOut, _, err := executeCollectingInput(t, options, nil, "projects", "get", "change-monitor", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showOut, `"project":{`) || !strings.Contains(showOut, `"name":"change-monitor"`) {
		t.Fatalf("projects show = %s", showOut)
	}
}

func TestProjectCommandsSaveWorkspaceTemplateReference(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"product", "product-v2"} {
		if _, _, err := executeCollectingInput(t, options, nil, "templates", "create", name); err != nil {
			t.Fatalf("create Template %s: %v", name, err)
		}
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"projects", "create", "change-monitor", "--template", "product", "--output", "json"); err != nil {
		t.Fatalf("projects create --template: %v", err)
	}
	show, _, err := executeCollectingInput(t, options, nil,
		"projects", "get", "change-monitor", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, `"templateName":"product"`) {
		t.Fatalf("projects show = %s", show)
	}

	dry, _, err := executeCollectingInput(t, options, nil,
		"projects", "set", "change-monitor", "--template", "product-v2", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("projects set --dry-run: %v", err)
	}
	if mutation := decodeTicketMutation(t, dry); mutation.Status != "valid" || mutation.Operation != "projects.set" {
		t.Fatalf("projects set dry-run = %s", dry)
	}
	if content := readTicketFile(t, filepath.Join(home, "change-monitor", "index.md")); strings.Contains(content, "product-v2") {
		t.Fatal("projects set --dry-run changed index.md")
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"projects", "set", "change-monitor", "--template", "product-v2", "--output", "json"); err != nil {
		t.Fatalf("projects set: %v", err)
	}
	show, _, err = executeCollectingInput(t, options, nil,
		"projects", "get", "change-monitor", "--output", "json")
	if err != nil || !strings.Contains(show, `"templateName":"product-v2"`) {
		t.Fatalf("projects show after set = %s, %v", show, err)
	}
}

func TestProjectsCreateValidatesTemplateBeforeWriting(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, nil,
		"projects", "create", "change-monitor", "--template", "missing", "--output", "json")
	if clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("projects create missing Template error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "change-monitor")); !os.IsNotExist(statErr) {
		t.Fatalf("projects create wrote the Project before Template validation: %v", statErr)
	}
}

func TestTemplateRemovalRejectsAProjectReference(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "templates", "create", "product"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil,
		"projects", "create", "change-monitor", "--template", "product"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, nil,
		"templates", "remove", "product", "--output", "json")
	if clierr.CodeOf(err) != clierr.PreconditionFailed || !strings.Contains(err.Error(), "Project") {
		t.Fatalf("templates remove error = %v", err)
	}
}

func TestSchemaListsTicketCommandsAndApplyOperations(t *testing.T) {
	output, err := execute(t, t.TempDir(), "schema")
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		`"twt tickets init"`, `"twt tickets home"`, `"twt tickets create"`, `"twt tickets list"`, `"twt tickets get"`,
		`"twt tickets set"`, `"twt tickets claim"`, `"twt tickets unclaim"`,
		`"twt tickets close"`, `"twt tickets comment"`, `"twt tickets queue"`, `"twt tickets dispatch"`,
		`"twt tickets doctor"`, `"twt tickets repair"`,
		`"twt projects create"`, `"twt projects remove"`,
		`"twt projects list"`, `"twt projects get"`,
	} {
		if !strings.Contains(output, command) {
			t.Fatalf("schema misses %s", command)
		}
	}
	for _, operation := range []string{
		`"tickets.create"`, `"tickets.set"`, `"tickets.claim"`, `"tickets.unclaim"`,
		`"tickets.close"`, `"tickets.comment"`, `"tickets.dispatch"`,
		`"tickets.repair"`, `"projects.create"`, `"projects.remove"`,
	} {
		if !strings.Contains(output, operation) {
			t.Fatalf("schema misses the apply operation %s", operation)
		}
	}
}

func TestApplySupportsTicketsRepair(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "create", "old shipped work"); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(home, "old-shipped-work.md")
	content := strings.Replace(readTicketFile(t, active), "status: needs-triage", "status: done", 1)
	if err := os.WriteFile(active, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	dry, _, err := executeCollectingInput(t, options, strings.NewReader(`{"operation":"tickets.repair"}`),
		"apply", "-", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatalf("apply tickets.repair dry run: %v", err)
	}
	if !strings.Contains(dry, `"status":"valid"`) {
		t.Fatalf("apply tickets.repair dry run = %s", dry)
	}

	applied, _, err := executeCollectingInput(t, options, strings.NewReader(`{"operation":"tickets.repair"}`),
		"apply", "-", "--output", "json")
	if err != nil {
		t.Fatalf("apply tickets.repair: %v", err)
	}
	if !strings.Contains(applied, `"status":"applied"`) || !strings.Contains(applied, `"movedCount":1`) {
		t.Fatalf("apply tickets.repair = %s", applied)
	}

	_, _, err = executeCollectingInput(t, options, strings.NewReader(`{"operation":"tickets.repair","ticket":{}}`),
		"apply", "-", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), "accepts no payload") {
		t.Fatalf("apply tickets.repair with a payload = %v", err)
	}
}

func TestApplySupportsTicketOperations(t *testing.T) {
	options, home := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"projects.create","project":{"name":"change-monitor"}}`),
		"apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.create","ticket":{"title":"apply work","project":"change-monitor","status":"ready-for-agent","priority":1,"body":"From apply."}}`),
		"apply", "-", "--output", "json")
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
		"apply", "-", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), "ticket.as") {
		t.Fatalf("apply claim without as = %v", err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.claim","ticket":{"reference":"apply-work","as":"apply-agent"}}`),
		"apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.comment","ticket":{"reference":"apply-work","text":"Done."}}`),
		"apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.set","ticket":{"reference":"apply-work","status":"done"}}`),
		"apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.unclaim","ticket":{"reference":"apply-work","as":"apply-agent"}}`),
		"apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	content = readTicketFile(t, filepath.Join(home, "closed", "change-monitor", "apply-work.md"))
	if !strings.Contains(content, "status: done") || !strings.Contains(content, "Done.") || strings.Contains(content, "claimed_by: apply-agent") {
		t.Fatalf("apply mutations result:\n%s", content)
	}

	// tickets.close resolves in one operation, and it needs ticket.as.
	if _, _, err := executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.create","ticket":{"title":"close via apply","status":"ready-for-agent"}}`),
		"apply", "-", "--output", "json"); err != nil {
		t.Fatal(err)
	}
	_, _, err = executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.close","ticket":{"reference":"close-via-apply"}}`),
		"apply", "-", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), "ticket.as") {
		t.Fatalf("apply close without as = %v", err)
	}
	stdout, _, err = executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.close","ticket":{"reference":"close-via-apply","as":"apply-agent"}}`),
		"apply", "-", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if closed := decodeTicketMutation(t, stdout); closed.Operation != "tickets.close" ||
		closed.Status != "applied" || closed.ID != "close-via-apply" {
		t.Fatalf("apply tickets.close = %+v\n%s", closed, stdout)
	}
	closedContent := readTicketFile(t, filepath.Join(home, "closed", "close-via-apply.md"))
	if !strings.Contains(closedContent, "status: done") || strings.Contains(closedContent, "claimed_by: apply-agent") {
		t.Fatalf("apply close result:\n%s", closedContent)
	}

	// Strict payloads reject fields from other operations.
	_, _, err = executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"tickets.set","ticket":{"reference":"apply-work","title":"nope"}}`),
		"apply", "-", "--output", "json")
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
	if !strings.Contains(stdout, "tickets-home") || !strings.Contains(stdout, "No Tickets home is set") {
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
	if !strings.Contains(stdout, "tickets-home") || !strings.Contains(stdout, home) {
		t.Fatalf("doctor output = %s", stdout)
	}

	missing := options
	missing.TicketsHome = filepath.Join(home, "not-created")
	stdout, _, err = executeCollectingInput(t, missing, nil, "doctor")
	if err != nil {
		t.Fatalf("doctor with a missing home = %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, "tickets-home") || !strings.Contains(stdout, "does not exist") {
		t.Fatalf("doctor output = %s", stdout)
	}
}
