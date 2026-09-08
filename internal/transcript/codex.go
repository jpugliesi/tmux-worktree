package transcript

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func (s *Service) codexRoot() string { return filepath.Join(s.home, ".codex", "sessions") }

// discoverCodex reads the session ID and the repository name of one Codex
// provider file for discovery.
func discoverCodex(path string, workspace domain.Workspace) discoveredFile {
	id, cwd, startedAt := "", "", time.Time{}
	err := scanJSONLines(path, maxDiscoverScanBytes, func(line map[string]any) bool {
		if stringValue(line["type"]) != "session_meta" {
			return true
		}
		payload := mapValue(line["payload"])
		id = firstString(payload["id"], payload["session_id"])
		cwd = stringValue(payload["cwd"])
		startedAt = parseCodexTime(firstString(payload["timestamp"], line["timestamp"]))
		return false
	})
	if err != nil || ValidateSessionID(id) != nil {
		return discoveredFile{}
	}
	return discoveredFile{
		SessionID: id, Directory: cwd, RepositoryName: repositoryForDirectory(workspace, cwd),
		StartedAt: startedAt, OK: true,
	}
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

func firstString(values ...any) string {
	for _, value := range values {
		if text := stringValue(value); text != "" {
			return text
		}
	}
	return ""
}

func parseCodexTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
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
