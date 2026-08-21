package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

const (
	maxTranscriptBytes   = 32 << 20
	maxDiscoverScanBytes = 1 << 20
	maxCandidateFiles    = 128
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
				return clierr.New(clierr.PreconditionFailed, "too many transcript candidates for provider session %q", sessionID)
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
		return nil, nil, clierr.New(clierr.UnsafeState, "transcript source is not a safe regular file")
	}
	lines := []map[string]any{}
	err = scanJSONLines(path, 0, func(value map[string]any) bool {
		lines = append(lines, value)
		return true
	})
	if err != nil {
		return nil, nil, err
	}
	return lines, info, nil
}

// scanJSONLines reads JSON objects from a JSON Lines file and calls visit for
// each object. visit returns false to stop. A positive limit reads only that
// many bytes, so discovery can take session metadata from a large file.
func scanJSONLines(path string, limit int64, visit func(map[string]any) bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return clierr.New(clierr.UnsafeState, "transcript source is not a safe regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	var reader io.Reader = file
	maxToken := maxTranscriptBytes
	if limit > 0 {
		reader = io.LimitReader(file, limit)
		if int(limit) < maxToken {
			maxToken = int(limit)
		}
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxToken)
	for scanner.Scan() {
		var value map[string]any
		if json.Unmarshal(scanner.Bytes(), &value) != nil {
			continue
		}
		if !visit(value) {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read transcript: %w", err)
	}
	return nil
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
