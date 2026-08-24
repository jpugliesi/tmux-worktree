package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"go.yaml.in/yaml/v3"
)

var resourceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type TemplateStore struct {
	dir string
}

func NewTemplateStore(configDir string) TemplateStore {
	return TemplateStore{dir: filepath.Join(configDir, "templates")}
}

func (s TemplateStore) Create(template domain.Template) error {
	if err := s.ValidateCreate(template); err != nil {
		return err
	}
	return s.write(s.path(template.Name), template)
}

func (s TemplateStore) ValidateCreate(template domain.Template) error {
	if err := ValidateResourceName(template.Name); err != nil {
		return err
	}
	if err := template.Validate(); err != nil {
		return fmt.Errorf("invalid Workspace Template %q: %w", template.Name, err)
	}
	path := s.path(template.Name)
	if _, err := os.Stat(path); err == nil {
		return clierr.New(clierr.AlreadyExists, "Workspace Template %q already exists", template.Name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Workspace Template %q: %w", template.Name, err)
	}
	return nil
}

func (s TemplateStore) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list Workspace Templates: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".yaml"))
	}
	sort.Strings(names)
	return names, nil
}

// Path returns the YAML file path of an existing Workspace Template.
func (s TemplateStore) Path(name string) (string, error) {
	if err := ValidateResourceName(name); err != nil {
		return "", err
	}
	path := s.path(name)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return "", clierr.New(clierr.NotFound, "Workspace Template %q does not exist", name)
	} else if err != nil {
		return "", fmt.Errorf("inspect Workspace Template %q: %w", name, err)
	}
	return path, nil
}

// Delete removes the YAML file of a Workspace Template. The caller must check
// that no Workspace uses the Workspace Template.
func (s TemplateStore) Delete(name string) error {
	path, err := s.Path(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete Workspace Template %q: %w", name, err)
	}
	return nil
}

// DecodeTemplate reads one strict Workspace Template YAML document. It rejects
// unknown fields and more than one document. The source value names the input
// in error messages. It does not validate the fields.
func DecodeTemplate(reader io.Reader, source string) (domain.Template, error) {
	var template domain.Template
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	if err := decoder.Decode(&template); err != nil {
		return template, fmt.Errorf("decode Workspace Template %s: %w", source, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return template, fmt.Errorf("decode Workspace Template %s: %w", source, err)
		}
		return template, fmt.Errorf("decode Workspace Template %s: multiple YAML documents are not supported", source)
	}
	return template, nil
}

func (s TemplateStore) Load(name string) (domain.Template, error) {
	var template domain.Template
	if err := ValidateResourceName(name); err != nil {
		return template, err
	}
	file, err := os.Open(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return template, clierr.New(clierr.NotFound, "Workspace Template %q does not exist", name)
	}
	if err != nil {
		return template, fmt.Errorf("open Workspace Template %q: %w", name, err)
	}
	defer file.Close()

	template, err = DecodeTemplate(file, fmt.Sprintf("%q", name))
	if err != nil {
		return template, err
	}
	if template.Name != name {
		return template, fmt.Errorf("Workspace Template %q contains name %q", name, template.Name)
	}
	if err := template.Validate(); err != nil {
		return template, fmt.Errorf("invalid Workspace Template %q: %w", name, err)
	}
	return template, nil
}

func (s TemplateStore) Save(template domain.Template) error {
	if err := ValidateResourceName(template.Name); err != nil {
		return err
	}
	if err := template.Validate(); err != nil {
		return fmt.Errorf("invalid Workspace Template %q: %w", template.Name, err)
	}
	if _, err := os.Stat(s.path(template.Name)); errors.Is(err, os.ErrNotExist) {
		return clierr.New(clierr.NotFound, "Workspace Template %q does not exist", template.Name)
	} else if err != nil {
		return fmt.Errorf("inspect Workspace Template %q: %w", template.Name, err)
	}
	return s.write(s.path(template.Name), template)
}

func EncodeTemplate(template domain.Template) ([]byte, error) {
	data, err := yaml.Marshal(template)
	if err != nil {
		return nil, fmt.Errorf("encode Workspace Template: %w", err)
	}
	return data, nil
}

func (s TemplateStore) path(name string) string {
	return filepath.Join(s.dir, name+".yaml")
}

func (s TemplateStore) write(path string, template domain.Template) error {
	data, err := EncodeTemplate(template)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create template directory: %w", err)
	}
	return WriteFileAtomic(path, data, 0o644, "Workspace Template")
}

func ValidateResourceName(name string) error {
	if !resourceName.MatchString(name) || name == "." || name == ".." {
		return fmt.Errorf("invalid name %q: use letters, numbers, dots, hyphens, or underscores", name)
	}
	return nil
}
