package transcript

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func (s *Service) readClaude(sessionID string, project domain.Project) (Transcript, error) {
	for _, repository := range project.Repositories {
		root := filepath.Join(s.home, ".claude", "projects", encodeClaudeProject(repository.Path))
		paths, err := matchingFiles(root, sessionID, func(name string) bool { return name == sessionID })
		if err != nil {
			return Transcript{}, err
		}
		for _, path := range paths {
			lines, info, err := readJSONLines(path)
			if err != nil {
				return Transcript{}, err
			}
			repositoryName, events, matched, err := parseClaude(lines, sessionID, project)
			if err != nil {
				return Transcript{}, err
			}
			if !matched {
				continue
			}
			return makeTranscript("claude", sessionID, repositoryName, info.ModTime(), events)
		}
	}
	return Transcript{}, fmt.Errorf("Claude transcript %q does not exist in Project %q", sessionID, project.Name)
}

func parseClaude(lines []map[string]any, sessionID string, project domain.Project) (string, []event, bool, error) {
	repositoryName := ""
	result := []event{}
	matched := false
	for _, line := range lines {
		lineID := stringValue(line["sessionId"])
		lineCWD := stringValue(line["cwd"])
		if lineID != "" && lineID != sessionID {
			return "", nil, false, fmt.Errorf("Claude transcript has conflicting session metadata")
		}
		lineRepository := ""
		if lineCWD != "" {
			lineRepository = repositoryForDirectory(project, lineCWD)
			if lineRepository == "" {
				return "", nil, false, fmt.Errorf("Claude transcript %q does not belong to Project %q", sessionID, project.Name)
			}
			if repositoryName != "" && lineRepository != repositoryName {
				return "", nil, false, fmt.Errorf("Claude transcript has conflicting Project directories")
			}
			repositoryName = lineRepository
		}
		if lineID == sessionID && lineRepository != "" {
			matched = true
		}
		role, text := claudeEvent(line)
		if role != "" {
			if lineID != sessionID || lineRepository == "" {
				return "", nil, false, fmt.Errorf("Claude transcript has an event without exact session metadata")
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

func encodeClaudeProject(path string) string {
	return strings.ReplaceAll(path, string(filepath.Separator), "-")
}
