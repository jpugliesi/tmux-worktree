package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// applyRequest runs one apply request and returns standard output. It fails
// the test when apply returns an error.
func applyRequest(t *testing.T, options cli.Options, request string, extra ...string) string {
	t.Helper()
	args := append([]string{"apply", "--stdin", "--output", "json"}, extra...)
	stdout, stderr, err := executeCollectingInput(t, options, strings.NewReader(request), args...)
	if err != nil {
		t.Fatalf("apply %s: %v\nstderr: %s", request, err, stderr)
	}
	return stdout
}

// applyRequestError runs one apply request that must fail and returns the
// error.
func applyRequestError(t *testing.T, options cli.Options, request string, extra ...string) error {
	t.Helper()
	args := append([]string{"apply", "--stdin", "--output", "json"}, extra...)
	_, _, err := executeCollectingInput(t, options, strings.NewReader(request), args...)
	if err == nil {
		t.Fatalf("apply %s did not fail", request)
	}
	return err
}

func TestApplyEditsAndRemovesProjectTemplates(t *testing.T) {
	root := t.TempDir()
	options := cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}
	templatePath := filepath.Join(root, "config", "templates", "product.yaml")
	applyRequest(t, options, `{"operation":"templates.create","template":{"name":"product"}}`)
	applyRequest(t, options, `{"operation":"templates.repos.add","template":{"name":"product","repository":{"name":"web","url":"https://example.com/web.git"}}}`)
	applyRequest(t, options, `{"operation":"templates.init.set","template":{"name":"product","cwd":".","command":["make","setup"]}}`)
	applyRequest(t, options, `{"operation":"templates.init.set","template":{"name":"product","repo":"web","command":["npm","install"]}}`)

	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	document := string(data)
	for _, want := range []string{"make", "setup", "npm", "install"} {
		if !strings.Contains(document, want) {
			t.Fatalf("Project Template after templates.init.set misses %q:\n%s", want, document)
		}
	}

	// Project initialization needs a working directory, and repository
	// initialization always runs in the repository worktree.
	if err := applyRequestError(t, options, `{"operation":"templates.init.set","template":{"name":"product","command":["make"]}}`); !strings.Contains(err.Error(), "template.cwd") {
		t.Fatalf("templates.init.set without cwd = %v", err)
	}
	if err := applyRequestError(t, options, `{"operation":"templates.init.set","template":{"name":"product","repo":"web","cwd":".","command":["make"]}}`); !strings.Contains(err.Error(), "template.repo") {
		t.Fatalf("templates.init.set with repo and cwd = %v", err)
	}

	prepare := applyRequest(t, options, `{"operation":"templates.prepare","template":{"name":"product"}}`, "--dry-run")
	if !strings.Contains(prepare, `"operation":"templates.prepare"`) || !strings.Contains(prepare, `"status":"valid"`) {
		t.Fatalf("apply templates.prepare --dry-run = %s", prepare)
	}

	removed := applyRequest(t, options, `{"operation":"templates.repos.remove","template":{"name":"product","repo":"web"}}`)
	if !strings.Contains(removed, `"operation":"templates.repos.remove"`) || !strings.Contains(removed, `"status":"applied"`) {
		t.Fatalf("apply templates.repos.remove = %s", removed)
	}
	data, err = os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "web") {
		t.Fatalf("Project Template keeps the removed repository:\n%s", data)
	}
	if err := applyRequestError(t, options, `{"operation":"templates.repos.remove","template":{"name":"product","repo":"web"}}`); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("second templates.repos.remove = %v (code %q)", err, clierr.CodeOf(err))
	}

	// A Project record that names the Project Template blocks the removal.
	now := time.Now().UTC()
	if err := store.NewProjectStore(options.StateDir).Save(domain.Project{
		Version: domain.ProjectVersion, ID: "project-user-id", Name: "user", TemplateName: "product",
		Status: domain.ProjectActive, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyRequestError(t, options, `{"operation":"templates.remove","template":{"name":"product"}}`); clierr.CodeOf(err) != clierr.PreconditionFailed {
		t.Fatalf("templates.remove of a used Project Template = %v (code %q)", err, clierr.CodeOf(err))
	}
	if err := store.NewProjectStore(options.StateDir).Delete("project-user-id"); err != nil {
		t.Fatal(err)
	}
	deleted := applyRequest(t, options, `{"operation":"templates.remove","template":{"name":"product"}}`)
	if !strings.Contains(deleted, `"operation":"templates.remove"`) || !strings.Contains(deleted, `"status":"applied"`) {
		t.Fatalf("apply templates.remove = %s", deleted)
	}
	if _, err := os.Stat(templatePath); !os.IsNotExist(err) {
		t.Fatalf("apply templates.remove kept the file: %v", err)
	}
}

func TestApplyRetriesProjectSetupAndRefusesToAttach(t *testing.T) {
	root := t.TempDir()
	options := cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}
	now := time.Now().UTC()
	projects := store.NewProjectStore(options.StateDir)
	if err := projects.Save(domain.Project{
		Version: domain.ProjectVersion, ID: "project-retry-id", Name: "retry-me", TemplateName: "example",
		Status: domain.ProjectActive, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	retry := applyRequest(t, options, `{"operation":"projects.setup.retry","project":{"reference":"retry-me"}}`, "--dry-run")
	if !strings.Contains(retry, `"operation":"projects.setup.retry"`) || !strings.Contains(retry, `"status":"valid"`) {
		t.Fatalf("apply projects.setup.retry --dry-run = %s", retry)
	}
	if err := applyRequestError(t, options, `{"operation":"projects.setup.retry","project":{"reference":"no-such-project"}}`, "--dry-run"); clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("projects.setup.retry of an unknown Project = %v (code %q)", err, clierr.CodeOf(err))
	}

	archived := now
	if err := projects.Save(domain.Project{
		Version: domain.ProjectVersion, ID: "project-archived-id", Name: "archived-one", TemplateName: "example",
		Status: domain.ProjectArchived, CreatedAt: now, UpdatedAt: now, ArchivedAt: &archived,
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyRequestError(t, options, `{"operation":"projects.setup.retry","project":{"reference":"archived-one"}}`, "--dry-run"); !strings.Contains(err.Error(), "archived") {
		t.Fatalf("projects.setup.retry of an archived Project = %v", err)
	}

	// Apply repairs the tmux session, but it never attaches a tmux client.
	if err := applyRequestError(t, options, `{"operation":"projects.open","project":{"reference":"retry-me","noAttach":false}}`, "--dry-run"); !strings.Contains(err.Error(), "noAttach") {
		t.Fatalf("projects.open with noAttach false = %v", err)
	}
	open := applyRequest(t, options, `{"operation":"projects.open","project":{"reference":"retry-me","noAttach":true}}`, "--dry-run")
	if !strings.Contains(open, `"operation":"projects.open"`) || !strings.Contains(open, `"status":"valid"`) {
		t.Fatalf("apply projects.open --dry-run = %s", open)
	}
}

func TestApplyPlansAndAppliesStorageClean(t *testing.T) {
	options := maintenanceOptions(t)
	saveProjectRecord(t, options, "live-project", "live-project", domain.ProjectActive, 512)
	now := time.Now().UTC()
	agents := store.NewAgentStore(options.StateDir)
	if err := agents.Save(domain.AgentSession{
		Version: domain.AgentVersion, ID: "orphan-agent", ProjectID: "removed-project", Provider: "codex",
		Label: "old", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	var plan struct {
		Applied bool `json:"applied"`
		Plan    struct {
			Agents []struct {
				ID string `json:"id"`
			} `json:"agents"`
		} `json:"plan"`
	}
	output := applyRequest(t, options, `{"operation":"storage.clean","storage":{}}`)
	if err := json.Unmarshal([]byte(output), &plan); err != nil {
		t.Fatalf("decode storage.clean plan: %v\n%s", err, output)
	}
	if plan.Applied || len(plan.Plan.Agents) != 1 || plan.Plan.Agents[0].ID != "orphan-agent" {
		t.Fatalf("apply storage.clean plan = %s", output)
	}
	if _, err := agents.Find("orphan-agent"); err != nil {
		t.Fatalf("the plan removed the Agent Session record: %v", err)
	}

	// A dry run keeps the plan, even with apply set.
	dry := applyRequest(t, options, `{"operation":"storage.clean","storage":{"apply":true}}`, "--dry-run")
	if !strings.Contains(dry, `"applied":false`) {
		t.Fatalf("apply storage.clean --dry-run = %s", dry)
	}
	if _, err := agents.Find("orphan-agent"); err != nil {
		t.Fatalf("the dry run removed the Agent Session record: %v", err)
	}

	applied := applyRequest(t, options, `{"operation":"storage.clean","storage":{"apply":true}}`)
	if !strings.Contains(applied, `"applied":true`) {
		t.Fatalf("apply storage.clean with apply = %s", applied)
	}
	if _, err := agents.Find("orphan-agent"); err == nil {
		t.Fatal("apply storage.clean kept the orphan Agent Session record")
	}
	remaining, err := agents.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("Agent Session records after the cleanup = %+v", remaining)
	}
}

func TestApplyEditsAndScaffoldsTickets(t *testing.T) {
	options, home := ticketTestOptions(t)
	initialized := applyRequest(t, options, `{"operation":"tickets.init"}`)
	if !strings.Contains(initialized, `"operation":"tickets.init"`) || !strings.Contains(initialized, `"status":"applied"`) {
		t.Fatalf("apply tickets.init = %s", initialized)
	}
	if _, err := os.Stat(filepath.Join(home, "index.md")); err != nil {
		t.Fatalf("apply tickets.init wrote no index: %v", err)
	}
	applyRequest(t, options, `{"operation":"tickets.create","ticket":{"title":"edit me","body":"First body."}}`)

	edited := applyRequest(t, options, `{"operation":"tickets.edit","ticket":{"reference":"edit-me","body":"Second body.\n"}}`)
	if !strings.Contains(edited, `"operation":"tickets.edit"`) || !strings.Contains(edited, `"status":"applied"`) {
		t.Fatalf("apply tickets.edit = %s", edited)
	}
	content := readTicketFile(t, filepath.Join(home, "edit-me.md"))
	if !strings.Contains(content, "Second body.") || strings.Contains(content, "First body.") {
		t.Fatalf("ticket file after tickets.edit:\n%s", content)
	}
	if err := applyRequestError(t, options, `{"operation":"tickets.edit","ticket":{"reference":"edit-me"}}`); !strings.Contains(err.Error(), "ticket.body") {
		t.Fatalf("tickets.edit without a body = %v", err)
	}
}

func TestApplyDrivesAgentSessionsOfAProject(t *testing.T) {
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
	template := fmt.Sprintf("version: 1\nname: agentwork\nrepositories:\n  - name: app\n    clone:\n      url: %s\n", source)
	if err := os.WriteFile(filepath.Join(configDir, "templates", "agentwork.yaml"), []byte(template), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("twt-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", socket, "kill-server").Run() })
	options := cli.Options{
		ConfigDir: configDir, StateDir: filepath.Join(root, "state"),
		DataDir: filepath.Join(root, "data"), TmuxSocket: socket,
	}
	executeWithOptions(t, options, nil, "projects", "create", "agentwork", "--template", "agentwork", "--no-open")

	registration := applyRequest(t, options,
		`{"operation":"agents.register","agent":{"project":"agentwork","provider":"command","label":"sleeper","resumeCommand":["sleep","60"]}}`)
	var registered struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(registration), &registered); err != nil || registered.ID == "" {
		t.Fatalf("decode agents.register: %v\n%s", err, registration)
	}

	// A pane of a terminal has no meaning for apply.
	if err := applyRequestError(t, options,
		`{"operation":"agents.register","agent":{"project":"agentwork","provider":"command","pane":"current"}}`); !strings.Contains(err.Error(), "pane") {
		t.Fatalf("agents.register with the pane value current = %v", err)
	}

	resumed := applyRequest(t, options, fmt.Sprintf(`{"operation":"agents.resume","agent":{"reference":%q}}`, registered.ID))
	if !strings.Contains(resumed, registered.ID) {
		t.Fatalf("apply agents.resume = %s", resumed)
	}

	if err := applyRequestError(t, options, fmt.Sprintf(`{"operation":"agents.send","agent":{"reference":%q,"project":"agentwork"}}`, registered.ID)); !strings.Contains(err.Error(), "agent.text") {
		t.Fatalf("agents.send without text = %v", err)
	}
	sent := applyRequest(t, options, fmt.Sprintf(`{"operation":"agents.send","agent":{"reference":%q,"project":"agentwork","text":"look at the tests\n"}}`, registered.ID))
	if !strings.Contains(sent, `"status":"sent"`) {
		t.Fatalf("apply agents.send = %s", sent)
	}

	// Only a provider with verifiable transcripts accepts a link.
	reader := applyRequest(t, options,
		`{"operation":"agents.register","agent":{"project":"agentwork","provider":"codex","label":"reader","resumeCommand":["codex","resume","session-one"]}}`)
	var readerAgent struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(reader), &readerAgent); err != nil || readerAgent.ID == "" {
		t.Fatalf("decode the second agents.register: %v\n%s", err, reader)
	}
	linked := applyRequest(t, options, fmt.Sprintf(`{"operation":"agents.transcript.link","agent":{"reference":%q,"project":"agentwork","session":"session-two"}}`, readerAgent.ID))
	if !strings.Contains(linked, `"operation":"agents.transcript.link"`) {
		t.Fatalf("apply agents.transcript.link = %s", linked)
	}
	stored, err := store.NewAgentStore(options.StateDir).Find(readerAgent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ProviderSessionID != "session-two" {
		t.Fatalf("provider session ID after the link = %q", stored.ProviderSessionID)
	}

	removed := applyRequest(t, options, fmt.Sprintf(`{"operation":"agents.rm","agent":{"reference":%q,"project":"agentwork"}}`, registered.ID))
	if !strings.Contains(removed, `"operation":"agents.rm"`) || !strings.Contains(removed, `"status":"applied"`) {
		t.Fatalf("apply agents.rm = %s", removed)
	}
	if _, err := store.NewAgentStore(options.StateDir).Find(registered.ID); err == nil {
		t.Fatal("apply agents.rm kept the Agent Session record")
	}
}

func TestApplyRefusesInteractiveOperations(t *testing.T) {
	options := maintenanceOptions(t)
	err := applyRequestError(t, options, `{"operation":"projects.switch","project":{"reference":"any"}}`)
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("unsupported operation code = %q", clierr.CodeOf(err))
	}
	for _, name := range []string{
		"templates.remove", "templates.prepare", "templates.init.set", "templates.repos.remove",
		"projects.open", "projects.setup.retry", "agents.send", "agents.resume", "agents.rm",
		"agents.transcript.link", "storage.clean", "tickets.init", "tickets.edit",
	} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("the unsupported operation error does not list %q: %v", name, err)
		}
	}
	hint := clierr.HintOf(err)
	for _, excluded := range []string{"twt start", "twt tickets start", "twt tickets home", "twt switch", "twt done", "twt archive", "twt templates edit", "twt agents focus", "twt agents register --pane current"} {
		if !strings.Contains(hint, excluded) {
			t.Fatalf("the unsupported operation hint does not name %q: %s", excluded, hint)
		}
	}
}
