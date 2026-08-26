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
	// dirs are the template roots in precedence order: the machine config
	// dir first (its templates override shared ones by name), then the
	// shared twt home dir.
	dirs []string
	// writeDir receives new templates: the shared home dir when one is
	// configured, else the config dir.
	writeDir string
}

func NewTemplateStore(configDir string) TemplateStore {
	dir := filepath.Join(configDir, "templates")
	return TemplateStore{dirs: []string{dir}, writeDir: dir}
}

// WithSharedDir layers the shared template root of the twt home under the
// machine config dir. Config-dir templates win name collisions; new
// templates land in the shared root so every executor machine receives
// them through the tickets sync.
func (s TemplateStore) WithSharedDir(dir string) TemplateStore {
	if dir == "" {
		return s
	}
	s.dirs = append(append([]string(nil), s.dirs...), dir)
	s.writeDir = dir
	return s
}

// existingPath returns the winning file of a Workspace Template: the first
// root that holds it.
func (s TemplateStore) existingPath(name string) (string, bool) {
	for _, dir := range s.dirs {
		path := filepath.Join(dir, name+".yaml")
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

// Shadowed lists Workspace Template names that exist in more than one root:
// the config-dir copy hides the shared copy.
func (s TemplateStore) Shadowed() []string {
	if len(s.dirs) < 2 {
		return nil
	}
	shadowed := []string{}
	for _, name := range s.listDir(s.dirs[0]) {
		for _, dir := range s.dirs[1:] {
			if _, err := os.Stat(filepath.Join(dir, name+".yaml")); err == nil {
				shadowed = append(shadowed, name)
				break
			}
		}
	}
	return shadowed
}

func (s TemplateStore) listDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		names = append(names, strings.TrimSuffix(entry.Name(), ".yaml"))
	}
	return names
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
	if path, exists := s.existingPath(template.Name); exists {
		return clierr.New(clierr.AlreadyExists, "Workspace Template %q already exists at %s", template.Name, path)
	}
	return nil
}

func (s TemplateStore) List() ([]string, error) {
	seen := map[string]bool{}
	names := []string{}
	for _, dir := range s.dirs {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list Workspace Templates: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".yaml" {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), ".yaml")
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

// Path returns the YAML file path of an existing Workspace Template.
func (s TemplateStore) Path(name string) (string, error) {
	if err := ValidateResourceName(name); err != nil {
		return "", err
	}
	path, exists := s.existingPath(name)
	if !exists {
		return "", clierr.New(clierr.NotFound, "Workspace Template %q does not exist", name)
	}
	return path, nil
}

// Delete removes the YAML file of a Workspace Template. The caller must check
// that no Workspace uses the Workspace Template. A name that exists in more
// than one root is refused: remove one copy by hand first.
func (s TemplateStore) Delete(name string) error {
	path, err := s.Path(name)
	if err != nil {
		return err
	}
	if err := ValidateResourceName(name); err != nil {
		return err
	}
	copies := 0
	for _, dir := range s.dirs {
		if _, statErr := os.Stat(filepath.Join(dir, name+".yaml")); statErr == nil {
			copies++
		}
	}
	if copies > 1 {
		return clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Workspace Template %q exists in more than one template root", name),
			"Remove the config-dir copy or the shared copy by hand, then run the command again.")
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
	path, exists := s.existingPath(name)
	if !exists {
		return template, clierr.New(clierr.NotFound, "Workspace Template %q does not exist", name)
	}
	file, err := os.Open(path)
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
	path, exists := s.existingPath(template.Name)
	if !exists {
		return clierr.New(clierr.NotFound, "Workspace Template %q does not exist", template.Name)
	}
	// Save edits the winning file in place, so a machine override stays a
	// machine override and a shared template stays shared.
	return s.write(path, template)
}

func EncodeTemplate(template domain.Template) ([]byte, error) {
	data, err := yaml.Marshal(template)
	if err != nil {
		return nil, fmt.Errorf("encode Workspace Template: %w", err)
	}
	return data, nil
}

// path is the destination of a NEW Workspace Template.
func (s TemplateStore) path(name string) string {
	return filepath.Join(s.writeDir, name+".yaml")
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
