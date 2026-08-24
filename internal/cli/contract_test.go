package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

// findCommand returns the command at the given path, or nil.
func findCommand(root *cobra.Command, path ...string) *cobra.Command {
	current := root
	for _, name := range path {
		found := (*cobra.Command)(nil)
		for _, child := range current.Commands() {
			if child.Name() == name {
				found = child
				break
			}
		}
		if found == nil {
			return nil
		}
		current = found
	}
	return current
}

// executeCollectingInput runs twt with one standard input value and returns
// standard output, standard error, and the command error.
func executeCollectingInput(t *testing.T, options cli.Options, stdin io.Reader, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	options.Stdout, options.Stderr = &stdout, &stderr
	command := cli.New(options)
	if stdin != nil {
		command.SetIn(stdin)
	}
	command.SetArgs(forceTextOutput(args))
	err := command.Execute()
	return stdout.String(), stderr.String(), err
}

// placeholderToken matches an uppercase argument placeholder in a Use value,
// such as NAME, [WORKSPACE], or RESUME_COMMAND....
var placeholderToken = regexp.MustCompile(`^\[?[A-Z][A-Z_]*\]?\.{0,3}\]?$`)

func TestEveryCommandWithPlaceholdersDeclaresItsArguments(t *testing.T) {
	root := cli.New(cli.Options{ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir()})
	var missing []string
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		if command.Runnable() && command.Name() != "help" && command.Name() != "completion" {
			tokens := strings.Fields(command.Use)
			hasPlaceholder := false
			for index, token := range tokens {
				if index == 0 || token == "--" {
					continue
				}
				if strings.HasPrefix(tokens[index-1], "-") {
					// The token is the value of a flag, not a positional
					// argument.
					continue
				}
				if placeholderToken.MatchString(token) {
					hasPlaceholder = true
				}
			}
			if hasPlaceholder && command.Annotations["twt.arguments"] == "" {
				missing = append(missing, command.CommandPath())
			}
		}
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(root)
	if len(missing) > 0 {
		t.Fatalf("commands without an arguments annotation: %v", missing)
	}
}

func TestEveryListCommandHasLsAlias(t *testing.T) {
	root := cli.New(cli.Options{ConfigDir: t.TempDir(), StateDir: t.TempDir(), DataDir: t.TempDir()})
	var missing []string
	found := 0
	var walk func(*cobra.Command)
	walk = func(command *cobra.Command) {
		if command.Name() == "list" && command.Runnable() {
			found++
			if !command.HasAlias("ls") {
				missing = append(missing, command.CommandPath())
			}
		}
		for _, child := range command.Commands() {
			walk(child)
		}
	}
	walk(root)
	if found == 0 {
		t.Fatal("no list commands found")
	}
	if len(missing) > 0 {
		t.Fatalf("list commands missing ls alias: %s", strings.Join(missing, ", "))
	}
}

