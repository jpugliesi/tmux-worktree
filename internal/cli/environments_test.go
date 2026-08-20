package cli_test

import (
	"bytes"
	"encoding/json"
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

// maintenanceOptions returns options for a new empty twt2 installation.
func maintenanceOptions(t *testing.T) cli.Options {
	t.Helper()
	root := t.TempDir()
	return cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}
}

// runCLI returns the standard output, the standard error, and the error of one
// twt2 command.
func runCLI(t *testing.T, options cli.Options, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	options.Stdout = &stdout
	options.Stderr = &stderr
	command := cli.New(options)
	command.SetArgs(args)
	err := command.Execute()
	return stdout.String(), stderr.String(), err
}

func maintenanceTemplate(name string, depth int) domain.Template {
	return domain.Template{
		Version: domain.TemplateVersion,
		Name:    name,
		Repositories: []domain.RepositorySpec{{
			Name:  "app",
			Clone: domain.CloneSpec{URL: "https://example.com/app.git", Depth: depth},
		}},
	}
}

func writeTemplateFile(t *testing.T, configDir string, template domain.Template) {
	t.Helper()
	data, err := store.EncodeTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	writeConfigFile(t, configDir, template.Name, string(data))
}

func writeConfigFile(t *testing.T, configDir, name, content string) {
	t.Helper()
	directory := filepath.Join(configDir, "templates")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name+".yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// saveEnvironmentRecord writes one Prepared Environment record and gives its
// root the requested number of bytes.
func saveEnvironmentRecord(t *testing.T, options cli.Options, id string, status domain.PreparedEnvironmentStatus, template domain.Template, size int, mutate func(*domain.PreparedEnvironment)) domain.PreparedEnvironment {
	t.Helper()
	digest, err := store.EnvironmentDigest(template)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	root := filepath.Join(options.DataDir, "projects", id)
	environment := domain.PreparedEnvironment{
		Version: domain.PreparedEnvironmentVersion, FormatVersion: domain.PreparationFormatVersion,
		ID: id, TemplateName: template.Name, TemplateDigest: digest, TemplateSnapshot: template,
		Status: status, Root: root, QueueToken: id + "-token", QueuedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	switch status {
	case domain.EnvironmentReady, domain.EnvironmentClaiming, domain.EnvironmentClaimed:
		for _, spec := range template.Repositories {
			environment.Repositories = append(environment.Repositories, domain.PreparedRepository{
				Name: spec.Name, CachePath: filepath.Join(options.DataDir, "caches", spec.Name),
				Path: filepath.Join(root, spec.Name), WindowName: spec.Name,
				BaseCommit: "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
			})
		}
		environment.Steps = []domain.SetupStep{{ID: "environment_root", Kind: domain.StepProjectRoot, Status: domain.StepSucceeded}}
	}
	if status == domain.EnvironmentReady {
		readyAt := now
		environment.ReadyAt = &readyAt
	}
	if mutate != nil {
		mutate(&environment)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "content"), bytes.Repeat([]byte("x"), size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.NewEnvironmentStore(options.StateDir).Save(environment); err != nil {
		t.Fatal(err)
	}
	return environment
}

// saveProjectRecord writes one Project record and gives its root some bytes.
func saveProjectRecord(t *testing.T, options cli.Options, id, name string, status domain.ProjectStatus, size int) domain.Project {
	t.Helper()
	now := time.Now().UTC()
	root := filepath.Join(options.DataDir, "projects", id)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "content"), bytes.Repeat([]byte("y"), size), 0o644); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{
		Version: domain.ProjectVersion, ID: id, Name: name, TemplateName: "example",
		Status: status, Root: root, CreatedAt: now, UpdatedAt: now,
		Repositories: []domain.ProjectRepository{{Name: "app", Path: filepath.Join(root, "app")}},
	}
	if status == domain.ProjectArchived {
		archived := now
		project.ArchivedAt = &archived
	}
	if err := store.NewProjectStore(options.StateDir).Save(project); err != nil {
		t.Fatal(err)
	}
	return project
}

func writePrepareLog(t *testing.T, stateDir, environmentID string) string {
	t.Helper()
	directory := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "prepare-"+environmentID+".log")
	if err := os.WriteFile(path, []byte("clone failed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStorageShowReplacesStorageStatus(t *testing.T) {
	options := maintenanceOptions(t)
	if _, _, err := runCLI(t, options, "storage", "show"); err != nil {
		t.Fatalf("storage show: %v", err)
	}
	_, _, err := runCLI(t, options, "storage", "status")
	if err == nil {
		t.Fatal("storage status did not fail")
	}
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("storage status code = %q, want invalid_usage", clierr.CodeOf(err))
	}
}

func TestStorageShowSeparatesActiveAndArchivedProjects(t *testing.T) {
	options := maintenanceOptions(t)
	saveProjectRecord(t, options, "active-id", "active-project", domain.ProjectActive, 4096)
	saveProjectRecord(t, options, "archived-id", "archived-project", domain.ProjectArchived, 2048)

	text, _, err := runCLI(t, options, "storage", "show")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Projects (active): 4.0 KiB (1)", "Projects (archived): 2.0 KiB (1)", "Worktrees: 2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("storage show text does not contain %q:\n%s", want, text)
		}
	}

	encoded, _, err := runCLI(t, options, "storage", "show", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		SchemaVersion int `json:"schemaVersion"`
		Storage       struct {
			ActiveProjectBytes   int64 `json:"activeProjectBytes"`
			ArchivedProjectBytes int64 `json:"archivedProjectBytes"`
			ActiveProjectCount   int   `json:"activeProjectCount"`
			ArchivedProjectCount int   `json:"archivedProjectCount"`
			ProjectCount         int   `json:"projectCount"`
		} `json:"storage"`
	}
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		t.Fatalf("decode storage JSON: %v\n%s", err, encoded)
	}
	if result.SchemaVersion != 1 || result.Storage.ActiveProjectBytes != 4096 || result.Storage.ArchivedProjectBytes != 2048 ||
		result.Storage.ActiveProjectCount != 1 || result.Storage.ArchivedProjectCount != 1 || result.Storage.ProjectCount != 2 {
		t.Fatalf("storage JSON = %s", encoded)
	}
}

