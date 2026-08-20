package cli_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

// switchFixture saves one archived and two active Projects and returns the
// base options.
func switchFixture(t *testing.T) cli.Options {
	t.Helper()
	root := t.TempDir()
	options := cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}
	now := time.Now().UTC()
	archivedAt := now.Add(-5 * time.Hour)
	projects := []domain.Project{
		{Version: domain.ProjectVersion, ID: "old-active-id", Name: "old-active", TemplateName: "example", Status: domain.ProjectActive, TmuxSession: "old-active", CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now},
		{Version: domain.ProjectVersion, ID: "new-active-id", Name: "new-active", TemplateName: "example", Status: domain.ProjectActive, TmuxSession: "new-active", CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
		{Version: domain.ProjectVersion, ID: "sleepy-id", Name: "sleepy", TemplateName: "example", Status: domain.ProjectArchived, TmuxSession: "sleepy", CreatedAt: now.Add(-72 * time.Hour), UpdatedAt: now, ArchivedAt: &archivedAt},
	}
	projectStore := store.NewProjectStore(options.StateDir)
	for _, project := range projects {
		if err := projectStore.Save(project); err != nil {
			t.Fatal(err)
		}
	}
	return options
}

func TestSwitchRefusesJSONOutput(t *testing.T) {
	options := switchFixture(t)
	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetArgs([]string{"switch", "new-active", "--output", "json"})
	err := command.Execute()
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(err.Error(), "interactive") {
		t.Fatalf("switch with JSON output = %v (code %q)", err, clierr.CodeOf(err))
	}
}

func TestSwitchDryRunReportsThePlanWithoutAChange(t *testing.T) {
	options := switchFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT2_PROJECT_ID", "")

	output := executeWithOptions(t, options, nil, "switch", "new-active", "--dry-run")
	if !strings.Contains(output, `switch the client to session "new-active"`) {
		t.Fatalf("switch dry-run output = %q", output)
	}

	archivedOutput := executeWithOptions(t, options, nil, "switch", "sleepy", "--dry-run")
	if !strings.Contains(archivedOutput, `open archived Project "sleepy"`) {
		t.Fatalf("switch dry-run for an archived Project = %q", archivedOutput)
	}
	unchanged, err := store.NewProjectStore(options.StateDir).Find("sleepy-id")
	if err != nil || unchanged.Status != domain.ProjectArchived {
		t.Fatalf("switch dry-run changed the archived Project: status=%q error=%v", unchanged.Status, err)
	}

	if _, _, err := executeCollectingOutput(t, options, "switch", "missing", "--dry-run"); err == nil || clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("switch for an unknown Project = %v", err)
	}
}

func TestSwitchPickerSortsActiveProjectsFirst(t *testing.T) {
	options := switchFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT2_PROJECT_ID", "")
	var pickedLines []string
	options.SwitchPick = func(_ *cobra.Command, lines []string) (int, error) {
		pickedLines = append([]string(nil), lines...)
		return 1, nil
	}
	output := executeWithOptions(t, options, nil, "switch", "--dry-run")
	if len(pickedLines) != 3 {
		t.Fatalf("switch picker lines = %v", pickedLines)
	}
	wantPrefixes := []string{"new-active\texample\tactive\t", "old-active\texample\tactive\t", "sleepy\texample\tarchived\t"}
	for index, want := range wantPrefixes {
		if !strings.HasPrefix(pickedLines[index], want) {
			t.Fatalf("switch picker line %d = %q, want prefix %q", index, pickedLines[index], want)
		}
	}
	if !strings.Contains(output, `switch the client to session "old-active"`) {
		t.Fatalf("switch picker dry-run output = %q", output)
	}
}

func TestSwitchNumberedPickerReadsTheProjectNumber(t *testing.T) {
	options := switchFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT2_PROJECT_ID", "")
	// An empty PATH hides fzf, so the real picker uses the numbered list.
	t.Setenv("PATH", "")
	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetIn(strings.NewReader("3\n"))
	command.SetArgs([]string{"switch", "--dry-run"})
	if err := command.Execute(); err != nil {
		t.Fatalf("switch with the numbered picker: %v", err)
	}
	if !strings.Contains(stderr.String(), "1) new-active") || !strings.Contains(stderr.String(), "Project number: ") {
		t.Fatalf("numbered picker prompt = %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), `open archived Project "sleepy"`) {
		t.Fatalf("numbered picker dry-run output = %q", stdout.String())
	}

	var badStdout, badStderr bytes.Buffer
	badOptions := options
	badOptions.Stdout, badOptions.Stderr = &badStdout, &badStderr
	badCommand := cli.New(badOptions)
	badCommand.SetIn(strings.NewReader("9\n"))
	badCommand.SetArgs([]string{"switch", "--dry-run"})
	err := badCommand.Execute()
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(err.Error(), "between 1 and 3") {
		t.Fatalf("numbered picker with an invalid number = %v", err)
	}
}
