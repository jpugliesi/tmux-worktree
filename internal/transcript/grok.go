package transcript

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

func (s *Service) grokRoot() string { return filepath.Join(s.home, ".grok", "sessions") }

// discoverGrok reads the session ID and the repository name of one Grok
// Build chat_history file. Other jsonl files in the same session directory
// are not sessions.
func discoverGrok(path string, workspace domain.Workspace) (string, string, bool) {
	sessionID, sessionDir, ok := grokSessionFromPath(path)
	if !ok {
		return "", "", false
	}
	cwd, id, err := grokSessionMeta(sessionDir)
	if err != nil || (id != "" && id != sessionID) {
		return "", "", false
	}
	if cwd == "" {
		return "", "", false
	}
	name := repositoryForDirectory(workspace, cwd)
	if name == "" {
		return "", "", false
	}
	return sessionID, name, true
}

func (s *Service) readGrok(sessionID string, workspace domain.Workspace) (Transcript, error) {
	paths, err := grokChatHistoryFiles(s.grokRoot(), sessionID)
	if err != nil {
		return Transcript{}, err
	}
	for _, path := range paths {
		lines, info, err := readJSONLines(path)
		if err != nil {
			return Transcript{}, err
		}
		sessionDir := filepath.Dir(path)
		cwd, id, err := grokSessionMeta(sessionDir)
		if err != nil {
			return Transcript{}, err
		}
		if id != "" && id != sessionID {
			return Transcript{}, clierr.New(clierr.PreconditionFailed, "Grok transcript has conflicting session metadata")
		}
		repositoryName := repositoryForDirectory(workspace, cwd)
		if repositoryName == "" {
			return Transcript{}, clierr.New(clierr.PreconditionFailed, "Grok transcript %q does not belong to Workspace %q", sessionID, workspace.Name)
		}
		return makeTranscript("grok", sessionID, repositoryName, info.ModTime(), grokEvents(lines))
	}
	return Transcript{}, clierr.New(clierr.NotFound, "Grok transcript %q does not exist", sessionID)
}

func grokSessionFromPath(path string) (string, string, bool) {
	if filepath.Base(path) != "chat_history.jsonl" {
		return "", "", false
	}
	sessionDir := filepath.Dir(path)
	sessionID := filepath.Base(sessionDir)
	if ValidateSessionID(sessionID) != nil {
		return "", "", false
	}
	return sessionID, sessionDir, true
}

func grokSessionMeta(sessionDir string) (cwd, id string, err error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, "summary.json"))
	if err == nil {
		var summary struct {
			Info struct {
				ID  string `json:"id"`
				CWD string `json:"cwd"`
			} `json:"info"`
		}
		if json.Unmarshal(data, &summary) == nil {
			id = summary.Info.ID
			cwd = summary.Info.CWD
		}
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	if cwd != "" {
		return cwd, id, nil
	}
	encoded := filepath.Base(filepath.Dir(sessionDir))
	decoded, decodeErr := url.PathUnescape(encoded)
	if decodeErr != nil || decoded == "" || strings.Contains(decoded, "..") {
		return "", id, nil
	}
	return decoded, id, nil
}

func grokChatHistoryFiles(root, sessionID string) ([]string, error) {
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || entry.Name() != "chat_history.jsonl" {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) != sessionID {
			return nil
		}
		paths = append(paths, path)
		if len(paths) > maxCandidateFiles {
			return clierr.New(clierr.PreconditionFailed, "too many transcript candidates for provider session %q", sessionID)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return paths, nil
	}
	return paths, err
}

func grokEvents(lines []map[string]any) []event {
	result := []event{}
	for _, line := range lines {
		if stringValue(line["synthetic_reason"]) != "" {
			continue
		}
		role := stringValue(line["type"])
		if role != "user" && role != "assistant" {
			continue
		}
		text := strings.TrimSpace(contentText(line["content"]))
		if text == "" || strings.HasPrefix(text, "<system-reminder>") {
			continue
		}
		result = append(result, event{role: role, text: text})
	}
	return result
}
