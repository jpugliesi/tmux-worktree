package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
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
	if schema.SchemaVersion != 1 || len(schema.Commands) == 0 || len(schema.ApplyOperations) != 4 {
		t.Fatalf("schema is incomplete: %+v", schema)
	}
	foundCreate := false
	foundArchive := false
	for _, command := range schema.Commands {
		if command.Path == "twt2 archive" {
			foundArchive = true
			if len(command.Arguments) != 1 || command.Arguments[0].Name != "project" || command.Arguments[0].Required {
				t.Fatalf("archive schema arguments = %+v", command.Arguments)
			}
		}
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
	if !foundArchive {
		t.Fatal("schema does not contain twt2 archive")
	}
	foundArchiveOperation := false
	for _, operation := range schema.ApplyOperations {
		if operation.Operation == "agents.register" && len(operation.Fields) != 5 {
			t.Fatalf("agents.register fields = %+v", operation.Fields)
		}
		if operation.Operation == "projects.archive" {
			foundArchiveOperation = len(operation.Fields) == 1 && operation.Fields[0].Path == "project.reference"
		}
	}
	if !foundArchiveOperation {
		t.Fatal("schema does not contain projects.archive")
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

func TestRawApplyArchivesAProject(t *testing.T) {
	root := t.TempDir()
	options := cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}
	project := domain.Project{
		Version:      domain.ProjectVersion,
		ID:           "project-archive-id",
		Name:         "archive-me",
		TemplateName: "example",
		Status:       domain.ProjectActive,
	}
	if err := store.NewProjectStore(options.StateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	command.SetIn(strings.NewReader(`{"operation":"projects.archive","project":{"reference":"archive-me","name":"not-valid"}}`))
	command.SetArgs([]string{"apply", "--stdin", "--dry-run", "--output", "json"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "only project.reference") {
		t.Fatalf("archive request with create fields error = %v", err)
	}
	options.Stdout, options.Stderr = nil, nil
	request := strings.NewReader(`{"operation":"projects.archive","project":{"reference":"archive-me"}}`)
	output := executeWithOptions(t, options, request, "apply", "--stdin", "--output", "json")
	if !strings.Contains(output, `"operation":"projects.archive"`) || !strings.Contains(output, `"status":"applied"`) {
		t.Fatalf("raw archive output = %s", output)
	}
	archived, err := store.NewProjectStore(options.StateDir).Find(project.ID)
	if err != nil || archived.Status != domain.ProjectArchived || archived.ArchivedAt == nil {
		t.Fatalf("raw archive Project = %#v, error = %v", archived, err)
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
