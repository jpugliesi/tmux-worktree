package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const currentProjectFileName = "current-project.json"

type currentProjectRecord struct {
	Name string `json:"name"`
}

// SaveCurrentProject records the Ticket Project that an interactive terminal
// selected. The write uses a temporary file and a rename, so a reader sees the
// complete old value or the complete new value.
func SaveCurrentProject(stateDir, name string) error {
	if err := ValidateResourceName(name); err != nil {
		return fmt.Errorf("invalid Project name: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	data, err := json.Marshal(currentProjectRecord{Name: name})
	if err != nil {
		return fmt.Errorf("encode current Project: %w", err)
	}
	data = append(data, '\n')
	return WriteFileAtomic(filepath.Join(stateDir, currentProjectFileName), data, 0o600, "current Project")
}

// LoadCurrentProject returns the recorded Ticket Project name. It returns an
// empty name when no record exists.
func LoadCurrentProject(stateDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, currentProjectFileName))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read current Project: %w", err)
	}
	var record currentProjectRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return "", fmt.Errorf("decode current Project: %w", err)
	}
	if record.Name == "" {
		return "", nil
	}
	if err := ValidateResourceName(record.Name); err != nil {
		return "", fmt.Errorf("invalid current Project name: %w", err)
	}
	return record.Name, nil
}

// ClearCurrentProject removes the recorded Ticket Project. A missing file is
// success.
func ClearCurrentProject(stateDir string) error {
	path := filepath.Join(stateDir, currentProjectFileName)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove current Project: %w", err)
	}
	return nil
}