func TestEnvironmentsListGroupsEnvironmentsByProjectTemplate(t *testing.T) {
	options := maintenanceOptions(t)
	template := maintenanceTemplate("example", 0)
	writeTemplateFile(t, options.ConfigDir, template)
	ready := saveEnvironmentRecord(t, options, "ready00000000000000000000000000ab", domain.EnvironmentReady, template, 3072, nil)
	failed := saveEnvironmentRecord(t, options, "failed0000000000000000000000000cd", domain.EnvironmentFailed, template, 1024, func(environment *domain.PreparedEnvironment) {
		environment.Failure = "clone failed"
	})
	log := writePrepareLog(t, options.StateDir, failed.ID)
	claimed := saveEnvironmentRecord(t, options, "claimed000000000000000000000000ef", domain.EnvironmentClaimed, template, 2048, func(environment *domain.PreparedEnvironment) {
		environment.ClaimReservation = &domain.EnvironmentClaim{
			Project: domain.Project{
				Version: domain.ProjectVersion, ID: "project-id", EnvironmentID: environment.ID, Name: "fix-auth",
				TemplateName: template.Name, Status: domain.ProjectActive,
			},
			ReservedAt: time.Now().UTC(),
		}
	})

	text, _, err := runCLI(t, options, "environments", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"example (3 environments,",
		ready.ID[:8] + "  ready",
		"3.0 KiB  base a1b2c3d",
		failed.ID[:8] + "  failed",
		"log: " + log,
		claimed.ID[:8] + "  claimed",
		"Project fix-auth (active)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("environments list text does not contain %q:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "└─") || !strings.Contains(text, "├─") {
		t.Fatalf("environments list text is not a tree:\n%s", text)
	}

	// A live Project record replaces the claim reservation snapshot.
	saveProjectRecord(t, options, "project-id", "fix-auth", domain.ProjectArchived, 512)
	text, _, err = runCLI(t, options, "environments", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Project fix-auth (archived)") {
		t.Fatalf("environments list does not use the live Project status:\n%s", text)
	}

	encoded, _, err := runCLI(t, options, "environments", "list", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		SchemaVersion int `json:"schemaVersion"`
		TotalCount    int `json:"totalCount"`
		Environments  []struct {
			ID          string            `json:"id"`
			Template    string            `json:"template"`
			Status      string            `json:"status"`
			ReadyAt     string            `json:"readyAt"`
			CreatedAt   string            `json:"createdAt"`
			Bytes       int64             `json:"bytes"`
			BaseCommits map[string]string `json:"baseCommits"`
			Failure     string            `json:"failure"`
			Log         string            `json:"log"`
			Project     *struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"project"`
		} `json:"environments"`
	}
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		t.Fatalf("decode environments JSON: %v\n%s", err, encoded)
	}
	if result.SchemaVersion != 1 || result.TotalCount != 3 || len(result.Environments) != 3 {
		t.Fatalf("environments JSON = %s", encoded)
	}
	found := map[string]bool{}
	for _, environment := range result.Environments {
		found[environment.Status] = true
		if environment.Template != "example" || environment.CreatedAt == "" {
			t.Fatalf("environment JSON = %+v", environment)
		}
		switch environment.Status {
		case "ready":
			if environment.Bytes != 3072 || environment.ReadyAt == "" || environment.BaseCommits["app"] != "a1b2c3d" {
				t.Fatalf("ready environment JSON = %+v", environment)
			}
		case "failed":
			if environment.Failure != "clone failed" || environment.Log != log {
				t.Fatalf("failed environment JSON = %+v", environment)
			}
		case "claimed":
			if environment.Project == nil || environment.Project.Name != "fix-auth" || environment.Project.Status != "archived" {
				t.Fatalf("claimed environment JSON = %+v", environment)
			}
		}
	}
	if len(found) != 3 {
		t.Fatalf("environments JSON statuses = %v", found)
	}

	limited, _, err := runCLI(t, options, "environments", "list", "--limit", "1", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(limited), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Environments) != 1 || result.TotalCount != 3 {
		t.Fatalf("limited environments JSON = %s", limited)
	}
}

func TestEnvironmentsListMarksObsoleteOnlyWhenTheTemplateLoads(t *testing.T) {
	options := maintenanceOptions(t)
	template := maintenanceTemplate("example", 0)
	ready := saveEnvironmentRecord(t, options, "obsolete00000000000000000000000ab", domain.EnvironmentReady, template, 1024, nil)
	// The saved Project Template now clones with a different depth, so the
	// prepared worktrees are obsolete.
	writeTemplateFile(t, options.ConfigDir, maintenanceTemplate("example", 1))

	text, _, err := runCLI(t, options, "environments", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, ready.ID[:8]+"  obsolete") {
		t.Fatalf("environments list does not mark the obsolete environment:\n%s", text)
	}

	// twt2 cannot compare an invalid Project Template, so the status stays.
	writeConfigFile(t, options.ConfigDir, "example", "version: 1\nname: example\nrepositories: [\n")
	text, _, err = runCLI(t, options, "environments", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, ready.ID[:8]+"  ready") {
		t.Fatalf("environments list changed the status of an invalid Project Template:\n%s", text)
	}
}

func TestEnvironmentsShowAcceptsAnIDPrefix(t *testing.T) {
	options := maintenanceOptions(t)
	template := maintenanceTemplate("example", 0)
	writeTemplateFile(t, options.ConfigDir, template)
	environment := saveEnvironmentRecord(t, options, "showable00000000000000000000000ab", domain.EnvironmentReady, template, 1024, nil)

	text, _, err := runCLI(t, options, "environments", "show", environment.ID[:8])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Prepared Environment: " + environment.ID,
		"Template: example",
		"Status: ready",
		"Size: 1.0 KiB",
		"app\ta1b2c3d",
		"Steps: 1 of 1 are complete",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("environments show text does not contain %q:\n%s", want, text)
		}
	}

	encoded, _, err := runCLI(t, options, "environments", "show", environment.ID, "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		SchemaVersion int `json:"schemaVersion"`
		Environment   struct {
			ID    string `json:"id"`
			Steps []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"steps"`
		} `json:"environment"`
	}
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		t.Fatalf("decode environment JSON: %v\n%s", err, encoded)
	}
	if result.SchemaVersion != 1 || result.Environment.ID != environment.ID || len(result.Environment.Steps) != 1 {
		t.Fatalf("environment JSON = %s", encoded)
	}

	_, _, err = runCLI(t, options, "environments", "show", "nothing")
	if err == nil {
		t.Fatal("environments show with an unknown ID did not fail")
	}
	if clierr.CodeOf(err) != clierr.NotFound {
		t.Fatalf("environments show code = %q, want not_found", clierr.CodeOf(err))
	}
}

