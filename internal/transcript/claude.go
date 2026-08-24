package transcript

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func (s *Service) claudeRoot() string { return filepath.Join(s.home, ".claude", "projects") }

// discoverClaude reads the session ID and the repository name of one Claude
// provider file for discovery.
func discoverClaude(path string, workspace domain.Workspace) (string, string, bool) {
	id := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if ValidateSessionID(id) != nil {
		return "", "", false
	}
	cwd := ""
	err := scanJSONLines(path, maxDiscoverScanBytes, func(line map[string]any) bool {
		lineID := stringValue(line["sessionId"])
		if lineID != "" && lineID != id {
			cwd = ""
			return false
		}
		lineCWD := stringValue(line["cwd"])
		if lineCWD == "" {
			return true
		}
		cwd = lineCWD
		return false
	})
	if err != nil || cwd == "" {
		return "", "", false
	}
	return id, repositoryForDirectory(workspace, cwd), true
}

func (s *Service) readClaude(sessionID string, workspace domain.Workspace) (Transcript, error) {
	root := s.claudeRoot()
	fast := []string{}
	for _, directory := range append([]string{workspace.Root}, repositoryPaths(workspace)...) {
		if directory == "" {
			continue
		}
		path := filepath.Join(root, encodeClaudeWorkspace(directory), sessionID+".jsonl")
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			fast = append(fast, path)
		}
	}
	if transcript, matched, err := s.readClaudePaths(fast, sessionID, workspace); err != nil || matched {
		return transcript, err
	}
	paths, err := matchingFiles(root, sessionID, func(name string) bool { return name == sessionID })
	if err != nil {
		return Transcript{}, err
	}
	if transcript, matched, err := s.readClaudePaths(paths, sessionID, workspace); err != nil || matched {
		return transcript, err
	}
	return Transcript{}, clierr.New(clierr.NotFound, "Claude transcript %q does not exist in Workspace %q", sessionID, workspace.Name)
}

func (s *Service) readClaudePaths(paths []string, sessionID string, workspace domain.Workspace) (Transcript, bool, error) {
	for _, path := range paths {
		lines, info, err := readJSONLines(path)
		if err != nil {
			return Transcript{}, false, err
		}
		repositoryName, events, matched, err := parseClaude(lines, sessionID, workspace)
		if err != nil {
			return Transcript{}, false, err
		}
		if !matched {
			continue
		}
		transcript, err := makeTranscript("claude", sessionID, repositoryName, info.ModTime(), events)
		return transcript, true, err
	}
	return Transcript{}, false, nil
}

func repositoryPaths(workspace domain.Workspace) []string {
	paths := []string{}
	for _, repository := range workspace.Repositories {
		paths = append(paths, repository.Path)
	}
	return paths
}

func parseClaude(lines []map[string]any, sessionID string, workspace domain.Workspace) (string, []event, bool, error) {
	repositoryName := ""
	result := []event{}
	matched := false
	for _, line := range lines {
		lineID := stringValue(line["sessionId"])
		lineCWD := stringValue(line["cwd"])
		if lineID != "" && lineID != sessionID {
			return "", nil, false, clierr.New(clierr.PreconditionFailed, "Claude transcript has conflicting session metadata")
		}
		lineRepository := ""
		if lineCWD != "" {
			lineRepository = repositoryForDirectory(workspace, lineCWD)
			if lineRepository == "" {
				return "", nil, false, clierr.New(clierr.PreconditionFailed, "Claude transcript %q does not belong to Workspace %q", sessionID, workspace.Name)
			}
			if repositoryName != "" && lineRepository != repositoryName {
				return "", nil, false, clierr.New(clierr.PreconditionFailed, "Claude transcript has conflicting Workspace directories")
			}
			repositoryName = lineRepository
		}
		if lineID == sessionID && lineRepository != "" {
			matched = true
		}
		role, text := claudeEvent(line)
		if role != "" {
			if lineID != sessionID || lineRepository == "" {
				return "", nil, false, clierr.New(clierr.PreconditionFailed, "Claude transcript has an event without exact session metadata")
			}
			result = append(result, event{role: role, text: text})
		}
	}
	return repositoryName, result, matched, nil
}

func claudeEvent(line map[string]any) (string, string) {
	message := mapValue(line["message"])
	role := stringValue(message["role"])
	if role == "" {
		role = stringValue(line["type"])
	}
	if role != "user" && role != "assistant" {
		return "", ""
	}
	content := message["content"]
	if content == nil {
		content = line["content"]
	}
	return role, contentText(content)
}

func encodeClaudeWorkspace(path string) string {
	encoded := []rune(path)
	for index, character := range encoded {
		if (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			encoded[index] = '-'
		}
	}
	return string(encoded)
}
