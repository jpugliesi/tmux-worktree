package cli_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// finishFixture prepares a config directory with the "example" template, a
// private tmux server, and base options.
func finishFixture(t *testing.T) cli.Options {
	t.Helper()
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
	socket := fmt.Sprintf("twt2-test-%d", time.Now().UnixNano())
	t.Cleanup(func() { exec.Command("tmux", "-L", socket, "kill-server").Run() })
	return cli.Options{ConfigDir: configDir, StateDir: filepath.Join(root, "state"), DataDir: filepath.Join(root, "data"), TmuxSocket: socket}
}

func TestFinishOutsideSessionSupportsDryRunKeepAndFullRemoval(t *testing.T) {
	options := finishFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT2_PROJECT_ID", "")
	executeWithOptions(t, options, nil, "projects", "create", "finish-me", "--template", "example", "--no-open")
	project, err := store.NewProjectStore(options.StateDir).Find("finish-me")
	if err != nil {
		t.Fatal(err)
	}

	dryRun := executeWithOptions(t, options, nil, "finish", "finish-me", "--dry-run")
	for _, want := range []string{"Archive of Project \"finish-me\" is valid.", "Removal plan for Project \"finish-me\":", "remove_worktree"} {
		if !strings.Contains(dryRun, want) {
			t.Fatalf("finish dry-run output does not contain %q: %s", want, dryRun)
		}
	}
	if strings.Contains(dryRun, "Blocked:") || strings.Contains(dryRun, "not archived") {
		t.Fatalf("finish dry-run of a clean Project shows blockers: %s", dryRun)
	}
	unchanged, err := store.NewProjectStore(options.StateDir).Find(project.ID)
	if err != nil || unchanged.Status != domain.ProjectActive {
		t.Fatalf("finish dry-run changed the Project: status=%q error=%v", unchanged.Status, err)
	}
	if _, err := os.Stat(project.Root); err != nil {
		t.Fatalf("finish dry-run changed Project data: %v", err)
	}

	keepOutput := executeWithOptions(t, options, nil, "finish", "finish-me", "--keep")
	if !strings.Contains(keepOutput, "Archived Project \"finish-me\"") || strings.Contains(keepOutput, "Removed") {
		t.Fatalf("finish --keep output = %q", keepOutput)
	}
	archived, err := store.NewProjectStore(options.StateDir).Find(project.ID)
	if err != nil || archived.Status != domain.ProjectArchived {
		t.Fatalf("Project after finish --keep: status=%q error=%v", archived.Status, err)
	}
	if _, err := os.Stat(project.Root); err != nil {
		t.Fatalf("finish --keep removed Project data: %v", err)
	}

	output := executeWithOptions(t, options, nil, "finish", "finish-me")
	if !strings.Contains(output, "Archived Project \"finish-me\"") || !strings.Contains(output, "Removed Project \"finish-me\". Reclaimed ") {
		t.Fatalf("finish output = %q", output)
	}
	if _, err := os.Stat(project.Root); !os.IsNotExist(err) {
		t.Fatalf("Project root still exists after finish: %v", err)
	}
	if _, err := store.NewProjectStore(options.StateDir).Find(project.ID); err == nil {
		t.Fatal("Project record still exists after finish")
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "=finish-me").Run(); err == nil {
		t.Fatal("Project tmux session still exists after finish")
	}
}

func TestFinishLeavesABlockedProjectArchived(t *testing.T) {
	options := finishFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT2_PROJECT_ID", "")
	executeWithOptions(t, options, nil, "projects", "create", "block-me", "--template", "example", "--no-open")
	project, err := store.NewProjectStore(options.StateDir).Find("block-me")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project.Root, "app", "unsaved.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	blockedOptions := options
	blockedOptions.Stdout, blockedOptions.Stderr = &stdout, &stderr
	command := cli.New(blockedOptions)
	command.SetArgs([]string{"finish", "block-me"})
	err = command.Execute()
	if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("blocked finish error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "Archived Project \"block-me\"") || !strings.Contains(output, "Blocked:") || !strings.Contains(output, "stays archived") {
		t.Fatalf("blocked finish output = %q", output)
	}
	archived, findErr := store.NewProjectStore(options.StateDir).Find(project.ID)
	if findErr != nil || archived.Status != domain.ProjectArchived {
		t.Fatalf("blocked finish Project: status=%q error=%v", archived.Status, findErr)
	}
	if _, err := os.Stat(filepath.Join(project.Root, "app", "unsaved.txt")); err != nil {
		t.Fatalf("blocked finish changed Project data: %v", err)
	}
}