func TestDoctorWarnsAboutAFailedEnvironmentAndStaysHealthy(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	options := maintenanceOptions(t)
	template := maintenanceTemplate("example", 0)
	writeTemplateFile(t, options.ConfigDir, template)
	failed := saveEnvironmentRecord(t, options, "failing000000000000000000000000ab", domain.EnvironmentFailed, template, 1024, func(environment *domain.PreparedEnvironment) {
		environment.Failure = "clone failed"
	})
	log := writePrepareLog(t, options.StateDir, failed.ID)

	text, _, err := runCLI(t, options, "doctor")
	if err != nil {
		t.Fatalf("doctor with a failed Prepared Environment returned an error: %v", err)
	}
	want := "warn\tenvironment:" + failed.ID + "\tPrepared Environment failed: clone failed. See " + log + ". Run 'twt2 storage clean --apply' to remove it."
	if !strings.Contains(text, want) {
		t.Fatalf("doctor text does not contain %q:\n%s", want, text)
	}

	encoded, _, err := runCLI(t, options, "doctor", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, `"healthy":true`) || !strings.Contains(encoded, `"status":"warn"`) {
		t.Fatalf("doctor JSON = %s", encoded)
	}
}

func TestStorageCleanReportsAnEmptyPlan(t *testing.T) {
	options := maintenanceOptions(t)
	text, _, err := runCLI(t, options, "storage", "clean")
	if err != nil {
		t.Fatal(err)
	}
	if text != "Nothing to clean.\n" {
		t.Fatalf("storage clean text = %q", text)
	}
	encoded, _, err := runCLI(t, options, "storage", "clean", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(encoded, `"applied":false`) {
		t.Fatalf("storage clean JSON = %s", encoded)
	}
}

func TestStorageCleanKeepsEnvironmentsOfAnInvalidProjectTemplate(t *testing.T) {
	options := maintenanceOptions(t)
	template := maintenanceTemplate("example", 0)
	environment := saveEnvironmentRecord(t, options, "keepable00000000000000000000000ab", domain.EnvironmentReady, template, 1024, nil)
	writeConfigFile(t, options.ConfigDir, "example", "version: 1\nname: example\nrepositories: [\n")

	text, warnings, err := runCLI(t, options, "storage", "clean")
	if err != nil {
		t.Fatal(err)
	}
	if text != "Nothing to clean.\n" {
		t.Fatalf("storage clean removed the environments of an invalid Project Template: %q", text)
	}
	if warnings != "Warning: Project Template \"example\" is not valid. twt2 kept its Prepared Environments.\n" {
		t.Fatalf("storage clean warning = %q", warnings)
	}
	if _, err := store.NewEnvironmentStore(options.StateDir).Find(environment.ID); err != nil {
		t.Fatalf("find the kept Prepared Environment: %v", err)
	}
}

func TestStorageCleanPlanCountsEnvironmentBytes(t *testing.T) {
	options := maintenanceOptions(t)
	template := maintenanceTemplate("example", 0)
	writeTemplateFile(t, options.ConfigDir, template)
	failed := saveEnvironmentRecord(t, options, "sizeable00000000000000000000000ab", domain.EnvironmentFailed, template, 4096, func(environment *domain.PreparedEnvironment) {
		environment.Failure = "clone failed"
	})

	text, _, err := runCLI(t, options, "storage", "clean")
	if err != nil {
		t.Fatal(err)
	}
	want := "Remove failed Prepared Environment \"" + failed.ID + "\" for Project Template \"example\" (4.0 KiB)"
	if !strings.Contains(text, want) {
		t.Fatalf("storage clean text does not contain %q:\n%s", want, text)
	}

	encoded, _, err := runCLI(t, options, "storage", "clean", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Plan struct {
			Environments []struct {
				ID    string `json:"id"`
				Bytes int64  `json:"bytes"`
			} `json:"environments"`
		} `json:"plan"`
	}
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		t.Fatalf("decode cleanup JSON: %v\n%s", err, encoded)
	}
	if len(result.Plan.Environments) != 1 || result.Plan.Environments[0].Bytes != 4096 {
		t.Fatalf("cleanup JSON = %s", encoded)
	}
}

func TestStorageCleanRemovesOrphanAgentRecords(t *testing.T) {
	options := maintenanceOptions(t)
	saveProjectRecord(t, options, "live-project", "live-project", domain.ProjectActive, 512)
	now := time.Now().UTC()
	agents := store.NewAgentStore(options.StateDir)
	if err := agents.Save(domain.AgentSession{
		Version: domain.AgentVersion, ID: "live-agent", ProjectID: "live-project", Provider: "codex",
		Label: "review", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := agents.Save(domain.AgentSession{
		Version: domain.AgentVersion, ID: "orphan-agent", ProjectID: "removed-project", Provider: "codex",
		Label: "old", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	text, _, err := runCLI(t, options, "storage", "clean")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Remove orphan Agent Session record \"orphan-agent\" for missing Project \"removed-project\"") ||
		strings.Contains(text, "live-agent") {
		t.Fatalf("storage clean plan = %q", text)
	}

	applied, _, err := runCLI(t, options, "storage", "clean", "--apply")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(applied, "1 Agent Session records") {
		t.Fatalf("storage clean --apply output = %q", applied)
	}
	remaining, err := agents.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != "live-agent" {
		t.Fatalf("Agent Session records after cleanup = %+v", remaining)
	}
}
