package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

const (
	maxTranscriptBytes = 32 << 20
	maxCandidateFiles  = 128
)

func matchingFiles(root, sessionID string, matches func(string) bool) ([]string, error) {
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
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".jsonl" && matches(strings.TrimSuffix(entry.Name(), ".jsonl")) {
			paths = append(paths, path)
			if len(paths) > maxCandidateFiles {
				return fmt.Errorf("too many transcript candidates for provider session %q", sessionID)
			}
		}
		return nil
	})
	if os.IsNotExist(err) {
		return paths, nil
	}
	return paths, err
}

func readJSONLines(path string) ([]map[string]any, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxTranscriptBytes {
		return nil, nil, fmt.Errorf("transcript source is not a safe regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	lines := []map[string]any{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxTranscriptBytes)
	for scanner.Scan() {
		var value map[string]any
		if json.Unmarshal(scanner.Bytes(), &value) == nil {
			lines = append(lines, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, fmt.Errorf("read transcript: %w", err)
	}
	return lines, info, nil
}

func repositoryForDirectory(project domain.Project, directory string) string {
	cleanDirectory, err := filepath.Abs(directory)
	if err != nil {
		return ""
	}
	for _, repository := range project.Repositories {
		root, err := filepath.Abs(repository.Path)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(root, cleanDirectory)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return repository.Name
		}
	}
	return ""
}