func TestSchemaSkipsHelpAndCompletionCommands(t *testing.T) {
	output, err := execute(t, t.TempDir(), "schema")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Commands []struct {
			Path      string `json:"path"`
			Arguments []struct {
				Name string `json:"name"`
			} `json:"arguments"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(output), &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	arguments := map[string][]string{}
	for _, command := range schema.Commands {
		if strings.Contains(command.Path, "help") || strings.Contains(command.Path, "completion") || strings.Contains(command.Path, "__complete") {
			t.Fatalf("schema contains the command %q", command.Path)
		}
		names := make([]string, 0, len(command.Arguments))
		for _, argument := range command.Arguments {
			names = append(names, argument.Name)
		}
		arguments[command.Path] = names
	}
	want := map[string][]string{
		"twt templates prepare":      {"template"},
		"twt templates repos add":    {"template", "repo", "url"},
		"twt templates init set":     {"template", "command"},
		"twt templates remove":       {"name"},
		"twt templates path":         {"name"},
		"twt templates edit":         {"name"},
		"twt templates repos remove": {"template", "repo"},
		"twt tickets create":         {"description"},
		"twt tickets show":           {"ticket"},
		"twt tickets edit":           {"ticket"},
		"twt tickets set":            {"ticket"},
		"twt tickets claim":          {"ticket"},
		"twt tickets unclaim":        {"ticket"},
		"twt tickets comment":        {"ticket"},
		"twt projects create":        {"name"},
		"twt projects show":          {"name"},
	}
	for path, names := range want {
		if strings.Join(arguments[path], ",") != strings.Join(names, ",") {
			t.Fatalf("%s arguments = %v, want %v", path, arguments[path], names)
		}
	}
	if _, found := arguments["twt templates repos init set"]; found {
		t.Fatal("schema still contains twt templates repos init set")
	}
}

func TestWorkspaceReadOutputsUseEnvelopesAndTotals(t *testing.T) {
	root := t.TempDir()
	options := cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data")}
	now := time.Now().UTC()
	for index, name := range []string{"alpha", "beta"} {
		workspace := domain.Workspace{
			Version: domain.WorkspaceVersion, ID: fmt.Sprintf("workspace-%d", index), Name: name,
			TemplateName: "example", Status: domain.WorkspaceActive, CreatedAt: now, UpdatedAt: now,
		}
		if err := store.NewWorkspaceStore(options.StateDir).Save(workspace); err != nil {
			t.Fatal(err)
		}
	}
	show := executeWithOptions(t, options, nil, "workspaces", "show", "alpha", "--output", "json")
	var showResult struct {
		SchemaVersion int `json:"schemaVersion"`
		Workspace     struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			SchemaVersion int    `json:"schemaVersion"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal([]byte(show), &showResult); err != nil {
		t.Fatalf("decode workspaces show: %v\n%s", err, show)
	}
	if showResult.SchemaVersion != 2 || showResult.Workspace.Name != "alpha" || showResult.Workspace.SchemaVersion != 0 {
		t.Fatalf("workspaces show envelope = %s", show)
	}

	list := executeWithOptions(t, options, nil, "workspaces", "list", "--limit", "1", "--output", "json")
	var listResult struct {
		Workspaces []struct {
			Name          string `json:"name"`
			SchemaVersion int    `json:"schemaVersion"`
		} `json:"workspaces"`
		TotalCount int  `json:"totalCount"`
		Truncated  bool `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(list), &listResult); err != nil {
		t.Fatalf("decode workspaces list: %v\n%s", err, list)
	}
	if len(listResult.Workspaces) != 1 || listResult.TotalCount != 2 || !listResult.Truncated {
		t.Fatalf("workspaces list totals = %s", list)
	}
	if listResult.Workspaces[0].SchemaVersion != 0 {
		t.Fatalf("workspaces list elements still carry schemaVersion: %s", list)
	}
}

func TestCurrentSentinelWorksForWorkspaceArguments(t *testing.T) {
	root := t.TempDir()
	options := cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data")}
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-current-id", Name: "fix-auth",
		TemplateName: "example", Status: domain.WorkspaceActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.NewWorkspaceStore(options.StateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TWT_WORKSPACE_ID", workspace.ID)
	t.Setenv("TMUX_PANE", "")

	show := executeWithOptions(t, options, nil, "workspaces", "show", "current", "--output", "json")
	if !strings.Contains(show, `"id":"workspace-current-id"`) {
		t.Fatalf("workspaces show current = %s", show)
	}
	archive := executeWithOptions(t, options, nil, "workspaces", "archive", "current", "--dry-run", "--output", "json")
	if !strings.Contains(archive, `"status":"valid"`) || !strings.Contains(archive, "workspace-current-id") {
		t.Fatalf("workspaces archive current --dry-run = %s", archive)
	}
	retry := executeWithOptions(t, options, nil, "workspaces", "setup", "retry", "current", "--dry-run", "--output", "json")
	if !strings.Contains(retry, `"status":"valid"`) {
		t.Fatalf("workspaces setup retry current --dry-run = %s", retry)
	}
	if _, _, err := executeCollectingOutput(t, options, "workspaces", "show", "no-such-workspace"); err == nil || clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("workspaces show for an unknown Workspace = %v", err)
	}
}

func TestApplySupportsRepositoryAddAndWorkspaceRemoval(t *testing.T) {
	root := t.TempDir()
	options := cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data")}
	if _, err := execute(t, root, "templates", "create", "product"); err != nil {
		t.Fatal(err)
	}
	request := `{"operation":"templates.repos.add","template":{"name":"product","repository":{"name":"web","url":"https://example.com/web.git","depth":1}}}`
	output := executeWithOptions(t, options, strings.NewReader(request), "apply", "--stdin", "--output", "json")
	if !strings.Contains(output, `"operation":"templates.repos.add"`) || !strings.Contains(output, `"status":"applied"`) {
		t.Fatalf("apply templates.repos.add = %s", output)
	}
	data, err := os.ReadFile(filepath.Join(root, "config", "templates", "product.yaml"))
	if err != nil || !strings.Contains(string(data), "url: https://example.com/web.git") {
		t.Fatalf("Workspace Template after apply = %s, error = %v", data, err)
	}

	now := time.Now().UTC()
	workspaceID := "workspace-remove-id"
	workspaceRoot := filepath.Join(options.DataDir, "projects", "remove-me-"+workspaceID[:8])
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf(`{"owner":"twt","workspaceId":%q}`, workspaceID)
	if err := os.WriteFile(filepath.Join(workspaceRoot, ".twt-owned.json"), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: workspaceID, Name: "remove-me", Root: workspaceRoot,
		TemplateName: "product", Status: domain.WorkspaceArchived, CreatedAt: now, UpdatedAt: now, ArchivedAt: &now,
	}
	if err := store.NewWorkspaceStore(options.StateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	plan := executeWithOptions(t, options,
		strings.NewReader(`{"operation":"workspaces.remove","workspace":{"reference":"remove-me"}}`),
		"apply", "--stdin", "--output", "json")
	var planResult struct {
		Applied bool `json:"applied"`
		Plan    struct {
			WorkspaceID string `json:"workspaceId"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(plan), &planResult); err != nil {
		t.Fatalf("decode apply workspaces.remove: %v\n%s", err, plan)
	}
	if planResult.Applied || planResult.Plan.WorkspaceID != workspace.ID {
		t.Fatalf("apply workspaces.remove plan = %s", plan)
	}
	if _, err := store.NewWorkspaceStore(options.StateDir).Find(workspace.ID); err != nil {
		t.Fatalf("the plan removed the Workspace record: %v", err)
	}
	applied := executeWithOptions(t, options,
		strings.NewReader(`{"operation":"workspaces.remove","workspace":{"reference":"remove-me","apply":true}}`),
		"apply", "--stdin", "--output", "json")
	if !strings.Contains(applied, `"applied":true`) {
		t.Fatalf("apply workspaces.remove --apply = %s", applied)
	}
	if _, err := store.NewWorkspaceStore(options.StateDir).Find(workspace.ID); err == nil {
		t.Fatal("apply workspaces.remove kept the Workspace record")
	}

	_, _, err = executeCollectingInput(t, options, strings.NewReader(`{"operation":"workspaces.nope","workspace":{"reference":"x"}}`), "apply", "--stdin", "--output", "json")
	if err == nil || clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("unknown apply operation = %v (code %q)", err, clierr.CodeOf(err))
	}
	for _, name := range []string{"templates.create", "templates.repos.add", "workspaces.create", "workspaces.archive", "workspaces.remove", "agents.register"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("unknown apply operation error does not list %q: %v", name, err)
		}
	}
	_, _, err = executeCollectingInput(t, options,
		strings.NewReader(`{"operation":"workspaces.create","workspace":{"name":"x","template":"product","noOpen":false}}`),
		"apply", "--stdin", "--dry-run", "--output", "json")
	if err == nil || !strings.Contains(err.Error(), "noOpen") {
		t.Fatalf("apply workspaces.create with noOpen false = %v", err)
	}
}

