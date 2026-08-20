package transcript

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func (s *Service) readCodex(sessionID string, project domain.Project) (Transcript, error) {
	root := filepath.Join(s.home, ".codex", "sessions")
	paths, err := matchingFiles(root, sessionID, func(name string) bool { return strings.HasSuffix(name, sessionID) })
	if err != nil {
		return Transcript{}, err
	}
	for _, path := range paths {
		lines, info, err := readJSONLines(path)
		if err != nil {
			return Transcript{}, err
		}
		id, cwd, err := codexMetadata(lines)
		if err != nil {
			return Transcript{}, err
		}
		if id != sessionID {
			continue
		}
		repositoryName := repositoryForDirectory(project, cwd)
		if repositoryName == "" {
			return Transcript{}, fmt.Errorf("Codex transcript %q does not belong to Project %q", sessionID, project.Name)
		}
		return makeTranscript("codex", sessionID, repositoryName, info.ModTime(), codexEvents(lines))
	}
	return Transcript{}, fmt.Errorf("Codex transcript %q does not exist", sessionID)
}

func codexMetadata(lines []map[string]any) (string, string, error) {
	id := ""
	cwd := ""
	for _, line := range lines {
		if stringValue(line["type"]) != "session_meta" {
			continue
		}
		payload := mapValue(line["payload"])
		lineID := stringValue(payload["id"])
		lineCWD := stringValue(payload["cwd"])
		if id != "" && (lineID != id || lineCWD != cwd) {
			return "", "", fmt.Errorf("Codex transcript has conflicting session metadata")
		}
		id, cwd = lineID, lineCWD
	}
	return id, cwd, nil
}

func codexEvents(lines []map[string]any) []event {
	result := []event{}
	for _, line := range lines {
		if stringValue(line["type"]) != "response_item" {
			continue
		}
		payload := mapValue(line["payload"])
		role := stringValue(payload["role"])
		if role == "user" || role == "assistant" {
			result = append(result, event{role: role, text: contentText(payload["content"])})
		}
	}
	return result
}
