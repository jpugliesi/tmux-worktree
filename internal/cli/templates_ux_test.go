package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// writeEditorScript writes a fake editor that replaces the file content with
// the value of TWT_TEST_EDIT_CONTENT.
func writeEditorScript(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "fake-editor.sh")
	script := "#!/bin/sh\nprintf '%s' \"$TWT_TEST_EDIT_CONTENT\" > \"$1\"\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTemplatesPathPrintsTheYAMLFile(t *testing.T) {
	root := t.TempDir()
	if _, err := execute(t, root, "templates", "create", "example"); err != nil {
		t.Fatal(err)
	}
	output, err := execute(t, root, "templates", "path", "example")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "config", "templates", "example.yaml") + "\n"
	if output != want {
		t.Fatalf("templates path = %q, want %q", output, want)
	}
	if _, err := execute(t, root, "templates", "path", "missing"); err == nil || clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("templates path for a missing Workspace Template = %v", err)
	}
}

func TestTemplatesRemoveRefusesAUsedWorkspaceTemplate(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "state")
	if _, err := execute(t, root, "templates", "create", "example"); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-uses-template", Name: "fix-auth",
		TemplateName: "example", Status: domain.WorkspaceActive,
	}
	if err := store.NewWorkspaceStore(stateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	_, err := execute(t, root, "templates", "remove", "example")
	if err == nil || clierr.CodeOf(err) != clierr.PreconditionFailed || !strings.Contains(err.Error(), "fix-auth") {
		t.Fatalf("templates remove with a Workspace = %v (code %q)", err, clierr.CodeOf(err))
	}
	if clierr.HintOf(err) == "" {
		t.Fatal("templates remove refusal has no hint")
	}
	if _, statErr := os.Stat(filepath.Join(root, "config", "templates", "example.yaml")); statErr != nil {
		t.Fatalf("refused removal deleted the file: %v", statErr)
	}

	if err := store.NewWorkspaceStore(stateDir).Delete(workspace.ID); err != nil {
		t.Fatal(err)
	}
	dryRun, err := execute(t, root, "templates", "remove", "example", "--dry-run", "--output", "json")
	if err != nil || !strings.Contains(dryRun, `"status":"valid"`) {
		t.Fatalf("templates remove dry run = %q, error = %v", dryRun, err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "config", "templates", "example.yaml")); statErr != nil {
		t.Fatalf("dry run deleted the file: %v", statErr)
	}
	if _, err := execute(t, root, "templates", "remove", "example"); err != nil {
		t.Fatalf("templates remove = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "config", "templates", "example.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("templates remove kept the file: %v", statErr)
	}
}

func TestTemplatesEditValidatesTheResult(t *testing.T) {
	root := t.TempDir()
	if _, err := execute(t, root, "templates", "create", "example"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config", "templates", "example.yaml")

	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if _, err := execute(t, root, "templates", "edit", "example"); err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("templates edit without an editor = %v (code %q)", err, clierr.CodeOf(err))
	}

	editor := writeEditorScript(t, root)
	t.Setenv("EDITOR", editor)
	t.Setenv("TWT_TEST_EDIT_CONTENT", "version: 1\nname: example\nrepositories: []\ntypo: true\n")
	_, err := execute(t, root, "templates", "edit", "example")
	if err == nil || clierr.CodeOf(err) != clierr.UnsafeState {
		t.Fatalf("templates edit with an invalid result = %v (code %q)", err, clierr.CodeOf(err))
	}
	if hint := clierr.HintOf(err); !strings.Contains(hint, "twt templates validate example") {
		t.Fatalf("templates edit hint = %q", hint)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || !strings.Contains(string(data), "typo: true") {
		t.Fatalf("templates edit did not keep the invalid file: %s, error = %v", data, readErr)
	}

	t.Setenv("TWT_TEST_EDIT_CONTENT", "version: 1\nname: example\nrepositories:\n  - name: app\n    clone:\n      url: https://example.com/app.git\n")
	output, err := execute(t, root, "templates", "edit", "example")
	if err != nil || !strings.Contains(output, "is valid") {
		t.Fatalf("templates edit with a valid result = %q, error = %v", output, err)
	}
}

func TestTemplatesCreateReadsAFileOrStandardInput(t *testing.T) {
	root := t.TempDir()
	document := "version: 1\nname: fromfile\nrepositories:\n  - name: app\n    clone:\n      url: https://example.com/app.git\n"
	documentPath := filepath.Join(root, "template.yaml")
	if err := os.WriteFile(documentPath, []byte(document), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, root, "templates", "create", "fromfile", "--from-file", documentPath); err != nil {
		t.Fatalf("templates create --from-file = %v", err)
	}
	saved, err := os.ReadFile(filepath.Join(root, "config", "templates", "fromfile.yaml"))
	if err != nil || !strings.Contains(string(saved), "url: https://example.com/app.git") {
		t.Fatalf("saved Workspace Template = %s, error = %v", saved, err)
	}

	if _, err := execute(t, root, "templates", "create", "other", "--from-file", documentPath); err == nil ||
		!strings.Contains(err.Error(), "contains name") {
		t.Fatalf("templates create with a name that does not match = %v", err)
	}

	options := cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data")}
	stdinOutput := executeWithOptions(t, options, strings.NewReader("version: 1\nrepositories: []\n"), "templates", "create", "fromstdin")
	if !strings.Contains(stdinOutput, "fromstdin") {
		t.Fatalf("templates create --from-stdin needs the flag: %q", stdinOutput)
	}
}

func TestTemplatesCreateFromStdinRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	options := cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data")}
	_, _, err := executeCollectingInput(t, options, strings.NewReader("version: 1\nname: strict\ntypo: true\n"),
		"templates", "create", "strict", "--from-stdin")
	if err == nil || !strings.Contains(err.Error(), "field typo not found") {
		t.Fatalf("templates create --from-stdin with an unknown field = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "config", "templates", "strict.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("invalid input created a Workspace Template: %v", statErr)
	}
	if _, _, err := executeCollectingInput(t, options, strings.NewReader("version: 1\nrepositories: []\n"),
		"templates", "create", "both", "--from-stdin", "--from-file", "/tmp/does-not-matter.yaml"); err == nil ||
		clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("templates create with both input flags = %v", err)
	}
}

func TestTemplatesValidateReportsWarningsWithoutFailing(t *testing.T) {
	root := t.TempDir()
	if _, err := execute(t, root, "templates", "create", "empty"); err != nil {
		t.Fatal(err)
	}
	text, err := execute(t, root, "templates", "validate", "empty")
	if err != nil {
		t.Fatalf("templates validate = %v", err)
	}
	if !strings.Contains(text, "is valid") || !strings.Contains(text, "Warning: The Workspace Template has no repositories.") {
		t.Fatalf("templates validate text = %q", text)
	}
	encoded, err := execute(t, root, "templates", "validate", "empty", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Status   string   `json:"status"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		t.Fatalf("decode validate JSON: %v\n%s", err, encoded)
	}
	if result.Status != "valid" || len(result.Warnings) != 1 {
		t.Fatalf("templates validate JSON = %+v", result)
	}

	if _, err := execute(t, root, "templates", "repos", "add", "empty", "app", "https://example.com/app.git"); err != nil {
		t.Fatal(err)
	}
	encoded, err = execute(t, root, "templates", "validate", "empty", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("Workspace Template with one repository has warnings: %+v", result.Warnings)
	}
}

func TestTemplatesInitSetHandlesBothModes(t *testing.T) {
	root := t.TempDir()
	if _, err := execute(t, root, "templates", "create", "product"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, root, "templates", "repos", "add", "product", "web", "https://example.com/web.git"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, root, "templates", "init", "set", "product", "--repo", "web", "--", "./init.sh"); err != nil {
		t.Fatalf("repository initialization: %v", err)
	}
	if _, err := execute(t, root, "templates", "init", "set", "product", "--cwd", "web", "--", "./scripts/init-workspace.sh"); err != nil {
		t.Fatalf("Workspace initialization: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "config", "templates", "product.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- ./init.sh", "- ./scripts/init-workspace.sh", "working_directory: web"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("Workspace Template YAML does not contain %q:\n%s", want, data)
		}
	}

	_, err = execute(t, root, "templates", "init", "set", "product", "--repo", "web", "--cwd", "web", "--", "./init.sh")
	if err == nil || !strings.Contains(err.Error(), "do not use --cwd together with --repo") {
		t.Fatalf("--repo with --cwd = %v", err)
	}
	_, err = execute(t, root, "templates", "init", "set", "product", "--", "./init.sh")
	if err == nil || !strings.Contains(err.Error(), "--cwd") {
		t.Fatalf("Workspace initialization without --cwd = %v", err)
	}
	_, err = execute(t, root, "templates", "init", "set", "product", "--repo", "missing", "--", "./init.sh")
	if err == nil || clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("--repo with an unknown repository = %v (code %q)", err, clierr.CodeOf(err))
	}
	if group := findCommand(cli.New(cli.Options{ConfigDir: root, StateDir: root, DataDir: root}), "templates", "repos", "init"); group != nil {
		t.Fatal("twt templates repos init still exists")
	}
}

func TestTemplatesReposRemoveDeletesOneRepositorySpecification(t *testing.T) {
	root := t.TempDir()
	if _, err := execute(t, root, "templates", "create", "product"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"web", "api"} {
		if _, err := execute(t, root, "templates", "repos", "add", "product", name, "https://example.com/"+name+".git"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := execute(t, root, "templates", "repos", "remove", "product", "web"); err != nil {
		t.Fatalf("templates repos remove = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "config", "templates", "product.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "name: web") || !strings.Contains(string(data), "name: api") {
		t.Fatalf("Workspace Template YAML after removal:\n%s", data)
	}
	if _, err := execute(t, root, "templates", "repos", "remove", "product", "web"); err == nil || clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("second templates repos remove = %v (code %q)", err, clierr.CodeOf(err))
	}
}

func TestEmptyListsGiveAStderrHintInTextMode(t *testing.T) {
	root := t.TempDir()
	options := cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data")}
	stdout, stderr, err := executeCollectingOutput(t, options, "templates", "list")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" || !strings.Contains(stderr, "twt templates create NAME") {
		t.Fatalf("empty templates list stdout = %q, stderr = %q", stdout, stderr)
	}
	stdout, stderr, err = executeCollectingOutput(t, options, "workspaces", "list")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "" || !strings.Contains(stderr, "twt create NAME") {
		t.Fatalf("empty workspaces list stdout = %q, stderr = %q", stdout, stderr)
	}
	stdout, stderr, err = executeCollectingOutput(t, options, "templates", "list", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if stderr != "" || !strings.Contains(stdout, `"totalCount":0`) {
		t.Fatalf("empty templates list JSON stdout = %q, stderr = %q", stdout, stderr)
	}
}
