package project

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func (s *Service) runInitialize(p domain.Project, directory string, init *domain.InitializeSpec) error {
	if init == nil || len(init.Command) == 0 {
		return fmt.Errorf("initialization command is empty")
	}
	command := exec.Command(init.Command[0], init.Command[1:]...)
	command.Dir = directory
	command.Env = append(os.Environ(), projectEnvironment(p)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run initialization in %q: %w: %s", directory, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (s *Service) writeOwnershipMarker(p domain.Project) error {
	if err := os.MkdirAll(filepath.Dir(p.Root), 0o755); err != nil {
		return fmt.Errorf("create Project data directory: %w", err)
	}
	if err := os.Mkdir(p.Root, 0o755); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("create Project root: %w", err)
		}
		if markerErr := validateProjectMarker(p.Root, p.ID); markerErr == nil {
			return nil
		}
		entries, readErr := os.ReadDir(p.Root)
		if readErr != nil {
			return fmt.Errorf("inspect existing Project root: %w", readErr)
		}
		if len(entries) != 0 {
			return fmt.Errorf("Project root %q already exists without the matching ownership marker", p.Root)
		}
	}
	marker := map[string]string{"owner": "twt2", "projectId": p.ID}
	return writeJSON(filepath.Join(p.Root, ".twt2-owned.json"), marker, 0o600)
}

func newStep(id string, kind domain.StepKind, repository string) domain.SetupStep {
	return domain.SetupStep{ID: id, Kind: kind, Repository: repository, Status: domain.StepPending}
}

func newID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("create Project ID: %w", err)
	}
	return hex.EncodeToString(data), nil
}

func projectEnvironment(p domain.Project) []string {
	environment := []string{
		"TWT2_PROJECT_ID=" + p.ID,
		"TWT2_PROJECT_NAME=" + p.Name,
		"TWT2_PROJECT_ROOT=" + p.Root,
	}
	for _, repository := range p.Repositories {
		key := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(repository.Name))
		environment = append(environment, "TWT2_REPOSITORY_"+key+"="+repository.Path)
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
	if err := os.WriteFile(path, data, mode); err != nil {
		return fmt.Errorf("write ownership marker: %w", err)
	}
	return nil
}

func validateProjectMarker(root, expectedProjectID string) error {
	data, err := os.ReadFile(filepath.Join(root, ".twt2-owned.json"))
	if err != nil {
		return fmt.Errorf("Project root %q has no twt2 ownership marker", root)
	}
	var marker struct {
		Owner     string `json:"owner"`
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(data, &marker); err != nil || marker.Owner != "twt2" || marker.ProjectID != expectedProjectID {
		return fmt.Errorf("Project root %q has a conflicting ownership marker", root)
	}
	return nil
}
