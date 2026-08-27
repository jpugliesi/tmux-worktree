package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/jpugliesi/tmux-worktree/internal/version"
)

func TestSchemaDescribesCommandsFlagsAndRawApplyOperations(t *testing.T) {
	root := t.TempDir()
	output, err := execute(t, root, "schema")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		SchemaVersion int `json:"schemaVersion"`
		Commands      []struct {
			Path      string `json:"path"`
			Arguments []struct {
				Name      string `json:"name"`
				Required  bool   `json:"required"`
				Condition string `json:"condition"`
			} `json:"arguments"`
			Flags []struct {
				Name     string   `json:"name"`
				Required bool     `json:"required"`
				Enum     []string `json:"enum"`
			} `json:"flags"`
		} `json:"commands"`
		ApplyOperations []struct {
			Operation string `json:"operation"`
			Fields    []struct {
				Path string `json:"path"`
				Type string `json:"type"`
			} `json:"fields"`
		} `json:"applyOperations"`
	}
	if err := json.Unmarshal([]byte(output), &schema); err != nil {
		t.Fatalf("decode schema: %v\n%s", err, output)
	}
	if schema.SchemaVersion != 2 || len(schema.Commands) == 0 || len(schema.ApplyOperations) != 42 {
		t.Fatalf("schema is incomplete: %+v", schema)
	}
	foundCreate := false
	foundWorkspacesCreate := false
	foundNext := false
	foundArchive := false
	for _, command := range schema.Commands {
		if command.Path == "twt next" {
			foundNext = true
			if len(command.Arguments) != 1 || command.Arguments[0].Name != "name_or_ticket" || command.Arguments[0].Required {
				t.Fatalf("next schema arguments = %+v", command.Arguments)
			}
			for _, flag := range command.Flags {
				if flag.Name == "no-fetch" {
					t.Fatal("next keeps the removed --no-fetch flag")
				}
			}
		}
		if command.Path == "twt archive" {
			foundArchive = true
			if len(command.Arguments) != 1 || command.Arguments[0].Name != "workspace" || command.Arguments[0].Required {
				t.Fatalf("archive schema arguments = %+v", command.Arguments)
			}
		}
		if command.Path != "twt create" && command.Path != "twt workspaces create" {
			continue
		}
		if command.Path == "twt create" {
			foundCreate = true
		} else {
			foundWorkspacesCreate = true
		}
		if len(command.Arguments) != 1 || command.Arguments[0].Name != "name" || command.Arguments[0].Required {
			t.Fatalf("%s schema arguments = %+v", command.Path, command.Arguments)
		}
		if !strings.Contains(command.Arguments[0].Condition, "prompt") {
			t.Fatalf("%s schema name condition = %q", command.Path, command.Arguments[0].Condition)
		}
		flags := map[string]struct {
			required bool
			enum     []string
		}{}
		for _, flag := range command.Flags {
			flags[flag.Name] = struct {
				required bool
				enum     []string
			}{flag.Required, flag.Enum}
		}
		if flags["template"].required || len(flags["output"].enum) != 3 {
			t.Fatalf("%s schema flags = %+v", command.Path, flags)
		}
		if _, ok := flags["branch"]; !ok {
			t.Fatalf("%s schema misses --branch: %+v", command.Path, flags)
		}
		if _, ok := flags["fresh"]; !ok {
			t.Fatalf("%s schema misses --fresh: %+v", command.Path, flags)
		}
		if _, ok := flags["no-fetch"]; ok {
			t.Fatalf("%s keeps the removed --no-fetch flag", command.Path)
		}
	}
	if !foundCreate {
		t.Fatal("schema does not contain twt create")
	}
	if !foundWorkspacesCreate {
		t.Fatal("schema does not contain twt workspaces create")
	}
	if !foundNext {
		t.Fatal("schema does not contain twt next")
	}
	if !foundArchive {
		t.Fatal("schema does not contain twt archive")
	}
	foundArchiveOperation := false
	for _, operation := range schema.ApplyOperations {
		if operation.Operation == "agents.register" && len(operation.Fields) != 6 {
			t.Fatalf("agents.register fields = %+v", operation.Fields)
		}
		if operation.Operation == "workspaces.archive" {
			foundArchiveOperation = len(operation.Fields) == 2 && operation.Fields[0].Path == "workspace.reference" && operation.Fields[1].Path == "workspace.force"
		}
	}
	if !foundArchiveOperation {
		t.Fatal("schema does not contain workspaces.archive")
	}

	foundCreateBlockedBy := false
	foundSetBlockedBy := false
	for _, command := range schema.Commands {
		if command.Path != "twt tickets create" && command.Path != "twt tickets set" {
			continue
		}
		hasBlockedBy := false
		for _, flag := range command.Flags {
			if flag.Name == "blocked-by" {
				hasBlockedBy = true
				break
			}
		}
		if !hasBlockedBy {
			t.Fatalf("%s schema misses --blocked-by: %+v", command.Path, command.Flags)
		}
	}
	for _, operation := range schema.ApplyOperations {
		if operation.Operation != "tickets.create" && operation.Operation != "tickets.set" {
			continue
		}
		found := false
		for _, field := range operation.Fields {
			if field.Path == "ticket.blockedBy" && field.Type == "array[string]" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s schema misses ticket.blockedBy: %+v", operation.Operation, operation.Fields)
		}
		if operation.Operation == "tickets.create" {
			foundCreateBlockedBy = true
		} else {
			foundSetBlockedBy = true
		}
	}
	if !foundCreateBlockedBy || !foundSetBlockedBy {
		t.Fatal("schema misses tickets.create or tickets.set blockedBy")
	}
}

