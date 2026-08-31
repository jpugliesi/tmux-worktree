package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

// projectCreateWizardOptions seeds an interactive projects create: the editor
// is injected so tests never start VISUAL.
func projectCreateWizardOptions(t *testing.T, plan string) (cli.Options, string) {
	t.Helper()
	options, home := ticketTestOptions(t)
	options.OpenEditor = func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(data)) != "" {
			return os.ErrInvalid
		}
		return os.WriteFile(path, []byte(plan), 0o644)
	}
	return options, home
}

func TestProjectsCreateWithoutNameNeedsATerminal(t *testing.T) {
	options, _ := ticketTestOptions(t)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, nil, "projects", "create")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || clierr.ExitCode(err) != 2 {
		t.Fatalf("projects create without NAME = %v (code %q, exit %d)", err, clierr.CodeOf(err), clierr.ExitCode(err))
	}
	if !strings.Contains(err.Error(), "missing Project name") {
		t.Fatalf("projects create without NAME = %v", err)
	}
}

func TestProjectsCreateWithoutNameRefusesJSON(t *testing.T) {
	options, home := projectCreateWizardOptions(t, "# plan\n")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := executeCollectingInput(t, options, strings.NewReader("ignored-name\nn\n"),
		"projects", "create", "--output", "json")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("json projects create without NAME = %v", err)
	}
	if !strings.Contains(err.Error(), "missing Project name") {
		t.Fatalf("json projects create without NAME = %v", err)
	}
	if strings.Contains(stderr, "Project name:") {
		t.Fatalf("json projects create prompted: %q", stderr)
	}
	if _, statErr := os.Stat(filepath.Join(home, "ignored-name")); !os.IsNotExist(statErr) {
		t.Fatalf("json projects create wrote a Project: %v", statErr)
	}
}

func TestProjectsCreateWizardWritesPlanAndSkipsWorkspace(t *testing.T) {
	options, home := projectCreateWizardOptions(t, "# change-monitor Plan\n\nShip the VFS reconnect.\n")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("change-monitor\nn\n"),
		"projects", "create")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "Project name: ") {
		t.Fatalf("wizard stderr = %q", stderr)
	}
	if !strings.Contains(stderr, "Start a new Workspace? [Y/n] ") {
		t.Fatalf("wizard did not ask about a Workspace: %q", stderr)
	}
	if strings.Contains(stderr, "Workspace name") {
		t.Fatalf("declined Workspace still asked for a name: %q", stderr)
	}
	if !strings.Contains(stdout, `Created Project "change-monitor"`) {
		t.Fatalf("wizard stdout = %q", stdout)
	}
	if !strings.Contains(stdout, `Wrote the plan of Project "change-monitor"`) {
		t.Fatalf("wizard did not write the plan: %q", stdout)
	}
	if _, statErr := os.Stat(filepath.Join(home, "change-monitor", "index.md")); statErr != nil {
		t.Fatalf("wizard did not scaffold index.md: %v", statErr)
	}
	content := readTicketFile(t, filepath.Join(home, "change-monitor", "plan.md"))
	if !strings.Contains(content, "Ship the VFS reconnect.") {
		t.Fatalf("wizard plan.md = %s", content)
	}
}

func TestProjectsCreateWizardRejectsAnExistingProject(t *testing.T) {
	options, home := projectCreateWizardOptions(t, "# overwrite\n")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "projects", "create", "change-monitor"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "change-monitor", "plan.md"), []byte("# keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, strings.NewReader("change-monitor\n"), "projects", "create")
	if err == nil || clierr.CodeOf(err) != clierr.AlreadyExists {
		t.Fatalf("existing Project = %v (code %q)", err, clierr.CodeOf(err))
	}
	if !strings.Contains(clierr.HintOf(err), "projects plan") {
		t.Fatalf("existing Project hint = %q", clierr.HintOf(err))
	}
	if readTicketFile(t, filepath.Join(home, "change-monitor", "plan.md")) != "# keep me\n" {
		t.Fatal("wizard overwrote an existing plan")
	}
}

func TestProjectsCreateWizardEmptyNameCancels(t *testing.T) {
	options, home := projectCreateWizardOptions(t, "# plan\n")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, strings.NewReader("\n"), "projects", "create")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("empty name = %v (code %q)", err, clierr.CodeOf(err))
	}
	if _, statErr := os.Stat(filepath.Join(home, "change-monitor")); !os.IsNotExist(statErr) {
		t.Fatalf("empty name wrote a Project: %v", statErr)
	}
}

func TestProjectsCreateWizardEmptyEditorCancels(t *testing.T) {
	options, home := projectCreateWizardOptions(t, "")
	options.OpenEditor = func(path string) error {
		return os.WriteFile(path, []byte("  \n"), 0o644)
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, _, err := executeCollectingInput(t, options, strings.NewReader("change-monitor\n"), "projects", "create")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("empty editor = %v (code %q)", err, clierr.CodeOf(err))
	}
	if _, statErr := os.Stat(filepath.Join(home, "change-monitor")); !os.IsNotExist(statErr) {
		t.Fatalf("empty editor wrote a Project: %v", statErr)
	}
}

