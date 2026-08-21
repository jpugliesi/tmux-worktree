package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// forceTextOutput puts "--output text" in the first position when the args do
// not select an output format. The test harness runs without a terminal, so
// twt would otherwise use the json default. Tests for the json default use
// executeRaw instead.
func forceTextOutput(args []string) []string {
	for _, arg := range args {
		if arg == "--output" || strings.HasPrefix(arg, "--output=") {
			return args
		}
	}
	return append([]string{"--output", "text"}, args...)
}

// executeRaw runs twt without the injected --output value, so the non-terminal
// json default applies. It returns standard output, standard error, and the
// command error.
func executeRaw(t *testing.T, options cli.Options, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), stderr.String(), err
}

func outputTestOptions(t *testing.T) cli.Options {
	t.Helper()
	root := t.TempDir()
	return cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}
}

// seedOutputProjects saves count active Projects named project-0..count-1,
// newest last, so the display order is project-(count-1) first.
func seedOutputProjects(t *testing.T, options cli.Options, count int) {
	t.Helper()
	base := time.Now().UTC().Add(-time.Duration(count) * time.Hour)
	for index := 0; index < count; index++ {
		project := domain.Project{
			Version: domain.ProjectVersion, ID: fmt.Sprintf("project-%d-id", index),
			Name: fmt.Sprintf("project-%d", index), TemplateName: "example",
			Status: domain.ProjectActive, CreatedAt: base.Add(time.Duration(index) * time.Hour),
			UpdatedAt: base.Add(time.Duration(index) * time.Hour),
		}
		if err := store.NewProjectStore(options.StateDir).Save(project); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOutputDefaultsToJSONWithoutTerminal(t *testing.T) {
	options := outputTestOptions(t)

	// A pipe or buffer is not a terminal: the default output is json.
	stdout, _, err := executeRaw(t, options, "templates", "list")
	if err != nil {
		t.Fatalf("templates list without --output: %v", err)
	}
	var envelope struct {
		SchemaVersion int `json:"schemaVersion"`
		TotalCount    int `json:"totalCount"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("templates list without --output is not JSON: %v\n%s", err, stdout)
	}
	if envelope.SchemaVersion != 1 {
		t.Fatalf("templates list without --output = %s", stdout)
	}

	// An explicit --output text always wins.
	textOut, textErr, err := executeRaw(t, options, "templates", "list", "--output", "text")
	if err != nil {
		t.Fatalf("templates list --output text: %v", err)
	}
	if strings.Contains(textOut, "schemaVersion") || !strings.Contains(textErr, "No Project Templates exist") {
		t.Fatalf("templates list --output text stdout=%q stderr=%q", textOut, textErr)
	}

	// An interactive command treats the implicit json default like an
	// explicit --output json.
	_, _, err = executeRaw(t, options, "new", "some-project")
	if err == nil || !strings.Contains(err.Error(), "use 'twt projects create' for JSON automation") {
		t.Fatalf("quick create without a terminal = %v", err)
	}
}

func TestNDJSONListsOneObjectPerLine(t *testing.T) {
	options := outputTestOptions(t)
	seedOutputProjects(t, options, 2)

	stdout, _, err := executeRaw(t, options, "projects", "list", "--output", "ndjson", "--limit", "1")
	if err != nil {
		t.Fatalf("projects list --output ndjson: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("ndjson line count = %d\n%s", len(lines), stdout)
	}
	var element struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &element); err != nil || element.Name != "project-1" {
		t.Fatalf("ndjson element = %q (error %v)", lines[0], err)
	}
	if strings.Contains(lines[0], "schemaVersion") {
		t.Fatalf("ndjson element carries an envelope: %q", lines[0])
	}
	var summary struct {
		TotalCount int  `json:"totalCount"`
		Truncated  bool `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &summary); err != nil || summary.TotalCount != 2 || !summary.Truncated {
		t.Fatalf("ndjson summary = %q (error %v)", lines[1], err)
	}

	// Commands that are not list commands refuse ndjson.
	_, _, err = executeRaw(t, options, "projects", "show", "project-0", "--output", "ndjson")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(err.Error(), "ndjson") {
		t.Fatalf("projects show --output ndjson = %v", err)
	}
}

func TestFieldsMasksReadOutputs(t *testing.T) {
	options := outputTestOptions(t)
	seedOutputProjects(t, options, 2)

	show, _, err := executeRaw(t, options, "projects", "show", "project-0", "--output", "json", "--fields", "id,name")
	if err != nil {
		t.Fatalf("projects show --fields: %v", err)
	}
	var shown struct {
		SchemaVersion int                        `json:"schemaVersion"`
		Project       map[string]json.RawMessage `json:"project"`
	}
	if err := json.Unmarshal([]byte(show), &shown); err != nil {
		t.Fatalf("decode projects show --fields: %v\n%s", err, show)
	}
	if shown.SchemaVersion != 1 || len(shown.Project) != 2 || shown.Project["id"] == nil || shown.Project["name"] == nil {
		t.Fatalf("projects show --fields id,name = %s", show)
	}

	list, _, err := executeRaw(t, options, "projects", "list", "--output", "json", "--fields", "name")
	if err != nil {
		t.Fatalf("projects list --fields: %v", err)
	}
	var listed struct {
		Projects   []map[string]json.RawMessage `json:"projects"`
		TotalCount int                          `json:"totalCount"`
	}
	if err := json.Unmarshal([]byte(list), &listed); err != nil {
		t.Fatalf("decode projects list --fields: %v\n%s", err, list)
	}
	if listed.TotalCount != 2 || len(listed.Projects) != 2 || len(listed.Projects[0]) != 1 || listed.Projects[0]["name"] == nil {
		t.Fatalf("projects list --fields name = %s", list)
	}

	ndjson, _, err := executeRaw(t, options, "projects", "list", "--output", "ndjson", "--fields", "id")
	if err != nil {
		t.Fatalf("projects list ndjson --fields: %v", err)
	}
	firstLine := strings.Split(strings.TrimSpace(ndjson), "\n")[0]
	var masked map[string]json.RawMessage
	if err := json.Unmarshal([]byte(firstLine), &masked); err != nil || len(masked) != 1 || masked["id"] == nil {
		t.Fatalf("ndjson --fields id element = %q (error %v)", firstLine, err)
	}

	// An unknown field name reports the valid names.
	_, _, err = executeRaw(t, options, "projects", "show", "project-0", "--output", "json", "--fields", "bogus")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(err.Error(), "valid fields") || !strings.Contains(err.Error(), "name") {
		t.Fatalf("projects show --fields bogus = %v", err)
	}

	// Text output does not support --fields.
	_, _, err = executeRaw(t, options, "projects", "show", "project-0", "--output", "text", "--fields", "id")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage || !strings.Contains(err.Error(), "use --fields with --output json") {
		t.Fatalf("projects show --output text --fields = %v", err)
	}
}

func TestOffsetWindowsListResults(t *testing.T) {
	options := outputTestOptions(t)
	seedOutputProjects(t, options, 3)
	type listResult struct {
		Projects []struct {
			Name string `json:"name"`
		} `json:"projects"`
		TotalCount int  `json:"totalCount"`
		Truncated  bool `json:"truncated"`
	}
	decode := func(text string) listResult {
		t.Helper()
		var result listResult
		if err := json.Unmarshal([]byte(text), &result); err != nil {
			t.Fatalf("decode projects list: %v\n%s", err, text)
		}
		return result
	}

	window, _, err := executeRaw(t, options, "projects", "list", "--output", "json", "--offset", "1", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	result := decode(window)
	if len(result.Projects) != 1 || result.Projects[0].Name != "project-1" || result.TotalCount != 3 || !result.Truncated {
		t.Fatalf("projects list --offset 1 --limit 1 = %s", window)
	}

	tail, _, err := executeRaw(t, options, "projects", "list", "--output", "json", "--offset", "2")
	if err != nil {
		t.Fatal(err)
	}
	result = decode(tail)
	if len(result.Projects) != 1 || result.Projects[0].Name != "project-0" || result.TotalCount != 3 || result.Truncated {
		t.Fatalf("projects list --offset 2 = %s", tail)
	}

	beyond, _, err := executeRaw(t, options, "projects", "list", "--output", "json", "--offset", "9")
	if err != nil {
		t.Fatal(err)
	}
	result = decode(beyond)
	if len(result.Projects) != 0 || result.TotalCount != 3 || result.Truncated {
		t.Fatalf("projects list --offset 9 = %s", beyond)
	}

	_, _, err = executeRaw(t, options, "projects", "list", "--output", "json", "--offset", "-1")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("projects list --offset -1 = %v", err)
	}
}
