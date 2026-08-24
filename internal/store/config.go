package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// Config is the twt config.yaml document.
type Config struct {
	// TicketsHome is the root directory of the Markdown ticket files.
	TicketsHome string `yaml:"ticketsHome"`
	// BranchPrefix is the user branch prefix for the {prefix} token of
	// Workspace branch patterns. twt concatenates it literally, so include the
	// separator, for example "jpugliesi/". TWT_BRANCH_PREFIX overrides it.
	BranchPrefix string `yaml:"branchPrefix"`
}

// LoadConfig reads one strict twt config document from
// configDir/config.yaml. It rejects unknown fields and more than one
// document, the same as Workspace Template loading. A missing file returns the
// zero Config without an error.
func LoadConfig(configDir string) (Config, error) {
	var config Config
	path := filepath.Join(configDir, "config.yaml")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, fmt.Errorf("open twt config %q: %w", path, err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		if errors.Is(err, io.EOF) {
			// An empty config file is the zero Config.
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("decode twt config %q: %w", path, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return Config{}, fmt.Errorf("decode twt config %q: %w", path, err)
		}
		return Config{}, fmt.Errorf("decode twt config %q: multiple YAML documents are not supported", path)
	}
	return config, nil
}
