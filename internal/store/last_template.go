package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const lastTemplateFileName = "last-template.json"

type lastTemplateRecord struct {
	Name string `json:"name"`
}

// SaveLastTemplate records the Workspace Template that the last Workspace creation
// used. The write uses a temporary file and a rename, so a reader sees the
// complete old value or the complete new value.
func SaveLastTemplate(stateDir, name string) error {
	if err := ValidateResourceName(name); err != nil {
		return fmt.Errorf("invalid Workspace Template name: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	data, err := json.Marshal(lastTemplateRecord{Name: name})
	if err != nil {
		return fmt.Errorf("encode last Workspace Template: %w", err)
	}
	data = append(data, '\n')
	return WriteFileAtomic(filepath.Join(stateDir, lastTemplateFileName), data, 0o600, "last Workspace Template")
}

// LoadLastTemplate returns the recorded Workspace Template name. It returns an
// empty name when no record exists.
func LoadLastTemplate(stateDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, lastTemplateFileName))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read last Workspace Template: %w", err)
	}
	var record lastTemplateRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return "", fmt.Errorf("decode last Workspace Template: %w", err)
	}
	if record.Name == "" {
		return "", nil
	}
	if err := ValidateResourceName(record.Name); err != nil {
		return "", fmt.Errorf("invalid last Workspace Template name: %w", err)
	}
	return record.Name, nil
}