func TestJSONOutputNoLongerImpliesNoOpen(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	initGitRepository(t, source)
	configDir := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(configDir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	template := fmt.Sprintf("version: 1\nname: example\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "example.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}

	// Text output to a buffer is not a terminal, so twt does not attach.
	stdout, _, err := executeCollectingOutput(t, options, "workspaces", "create", "buffered", "--template", "example")
	if err != nil {
		t.Fatalf("workspaces create without --no-open: %v", err)
	}
	if !strings.Contains(stdout, `Created Workspace "buffered"`) {
		t.Fatalf("workspaces create output = %q", stdout)
	}
	// JSON output no longer implies no-open; the buffer decides it.
	openOutput, _, err := executeCollectingOutput(t, options, "workspaces", "open", "buffered", "--output", "json")
	if err != nil {
		t.Fatalf("workspaces open with JSON output: %v", err)
	}
	if !strings.Contains(openOutput, `"status":"applied"`) {
		t.Fatalf("workspaces open JSON = %q", openOutput)
	}
	textOpen, _, err := executeCollectingOutput(t, options, "workspaces", "open", "buffered")
	if err != nil {
		t.Fatalf("workspaces open in text mode: %v", err)
	}
	if !strings.Contains(textOpen, `Opened Workspace "buffered"`) {
		t.Fatalf("workspaces open text = %q", textOpen)
	}
}

func TestCompletionFunctionsListStoredNames(t *testing.T) {
	root := t.TempDir()
	options := cli.Options{ConfigDir: filepath.Join(root, "config"), StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data")}
	for _, name := range []string{"alpha", "zebra"} {
		if _, err := execute(t, root, "templates", "create", name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := execute(t, root, "templates", "repos", "add", "alpha", "web", "https://example.com/web.git"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	workspace := domain.Workspace{
		Version: domain.WorkspaceVersion, ID: "workspace-completion-id", Name: "fix-auth",
		TemplateName: "alpha", Status: domain.WorkspaceActive, CreatedAt: now, UpdatedAt: now,
		Repositories: []domain.WorkspaceRepository{{Name: "web"}},
	}
	if err := store.NewWorkspaceStore(options.StateDir).Save(workspace); err != nil {
		t.Fatal(err)
	}
	command := cli.New(options)

	templateShow := findCommand(command, "templates", "show")
	if templateShow == nil || templateShow.ValidArgsFunction == nil {
		t.Fatal("twt templates show has no argument completion")
	}
	names, _ := templateShow.ValidArgsFunction(templateShow, nil, "")
	if strings.Join(names, ",") != "alpha,zebra" {
		t.Fatalf("template completion = %v", names)
	}
	names, _ = templateShow.ValidArgsFunction(templateShow, nil, "z")
	if strings.Join(names, ",") != "zebra" {
		t.Fatalf("template completion with a prefix = %v", names)
	}
	names, _ = templateShow.ValidArgsFunction(templateShow, []string{"alpha"}, "")
	if len(names) != 0 {
		t.Fatalf("template completion after the first argument = %v", names)
	}

	workspacesShow := findCommand(command, "workspaces", "show")
	names, _ = workspacesShow.ValidArgsFunction(workspacesShow, nil, "")
	if strings.Join(names, ",") != "current,fix-auth" {
		t.Fatalf("Workspace completion = %v", names)
	}

	workspacesPath := findCommand(command, "workspaces", "path")
	names, _ = workspacesPath.ValidArgsFunction(workspacesPath, []string{"fix-auth"}, "")
	if strings.Join(names, ",") != "web" {
		t.Fatalf("Workspace repository completion = %v", names)
	}

	reposRemove := findCommand(command, "templates", "repos", "remove")
	names, _ = reposRemove.ValidArgsFunction(reposRemove, []string{"alpha"}, "")
	if strings.Join(names, ",") != "web" {
		t.Fatalf("Workspace Template repository completion = %v", names)
	}

	workspacesCreate := findCommand(command, "workspaces", "create")
	templateFlag, found := workspacesCreate.GetFlagCompletionFunc("template")
	if !found {
		t.Fatal("--template has no completion function")
	}
	names, _ = templateFlag(workspacesCreate, []string{"new-workspace"}, "a")
	if strings.Join(names, ",") != "alpha" {
		t.Fatalf("--template completion = %v", names)
	}

	register := findCommand(command, "agents", "register")
	providerFlag, found := register.GetFlagCompletionFunc("provider")
	if !found {
		t.Fatal("--provider has no completion function")
	}
	names, _ = providerFlag(register, nil, "c")
	if strings.Join(names, ",") != "codex,claude,cursor,command" {
		t.Fatalf("--provider completion = %v", names)
	}
	workspaceFlag, found := register.GetFlagCompletionFunc("workspace")
	if !found {
		t.Fatal("--workspace has no completion function")
	}
	names, _ = workspaceFlag(register, nil, "")
	if strings.Join(names, ",") != "current,fix-auth" {
		t.Fatalf("--workspace completion = %v", names)
	}
	outputFlag, found := command.GetFlagCompletionFunc("output")
	if !found {
		t.Fatal("--output has no completion function")
	}
	names, _ = outputFlag(command, nil, "")
	if strings.Join(names, ",") != "text,json,ndjson" {
		t.Fatalf("--output completion = %v", names)
	}

	// A store read failure returns no candidate.
	broken := cli.New(cli.Options{ConfigDir: filepath.Join(root, "missing"), StateDir: filepath.Join(root, "missing"), DataDir: filepath.Join(root, "missing")})
	brokenShow := findCommand(broken, "templates", "show")
	if names, _ = brokenShow.ValidArgsFunction(brokenShow, nil, ""); len(names) != 0 {
		t.Fatalf("completion without a config directory = %v", names)
	}
}
