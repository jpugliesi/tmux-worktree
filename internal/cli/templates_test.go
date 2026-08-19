package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func execute(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	command := cli.New(cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
		Stdout:    &stdout,
		Stderr:    &stderr,
	})
	command.SetArgs(args)
	err := command.Execute()
	if stderr.Len() != 0 {
		t.Logf("stderr: %s", stderr.String())
	}
	return stdout.String(), err
}

func TestTemplateMutationsUseTheGlobalMutationLock(t *testing.T) {
	root := t.TempDir()
	if _, err := execute(t, root, "templates", "create", "existing"); err != nil {
		t.Fatal(err)
	}
	lock, err := store.AcquireMutationLock(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := execute(t, root, "templates", "create", "blocked"); err == nil || !strings.Contains(err.Error(), "another twt2 change") {
		t.Fatalf("concurrent create error = %v", err)
	}
	if _, err := execute(t, root, "templates", "repos", "add", "existing", "app", "https://example.com/app.git"); err == nil || !strings.Contains(err.Error(), "another twt2 change") {
		t.Fatalf("concurrent update error = %v", err)
	}
}

func TestTemplatesCreateWritesEditableYAML(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	command := cli.New(cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
		Stdout:    &stdout,
		Stderr:    &stderr,
	})
	command.SetArgs([]string{"templates", "create", "everysphere"})

	if err := command.Execute(); err != nil {
		t.Fatalf("templates create returned an error: %v\nstderr: %s", err, stderr.String())
	}

	want := "version: 1\nname: everysphere\nrepositories: []\n"
	path := filepath.Join(root, "config", "templates", "everysphere.yaml")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	if string(got) != want {
		t.Fatalf("template YAML:\n%s\nwant:\n%s", got, want)
	}
}

func TestTemplatesListAndShow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, name := range []string{"zebra", "alpha"} {
		if _, err := execute(t, root, "templates", "create", name); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	output, err := execute(t, root, "templates", "list")
	if err != nil {
		t.Fatalf("templates list returned an error: %v", err)
	}
	if output != "alpha\nzebra\n" {
		t.Fatalf("templates list output = %q", output)
	}

	output, err = execute(t, root, "templates", "show", "alpha")
	if err != nil {
		t.Fatalf("templates show returned an error: %v", err)
	}
	want := "version: 1\nname: alpha\nrepositories: []\n"
	if output != want {
		t.Fatalf("templates show output:\n%s\nwant:\n%s", output, want)
	}
}

func TestTemplatesReposAddSupportsInterspersedFlags(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := execute(t, root, "templates", "create", "everysphere"); err != nil {
		t.Fatalf("create template: %v", err)
	}

	_, err := execute(t, root,
		"templates", "repos", "add", "everysphere",
		"--depth", "1", "everysphere",
		"--remote", "github=https://github.com/anysphere/everysphere.git",
		"https://origin.cursor.com/anysphere/everysphere.git",
		"--default-branch", "main", "--window-name", "app",
	)
	if err != nil {
		t.Fatalf("templates repos add returned an error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "config", "templates", "everysphere.yaml"))
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	wantParts := []string{
		"name: everysphere",
		"url: https://origin.cursor.com/anysphere/everysphere.git",
		"depth: 1",
		"github: https://github.com/anysphere/everysphere.git",
		"default_branch: main",
		"window_name: app",
	}
	for _, want := range wantParts {
		if !strings.Contains(string(data), want) {
			t.Errorf("template YAML does not contain %q:\n%s", want, data)
		}
	}
}

func TestTemplatesInitializationCommands(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := execute(t, root, "templates", "create", "everysphere"); err != nil {
		t.Fatalf("create template: %v", err)
	}
	if _, err := execute(t, root, "templates", "repos", "add", "everysphere", "app", "https://example.com/app.git"); err != nil {
		t.Fatalf("add repository: %v", err)
	}
	if _, err := execute(t, root, "templates", "repos", "init", "set", "everysphere", "app", "--", "./init.sh", "--quick"); err != nil {
		t.Fatalf("set repository initialization: %v", err)
	}
	if _, err := execute(t, root, "templates", "init", "set", "everysphere", "--cwd", "app", "--", "./scripts/init-project.sh"); err != nil {
		t.Fatalf("set template initialization: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "config", "templates", "everysphere.yaml"))
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	wantParts := []string{
		"command:\n            - ./init.sh\n            - --quick",
		"command:\n        - ./scripts/init-project.sh",
		"working_directory: app",
	}
	for _, want := range wantParts {
		if !strings.Contains(string(data), want) {
			t.Errorf("template YAML does not contain %q:\n%s", want, data)
		}
	}
}

func TestTemplatesInitRequiresExplicitWorkingDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := execute(t, root, "templates", "create", "example"); err != nil {
		t.Fatalf("create template: %v", err)
	}
	_, err := execute(t, root, "templates", "init", "set", "example", "--", "./init.sh")
	if err == nil || !strings.Contains(err.Error(), "--cwd") {
		t.Fatalf("templates init set error = %v, want an explicit --cwd error", err)
	}
}

func TestTemplatesValidateChecksUserEditedYAML(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := execute(t, root, "templates", "create", "example"); err != nil {
		t.Fatalf("create template: %v", err)
	}
	path := filepath.Join(root, "config", "templates", "example.yaml")
	invalid := "version: 1\nname: example\nrepositories: []\ntypo: true\n"
	if err := os.WriteFile(path, []byte(invalid), 0o644); err != nil {
		t.Fatalf("edit template: %v", err)
	}

	_, err := execute(t, root, "templates", "validate", "example")
	if err == nil || !strings.Contains(err.Error(), "field typo not found") {
		t.Fatalf("templates validate error = %v, want an unknown field error", err)
	}
}

func TestTemplatesReposAddDoesNotChangeTemplateWhenInputIsInvalid(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := execute(t, root, "templates", "create", "example"); err != nil {
		t.Fatalf("create template: %v", err)
	}
	path := filepath.Join(root, "config", "templates", "example.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read template before update: %v", err)
	}

	_, err = execute(t, root, "templates", "repos", "add", "example", "app", "https://example.com/app.git", "--depth", "-1")
	if err == nil || !strings.Contains(err.Error(), "negative clone depth") {
		t.Fatalf("templates repos add error = %v, want a negative depth error", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read template after update: %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("invalid update changed template:\n%s", after)
	}
}