func TestDryRunAndRawApplyDoNotChangeState(t *testing.T) {
	root := t.TempDir()
	output, err := execute(t, root, "templates", "create", "preview", "--dry-run", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, `"status":"valid"`) {
		t.Fatalf("dry-run output = %s", output)
	}
	if _, err := os.Stat(filepath.Join(root, "config", "templates", "preview.yaml")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a template: %v", err)
	}

	options := cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data")}
	request := strings.NewReader(`{"operation":"templates.create","template":{"name":"raw-preview"}}`)
	rawOutput := executeWithOptions(t, options, request, "apply", "--stdin", "--dry-run", "--output", "json")
	if !strings.Contains(rawOutput, `"operation":"templates.create"`) || !strings.Contains(rawOutput, `"status":"valid"`) {
		t.Fatalf("raw apply output = %s", rawOutput)
	}
	if _, err := os.Stat(filepath.Join(root, "config", "templates", "raw-preview.yaml")); !os.IsNotExist(err) {
		t.Fatalf("raw apply dry-run created a template: %v", err)
	}
	if _, err := execute(t, root, "templates", "create", "raw-preview"); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetIn(strings.NewReader(`{"operation":"templates.create","template":{"name":"raw-preview"}}`))
	command.SetArgs(forceTextOutput([]string{"apply", "--stdin", "--dry-run", "--output", "json"}))
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate raw dry-run error = %v", err)
	}
}

func TestRawApplyArchivesAWorkspace(t *testing.T) {
	root := t.TempDir()
	options := cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}
	workspace := domain.Workspace{
		Version:      domain.WorkspaceVersion,
		ID:           "workspace-archive-id",
		Name:         "archive-me",
		TemplateName: "example",
		Status:       domain.WorkspaceActive,
	}
	if err := store.NewWorkspaceStore(options.StateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetIn(strings.NewReader(`{"operation":"workspaces.archive","workspace":{"reference":"archive-me","name":"not-valid"}}`))
	command.SetArgs(forceTextOutput([]string{"apply", "--stdin", "--dry-run", "--output", "json"}))
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), `unknown field "name"`) {
		t.Fatalf("archive request with create fields error = %v", err)
	}
	options.Stdout, options.Stderr = nil, nil
	request := strings.NewReader(`{"operation":"workspaces.archive","workspace":{"reference":"archive-me"}}`)
	output := executeWithOptions(t, options, request, "apply", "--stdin", "--output", "json")
	if !strings.Contains(output, `"operation":"workspaces.archive"`) || !strings.Contains(output, `"status":"applied"`) {
		t.Fatalf("raw archive output = %s", output)
	}
	archived, err := store.NewWorkspaceStore(options.StateDir).Find(workspace.ID)
	if err != nil || archived.Status != domain.WorkspaceArchived || archived.ArchivedAt == nil {
		t.Fatalf("raw archive Workspace = %#v, error = %v", archived, err)
	}
}