func TestFinishAndArchiveRelocateInsideTheProjectSession(t *testing.T) {
	options := finishFixture(t)
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TWT2_PROJECT_ID", "")
	for _, name := range []string{"alpha", "beta", "gamma"} {
		executeWithOptions(t, options, nil, "projects", "create", name, "--template", "example", "--no-open")
	}
	projects := store.NewProjectStore(options.StateDir)
	alpha, err := projects.Find("alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := projects.Find("beta")
	if err != nil {
		t.Fatal(err)
	}
	gamma, err := projects.Find("gamma")
	if err != nil {
		t.Fatal(err)
	}

	gammaPane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-panes", "-t", "=gamma", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", gammaPane)

	// JSON output cannot relocate the tmux client.
	var stdout, stderr bytes.Buffer
	jsonOptions := options
	jsonOptions.Stdout, jsonOptions.Stderr = &stdout, &stderr
	jsonCommand := cli.New(jsonOptions)
	jsonCommand.SetArgs([]string{"finish", "--output", "json"})
	err = jsonCommand.Execute()
	if err == nil || !strings.Contains(err.Error(), "text output") {
		t.Fatalf("finish JSON inside the Project session error = %v", err)
	}
	if unchanged, findErr := projects.Find(gamma.ID); findErr != nil || unchanged.Status != domain.ProjectActive {
		t.Fatalf("refused JSON finish changed the Project: %+v error=%v", unchanged, findErr)
	}

	// Finish relocates to the most recently updated other active Project.
	var destinations []string
	options.FinishRelocate = func(destinationProjectID string) error {
		destinations = append(destinations, destinationProjectID)
		return nil
	}
	output := executeWithOptions(t, options, nil, "finish")
	if !strings.Contains(output, "Removed Project \"gamma\"") {
		t.Fatalf("relocated finish output = %q", output)
	}
	if _, err := projects.Find(gamma.ID); err == nil {
		t.Fatal("relocated finish kept the Project record")
	}
	if _, err := os.Stat(gamma.Root); !os.IsNotExist(err) {
		t.Fatalf("relocated finish kept the Project root: %v", err)
	}

	// Archive relocates too and keeps the Project data.
	betaPane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-panes", "-t", "=beta", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", betaPane)
	archiveOutput := executeWithOptions(t, options, nil, "archive")
	if !strings.Contains(archiveOutput, "Archived Project \"beta\"") {
		t.Fatalf("relocated archive output = %q", archiveOutput)
	}
	archivedBeta, err := projects.Find(beta.ID)
	if err != nil || archivedBeta.Status != domain.ProjectArchived {
		t.Fatalf("relocated archive Project: status=%q error=%v", archivedBeta.Status, err)
	}
	if _, err := os.Stat(beta.Root); err != nil {
		t.Fatalf("relocated archive removed Project data: %v", err)
	}

	// Without another active Project, finish reports an empty destination.
	alphaPane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "list-panes", "-t", "=alpha", "-F", "#{pane_id}")
	t.Setenv("TMUX_PANE", alphaPane)
	lastOutput := executeWithOptions(t, options, nil, "finish")
	if !strings.Contains(lastOutput, "Removed Project \"alpha\"") {
		t.Fatalf("last finish output = %q", lastOutput)
	}
	if _, err := os.Stat(alpha.Root); !os.IsNotExist(err) {
		t.Fatalf("last finish kept the Project root: %v", err)
	}

	want := []string{beta.ID, alpha.ID, ""}
	if strings.Join(destinations, "\n") != strings.Join(want, "\n") {
		t.Fatalf("relocation destinations = %v, want %v", destinations, want)
	}
}

func TestFinishWorkerArchivesAndRemovesFromAnotherSession(t *testing.T) {
	options := finishFixture(t)
	t.Setenv("TWT2_PROJECT_ID", "")
	executeWithOptions(t, options, nil, "projects", "create", "worker-src", "--template", "example", "--no-open")
	executeWithOptions(t, options, nil, "projects", "create", "worker-dest", "--template", "example", "--no-open")
	source, err := store.NewProjectStore(options.StateDir).Find("worker-src")
	if err != nil {
		t.Fatal(err)
	}
	helperPane := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "new-window", "-d", "-P", "-F", "#{pane_id}", "-t", "=worker-dest", "-n", "finish-helper", "--", "sleep", "60")
	t.Setenv("TMUX_PANE", helperPane)

	timeoutOptions := options
	timeoutOptions.QuickCreateWaitTimeout = 50 * time.Millisecond
	err = cli.RunFinishWorker(timeoutOptions, []string{source.ID, "keep=false", "allow-unpublished=false", "-", "twt2-finish-timeout", "no-client"})
	if err == nil || !strings.Contains(err.Error(), "finish signal timed out") || !strings.Contains(err.Error(), "twt2 finish "+source.ID) {
		t.Fatalf("finish worker timeout = %v", err)
	}
	unchanged, err := store.NewProjectStore(options.StateDir).Find(source.ID)
	if err != nil || unchanged.Status != domain.ProjectActive {
		t.Fatalf("Project after worker timeout: status=%q error=%v", unchanged.Status, err)
	}
	windowName := runCommand(t, "", "tmux", "-L", options.TmuxSocket, "display-message", "-p", "-t", helperPane, "#{window_name}")
	if windowName != "finish-failed" {
		t.Fatalf("worker timeout window = %q", windowName)
	}
	runCommand(t, "", "tmux", "-L", options.TmuxSocket, "rename-window", "-t", helperPane, "finish-helper")

	channel := "twt2-finish-worker-test"
	signalResult := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		signalResult <- exec.Command("tmux", "-L", options.TmuxSocket, "wait-for", "-S", channel).Run()
	}()
	if err := cli.RunFinishWorker(options, []string{source.ID, "keep=false", "allow-unpublished=false", "-", channel, "no-client"}); err != nil {
		t.Fatalf("run finish worker: %v", err)
	}
	if err := <-signalResult; err != nil {
		t.Fatalf("signal finish worker: %v", err)
	}
	if _, err := store.NewProjectStore(options.StateDir).Find(source.ID); err == nil {
		t.Fatal("finish worker kept the Project record")
	}
	if _, err := os.Stat(source.Root); !os.IsNotExist(err) {
		t.Fatalf("finish worker kept the Project root: %v", err)
	}
	if err := exec.Command("tmux", "-L", options.TmuxSocket, "has-session", "-t", "=worker-src").Run(); err == nil {
		t.Fatal("finish worker kept the source tmux session")
	}
}
