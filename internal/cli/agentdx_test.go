package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
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
				Name     string `json:"name"`
				Required bool   `json:"required"`
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
	if schema.SchemaVersion != 1 || len(schema.Commands) == 0 || len(schema.ApplyOperations) != 3 {
		t.Fatalf("schema is incomplete: %+v", schema)
	}
	foundCreate := false
	for _, command := range schema.Commands {
		if command.Path != "twt2 projects create" {
			continue
		}
		foundCreate = true
		if len(command.Arguments) != 1 || command.Arguments[0].Name != "name" || !command.Arguments[0].Required {
			t.Fatalf("projects create schema arguments = %+v", command.Arguments)
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
		if !flags["template"].required || len(flags["output"].enum) != 2 {
			t.Fatalf("projects create schema flags = %+v", flags)
		}
	}
	if !foundCreate {
		t.Fatal("schema does not contain twt2 projects create")
	}
	for _, operation := range schema.ApplyOperations {
		if operation.Operation == "agents.register" && len(operation.Fields) != 5 {
			t.Fatalf("agents.register fields = %+v", operation.Fields)
		}
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
	command.SetArgs([]string{"apply", "--stdin", "--dry-run", "--output", "json"})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate raw dry-run error = %v", err)
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
		Templates []string `json:"templates"`
	}
	if err := json.Unmarshal([]byte(output), &list); err != nil || len(list.Templates) != 1 {
		t.Fatalf("limited list = %s; error = %v", output, err)
	}

	var stdout, stderr bytes.Buffer
	command := cli.New(cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), Stdout: &stdout, Stderr: &stderr})
	command.SetArgs([]string{"templates", "show", "missing", "--output", "json"})
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
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil || result.SchemaVersion != 1 || result.Error.Code != "command_failed" || !strings.Contains(result.Error.Message, "does not exist") {
		t.Fatalf("structured error = %s; decode error = %v", stderr.String(), err)
	}
}
