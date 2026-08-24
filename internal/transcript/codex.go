package transcript

import (
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func (s *Service) codexRoot() string { return filepath.Join(s.home, ".codex", "sessions") }

// discoverCodex reads the session ID and the repository name of one Codex
// provider file for discovery.
func discoverCodex(path string, workspace domain.Workspace) (string, string, bool) {
	id, cwd := "", ""
	err := scanJSONLines(path, maxDiscoverScanBytes, func(line map[string]any) bool {
		if stringValue(line["type"]) != "session_meta" {
			return true
		}
		payload := mapValue(line["payload"])
		id = stringValue(payload["id"])
		cwd = stringValue(payload["cwd"])
		return false
	})
	if err != nil || ValidateSessionID(id) != nil {
		return "", "", false
	}
	return id, repositoryForDirectory(workspace, cwd), true
}

func (s *Service) readCodex(sessionID string, workspace domain.Workspace) (Transcript, error) {
	paths, err := matchingFiles(s.codexRoot(), sessionID, func(name string) bool { return strings.HasSuffix(name, sessionID) })
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
		repositoryName := repositoryForDirectory(workspace, cwd)
		if repositoryName == "" {
			return Transcript{}, clierr.New(clierr.PreconditionFailed, "Codex transcript %q does not belong to Workspace %q", sessionID, workspace.Name)
		}
		return makeTranscript("codex", sessionID, repositoryName, info.ModTime(), codexEvents(lines))
	}
	return Transcript{}, clierr.New(clierr.NotFound, "Codex transcript %q does not exist", sessionID)
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
			return "", "", clierr.New(clierr.PreconditionFailed, "Codex transcript has conflicting session metadata")
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