func TestJSONErrorsAndListLimitsAreMachineReadable(t *testing.T) {
	root := t.TempDir()
	if _, err := execute(t, root, "templates", "create", "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, root, "templates", "create", "beta"); err != nil {
		t.Fatal(err)
	}
	output, err := execute(t, root, "templates", "list", "--limit", "1", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Templates []struct {
			Name string `json:"name"`
		} `json:"templates"`
		TotalCount int  `json:"totalCount"`
		Truncated  bool `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(output), &list); err != nil || len(list.Templates) != 1 || list.Templates[0].Name != "alpha" {
		t.Fatalf("limited list = %s; error = %v", output, err)
	}
	if list.TotalCount != 2 || !list.Truncated {
		t.Fatalf("limited list totals = %s", output)
	}

	var stdout, stderr bytes.Buffer
	command := cli.New(cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), Stdout: &stdout, Stderr: &stderr})
	command.SetArgs(forceTextOutput([]string{"templates", "show", "missing", "--output", "json"}))
	commandErr := command.Execute()
	if commandErr == nil {
		t.Fatal("missing template did not return an error")
	}
	if err := cli.WriteError(command, &stderr, commandErr); err != nil {
		t.Fatal(err)
	}
	var result struct {
		SchemaVersion int `json:"schemaVersion"`
		Error         struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil || result.SchemaVersion != 2 || result.Error.Code != "not_found" || !strings.Contains(result.Error.Message, "does not exist") {
		t.Fatalf("structured error = %s; decode error = %v", stderr.String(), err)
	}
}

func TestErrorCodesMapToExitCodes(t *testing.T) {
	root := t.TempDir()

	_, err := execute(t, root, "templates", "show", "nope", "--output", "json")
	if err == nil {
		t.Fatal("missing template did not return an error")
	}
	if clierr.CodeOf(err) != clierr.NotFound || clierr.ExitCode(err) != 3 {
		t.Fatalf("missing template code = %q, exit = %d; want not_found, 3", clierr.CodeOf(err), clierr.ExitCode(err))
	}

	_, err = execute(t, root, "templates", "list", "--definitely-not-a-flag")
	if err == nil {
		t.Fatal("unknown flag did not return an error")
	}
	if clierr.CodeOf(err) != clierr.InvalidUsage || clierr.ExitCode(err) != 2 {
		t.Fatalf("unknown flag code = %q, exit = %d; want invalid_usage, 2", clierr.CodeOf(err), clierr.ExitCode(err))
	}
}

func TestLockedMutationsReportTheLockedCode(t *testing.T) {
	root := t.TempDir()
	lock, err := store.AcquireMutationLock(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	var stdout, stderr bytes.Buffer
	command := cli.New(cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), Stdout: &stdout, Stderr: &stderr})
	command.SetArgs(forceTextOutput([]string{"templates", "create", "blocked", "--output", "json"}))
	commandErr := command.Execute()
	if commandErr == nil {
		t.Fatal("locked mutation did not return an error")
	}
	if clierr.CodeOf(commandErr) != clierr.Locked || clierr.ExitCode(commandErr) != 3 {
		t.Fatalf("locked code = %q, exit = %d; want locked, 3", clierr.CodeOf(commandErr), clierr.ExitCode(commandErr))
	}
	if err := cli.WriteError(command, &stderr, commandErr); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil || result.Error.Code != "locked" {
		t.Fatalf("locked JSON error = %s; decode error = %v", stderr.String(), err)
	}
}

func TestWriteErrorShowsHintsInTextAndJSON(t *testing.T) {
	hinted := clierr.WithHint(
		clierr.New(clierr.PreconditionFailed, "Workspace %q is archived", "fix-auth"),
		"Run 'twt workspaces open %s' to open the Workspace.", "fix-auth")

	textCommand := cli.New(cli.Options{ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir()})
	var text bytes.Buffer
	if err := cli.WriteError(textCommand, &text, hinted); err != nil {
		t.Fatal(err)
	}
	want := "twt: Workspace \"fix-auth\" is archived\nRun 'twt workspaces open fix-auth' to open the Workspace.\n"
	if text.String() != want {
		t.Fatalf("text error:\n%s\nwant:\n%s", text.String(), want)
	}

	jsonCommand := cli.New(cli.Options{ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir()})
	if err := jsonCommand.ParseFlags([]string{"--output", "json"}); err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := cli.WriteError(jsonCommand, &encoded, hinted); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Error struct {
			Code string `json:"code"`
			Hint string `json:"hint"`
		} `json:"error"`
	}
	if err := json.Unmarshal(encoded.Bytes(), &result); err != nil {
		t.Fatalf("decode JSON error: %v\n%s", err, encoded.String())
	}
	if result.Error.Code != "precondition_failed" || result.Error.Hint != "Run 'twt workspaces open fix-auth' to open the Workspace." {
		t.Fatalf("JSON error = %+v", result.Error)
	}
}

func TestVersionFlagPrintsTheBuildVersion(t *testing.T) {
	output, err := execute(t, t.TempDir(), "--version")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, version.Version) {
		t.Fatalf("--version output = %q does not contain %q", output, version.Version)
	}
}

func TestSchemaListsVersionErrorCodesAndExitCodes(t *testing.T) {
	output, err := execute(t, t.TempDir(), "schema")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Version    string            `json:"version"`
		ErrorCodes []string          `json:"errorCodes"`
		ExitCodes  map[string]string `json:"exitCodes"`
	}
	if err := json.Unmarshal([]byte(output), &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	if schema.Version != version.Version {
		t.Fatalf("schema version = %q, want %q", schema.Version, version.Version)
	}
	wantCodes := []string{"already_exists", "internal", "invalid_usage", "locked", "not_found", "precondition_failed", "unsafe_state"}
	if len(schema.ErrorCodes) != len(wantCodes) {
		t.Fatalf("schema errorCodes = %v, want %v", schema.ErrorCodes, wantCodes)
	}
	for index, code := range wantCodes {
		if schema.ErrorCodes[index] != code {
			t.Fatalf("schema errorCodes = %v, want %v", schema.ErrorCodes, wantCodes)
		}
	}
	wantExits := map[string]string{"0": "success", "1": "internal", "2": "invalid_usage", "3": "precondition"}
	for exit, meaning := range wantExits {
		if schema.ExitCodes[exit] != meaning {
			t.Fatalf("schema exitCodes = %v, want %v", schema.ExitCodes, wantExits)
		}
	}
}
