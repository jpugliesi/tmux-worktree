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
		return fmt.Errorf("invalid Project Template %q: %w", template.Name, err)
	}
	path := s.path(template.Name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("Project Template %q already exists", template.Name)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Project Template %q: %w", template.Name, err)
	}
	return nil
}

func (s TemplateStore) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list Project Templates: %w", err)
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

func (s TemplateStore) Load(name string) (domain.Template, error) {
	var template domain.Template
	if err := ValidateResourceName(name); err != nil {
		return template, err
	}
	file, err := os.Open(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return template, fmt.Errorf("Project Template %q does not exist", name)
	}
	if err != nil {
		return template, fmt.Errorf("open Project Template %q: %w", name, err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&template); err != nil {
		return template, fmt.Errorf("decode Project Template %q: %w", name, err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return template, fmt.Errorf("decode Project Template %q: %w", name, err)
		}
		return template, fmt.Errorf("decode Project Template %q: multiple YAML documents are not supported", name)
	}
	if template.Name != name {
		return template, fmt.Errorf("Project Template %q contains name %q", name, template.Name)
	}
	if err := template.Validate(); err != nil {
		return template, fmt.Errorf("invalid Project Template %q: %w", name, err)
	}
	return template, nil
}

func (s TemplateStore) Save(template domain.Template) error {
	if err := ValidateResourceName(template.Name); err != nil {
		return err
	}
	if err := template.Validate(); err != nil {
		return fmt.Errorf("invalid Project Template %q: %w", template.Name, err)
	}
	if _, err := os.Stat(s.path(template.Name)); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Project Template %q does not exist", template.Name)
	} else if err != nil {
		return fmt.Errorf("inspect Project Template %q: %w", template.Name, err)
	}
	return s.write(s.path(template.Name), template)
}

func EncodeTemplate(template domain.Template) ([]byte, error) {
	data, err := yaml.Marshal(template)
	if err != nil {
		return nil, fmt.Errorf("encode Project Template: %w", err)
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
	temporary, err := os.CreateTemp(filepath.Dir(path), ".twt2-template-*")
	if err != nil {
		return fmt.Errorf("create temporary template: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("set template permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary template: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary template: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary template: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("save Project Template: %w", err)
	}
	return nil
}

func ValidateResourceName(name string) error {
	if !resourceName.MatchString(name) || name == "." || name == ".." {
		return fmt.Errorf("invalid name %q: use letters, numbers, dots, hyphens, or underscores", name)
	}
	return nil
}
