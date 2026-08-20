package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// Config is the twt2 config.yaml document.
type Config struct {
	// TicketsHome is the root directory of the Markdown ticket files.
	TicketsHome string `yaml:"ticketsHome"`
}

// LoadConfig reads one strict twt2 config document from
// configDir/config.yaml. It rejects unknown fields and more than one
// document, the same as Project Template loading. A missing file returns the
// zero Config without an error.
func LoadConfig(configDir string) (Config, error) {
	var config Config
	path := filepath.Join(configDir, "config.yaml")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, fmt.Errorf("open twt2 config %q: %w", path, err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		if errors.Is(err, io.EOF) {
			// An empty config file is the zero Config.
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("decode twt2 config %q: %w", path, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return Config{}, fmt.Errorf("decode twt2 config %q: %w", path, err)
		}
		return Config{}, fmt.Errorf("decode twt2 config %q: multiple YAML documents are not supported", path)
	}
	return config, nil
}