func TestProjectsCreateWizardDryRunWritesNothing(t *testing.T) {
	options, home := projectCreateWizardOptions(t, "# change-monitor Plan\n\nDry plan.\n")
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	before := homeFiles(t, home)
	stdout, _, err := executeCollectingInput(t, options, strings.NewReader("change-monitor\nn\n"),
		"projects", "create", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "projects.create: valid") {
		t.Fatalf("dry-run stdout = %q", stdout)
	}
	if !strings.Contains(stdout, "projects.plan: valid") {
		t.Fatalf("dry-run did not preview the plan write: %q", stdout)
	}
	after := homeFiles(t, home)
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Fatalf("dry-run changed the Tickets home:\nbefore=%v\nafter=%v", before, after)
	}
}

func TestProjectsCreateNamedArgsSkipTheWizard(t *testing.T) {
	called := false
	options, home := ticketTestOptions(t)
	options.OpenEditor = func(string) error {
		called = true
		return nil
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("should-not-prompt\n"),
		"projects", "create", "change-monitor")
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("NAME opened the plan editor")
	}
	if strings.Contains(stderr, "Project name:") || strings.Contains(stderr, "Start a new Workspace?") {
		t.Fatalf("NAME entered the wizard: %q", stderr)
	}
	if !strings.Contains(stdout, `Created Project "change-monitor"`) {
		t.Fatalf("named create stdout = %q", stdout)
	}
	if _, statErr := os.Stat(filepath.Join(home, "change-monitor", "plan.md")); !os.IsNotExist(statErr) {
		t.Fatalf("named create wrote plan.md: %v", statErr)
	}
}

func TestProjectsCreateWizardStartsAWorkspaceOnConfirm(t *testing.T) {
	options, home := projectCreateWizardOptions(t, "# change-monitor Plan\n\nPlan body.\n")
	writeCreateNameTemplate(t, options.ConfigDir)
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("change-monitor\ny\n\n"),
		"projects", "create", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "Start a new Workspace? [Y/n] ") {
		t.Fatalf("workspace confirm stderr = %q", stderr)
	}
	if !strings.Contains(stderr, "Workspace name [change-monitor]: ") {
		t.Fatalf("workspace name prompt = %q", stderr)
	}
	if !strings.Contains(stdout, "projects.create: valid") || !strings.Contains(stdout, "workspaces.create: valid") {
		t.Fatalf("workspace dry-run stdout = %q", stdout)
	}
	if _, findErr := store.NewWorkspaceStore(options.StateDir).Find("change-monitor"); findErr == nil {
		t.Fatal("workspace dry-run created a Workspace")
	}
	if _, statErr := os.Stat(filepath.Join(home, "change-monitor")); !os.IsNotExist(statErr) {
		t.Fatalf("workspace dry-run wrote a Project: %v", statErr)
	}
}

func TestProjectsCreateWizardPicksATemplateWhenSeveralExist(t *testing.T) {
	options, home := projectCreateWizardOptions(t, "# change-monitor Plan\n\nPlan body.\n")
	writeCreateNameTemplate(t, options.ConfigDir)
	writeNamedTemplate(t, options.ConfigDir, "zeta")
	picked := []string{}
	options.PickTemplate = func(_ *cobra.Command, lines []string) (int, error) {
		picked = append(picked, strings.Join(lines, ","))
		for index, line := range lines {
			if line == "zeta" {
				return index, nil
			}
		}
		return 0, nil
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader("change-monitor\nn\n"),
		"projects", "create")
	if err != nil {
		t.Fatal(err)
	}
	if len(picked) != 1 || !strings.Contains(picked[0], "example") || !strings.Contains(picked[0], "zeta") {
		t.Fatalf("picker lines = %v", picked)
	}
	if !strings.Contains(stderr, "Template: zeta (selected)") {
		t.Fatalf("wizard stderr = %q", stderr)
	}
	if !strings.Contains(stdout, `Created Project "change-monitor"`) {
		t.Fatalf("wizard stdout = %q", stdout)
	}
	shown := executeWithOptions(t, options, nil, "projects", "get", "change-monitor", "--output", "json")
	if !strings.Contains(shown, `"templateName":"zeta"`) {
		t.Fatalf("Project Template after picker = %s", shown)
	}
	if _, statErr := os.Stat(filepath.Join(home, "change-monitor", "index.md")); statErr != nil {
		t.Fatal(statErr)
	}
}

func TestProjectsCreateWizardTemplateFlagSkipsThePicker(t *testing.T) {
	options, _ := projectCreateWizardOptions(t, "# change-monitor Plan\n\nPlan body.\n")
	writeCreateNameTemplate(t, options.ConfigDir)
	writeNamedTemplate(t, options.ConfigDir, "zeta")
	options.PickTemplate = func(_ *cobra.Command, _ []string) (int, error) {
		t.Fatal(" --template still opened the picker")
		return 0, nil
	}
	if _, _, err := executeCollectingInput(t, options, nil, "tickets", "init"); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := executeCollectingInput(t, options, strings.NewReader("change-monitor\nn\n"),
		"projects", "create", "--template", "example")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "Template:") {
		t.Fatalf("flag still printed an inferred Template: %q", stderr)
	}
	shown := executeWithOptions(t, options, nil, "projects", "get", "change-monitor", "--output", "json")
	if !strings.Contains(shown, `"templateName":"example"`) {
		t.Fatalf("Project Template after --template = %s", shown)
	}
}

func TestProjectsCreateUseMarksNameOptional(t *testing.T) {
	root := cli.New(cli.Options{ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir()})
	command := findCommand(root, "projects", "create")
	if command == nil {
		t.Fatal("missing projects create")
	}
	if command.Use != "create [NAME]" {
		t.Fatalf("projects create Use = %q", command.Use)
	}
}
