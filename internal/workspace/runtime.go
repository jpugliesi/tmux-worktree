package workspace

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

func (s *Service) runInitialize(p domain.Workspace, directory string, init *domain.InitializeSpec) error {
	if init == nil || len(init.Command) == 0 {
		return fmt.Errorf("initialization command is empty")
	}
	command := exec.Command(init.Command[0], init.Command[1:]...)
	command.Dir = directory
	command.Env = append(os.Environ(), workspaceEnvironment(p)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run initialization in %q: %w: %s", directory, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func runInitializationProcess(directory string, argv, environment []string, activityFile *os.File) error {
	if len(argv) == 0 {
		return fmt.Errorf("initialization command is empty")
	}
	command := exec.Command(argv[0], argv[1:]...)
	command.Dir = directory
	command.Env = environment
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if activityFile != nil {
		command.ExtraFiles = []*os.File{activityFile}
	}
	// Tee the initialization output to stderr so a long init is observable:
	// the background preparation worker's stderr lands in its log file, and
	// an inline preparation shows the output on the caller's terminal
	// without touching the JSON on stdout. The buffer keeps the copy for
	// error messages.
	var output bytes.Buffer
	teed := io.MultiWriter(&output, os.Stderr)
	command.Stdout = teed
	command.Stderr = teed
	if err := command.Start(); err != nil {
		return fmt.Errorf("start initialization in %q: %w", directory, err)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signals)
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("run initialization in %q: %w: %s", directory, err, strings.TrimSpace(output.String()))
		}
		return nil
	case received := <-signals:
		if value, ok := received.(syscall.Signal); ok {
			_ = syscall.Kill(-command.Process.Pid, value)
		}
		err := <-done
		return fmt.Errorf("initialization in %q stopped after signal %s: %w: %s", directory, received, err, strings.TrimSpace(output.String()))
	}
}

func (s *Service) writeOwnershipMarker(p domain.Workspace) error {
	if err := os.MkdirAll(filepath.Dir(p.Root), 0o755); err != nil {
		return fmt.Errorf("create Workspace data directory: %w", err)
	}
	if err := os.Mkdir(p.Root, 0o755); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("create Workspace root: %w", err)
		}
		if markerErr := ValidateWorkspaceMarker(p.Root, p.ID); markerErr == nil {
			return nil
		}
		entries, readErr := os.ReadDir(p.Root)
		if readErr != nil {
			return fmt.Errorf("inspect existing Workspace root: %w", readErr)
		}
		if len(entries) != 0 {
			return fmt.Errorf("Workspace root %q already exists without the matching ownership marker", p.Root)
		}
	}
	marker := map[string]string{"owner": "twt", "workspaceId": p.ID}
	return writeJSON(filepath.Join(p.Root, ".twt-owned.json"), marker, 0o600)
}

func newStep(id string, kind domain.StepKind, repository string) domain.SetupStep {
	return domain.SetupStep{ID: id, Kind: kind, Repository: repository, Status: domain.StepPending}
}

func newID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("create Workspace ID: %w", err)
	}
	return hex.EncodeToString(data), nil
}

func workspaceEnvironment(p domain.Workspace) []string {
	environment := []string{
		"TWT_WORKSPACE_ID=" + p.ID,
		"TWT_WORKSPACE_NAME=" + p.Name,
		"TWT_WORKSPACE_ROOT=" + p.Root,
		// Keep these aliases so saved version-one templates still run.
		"TWT_PROJECT_ID=" + p.ID,
		"TWT_PROJECT_NAME=" + p.Name,
		"TWT_PROJECT_ROOT=" + p.Root,
	}
	for _, repository := range p.Repositories {
		key := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(repository.Name))
		environment = append(environment, "TWT_REPOSITORY_"+key+"="+repository.Path)
	}
	return environment
}

func run(directory, name string, args ...string) error {
	command := exec.Command(name, args...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func output(directory, name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	command.Dir = directory
	data, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(data)))
	}
	return strings.TrimSpace(string(data)), nil
}

func writeJSON(path string, value any, mode os.FileMode) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode ownership marker: %w", err)
	}
	data = append(data, '\n')
	return store.WriteFileAtomic(path, data, mode, "ownership marker")
}

// ValidateWorkspaceMarker checks that root carries the twt ownership marker of
// the Workspace with expectedWorkspaceID.
func ValidateWorkspaceMarker(root, expectedWorkspaceID string) error {
	data, err := os.ReadFile(filepath.Join(root, ".twt-owned.json"))
	if err != nil {
		return clierr.New(clierr.UnsafeState, "Workspace root %q has no twt ownership marker", root)
	}
	owner, workspaceID, ok := decodeWorkspaceOwnership(data)
	if !ok || owner != "twt" || workspaceID != expectedWorkspaceID {
		return clierr.New(clierr.UnsafeState, "Workspace root %q has a conflicting ownership marker", root)
	}
	return nil
}

func readOwnershipWorkspaceID(root string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(root, ".twt-owned.json"))
	if err != nil {
		return "", false
	}
	owner, workspaceID, ok := decodeWorkspaceOwnership(data)
	if !ok || owner != "twt" || workspaceID == "" {
		return "", false
	}
	return workspaceID, true
}

func decodeWorkspaceOwnership(data []byte) (string, string, bool) {
	var marker struct {
		Owner       string `json:"owner"`
		WorkspaceID string `json:"workspaceId"`
		ProjectID   string `json:"projectId"`
	}
	if json.Unmarshal(data, &marker) != nil {
		return "", "", false
	}
	if marker.WorkspaceID != "" && marker.ProjectID != "" && marker.WorkspaceID != marker.ProjectID {
		return "", "", false
	}
	if marker.WorkspaceID == "" {
		marker.WorkspaceID = marker.ProjectID
	}
	return marker.Owner, marker.WorkspaceID, true
}
