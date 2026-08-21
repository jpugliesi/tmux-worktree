// Package skills carries the canonical twt agent skill and installs it into
// agent skill trees. The file skills/twt/SKILL.md is the one source of the
// text. The binary embeds a copy at build time, so an installed twt does not
// need the repository checkout.
package skills

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Name is the skill name and the directory name inside a skill tree.
const Name = "twt"

// fence is the YAML frontmatter delimiter of a skill file.
const fence = "---"

// installMark tells a reader that twt wrote this copy of the skill.
const installMark = "<!-- installed by twt skills install -->"

// Action reports what an install did, or what a dry run would do.
const (
	ActionWrote    = "wrote"
	ActionReplaced = "replaced"
)

//go:embed twt/SKILL.md
var files embed.FS

// Result is one installed skill file.
type Result struct {
	Path    string `json:"path"`
	Action  string `json:"action"`
	Version string `json:"version"`
}

// Canonical returns the skill text of this build. It carries no version: only
// an installed copy gets a version stamp.
func Canonical() string {
	data, err := files.ReadFile(Name + "/SKILL.md")
	if err != nil {
		return ""
	}
	return string(data)
}

// Stamped returns the copy that twt installs for one build version. It sets
// the frontmatter version field and adds one comment line, so a reader and
// twt doctor can tell which build wrote the file.
func Stamped(version string) string {
	content := Canonical()
	front, body, ok := splitFrontmatter(content)
	stamp := "version: " + version
	if !ok {
		return fence + "\n" + stamp + "\n" + fence + "\n" + installMark + "\n\n" + content
	}
	kept := make([]string, 0, len(front)+1)
	for _, line := range front {
		if isVersionField(line) {
			continue
		}
		kept = append(kept, line)
	}
	kept = append(kept, stamp)
	return fence + "\n" + strings.Join(kept, "\n") + "\n" + fence + "\n" + installMark + "\n" + body
}

// Version returns the frontmatter version of one installed skill file. It
// returns an empty value when the file carries no version field.
func Version(content string) string {
	front, _, ok := splitFrontmatter(content)
	if !ok {
		return ""
	}
	for _, line := range front {
		if isVersionField(line) {
			return versionValue(line)
		}
	}
	return ""
}

// UserPaths returns the skill file path in each user skill tree: Cursor,
// Claude Code, and the shared agents tree. The result is empty when the home
// directory is unknown.
func UserPaths(home string) []string {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	trees := []string{".cursor", ".claude", ".agents"}
	paths := make([]string, 0, len(trees))
	for _, tree := range trees {
		paths = append(paths, filepath.Join(home, tree, "skills", Name, "SKILL.md"))
	}
	return paths
}

// PathIn returns the skill file path inside one custom skill tree.
func PathIn(directory string) string {
	return filepath.Join(directory, Name, "SKILL.md")
}

// Install writes the stamped skill to each path. It replaces an existing
// file or symlink with a real file, because the binary can outlive the
// repository checkout. A dry run reports the same actions and writes nothing.
func Install(paths []string, version string, dryRun bool) ([]Result, error) {
	content := Stamped(version)
	results := make([]Result, 0, len(paths))
	for _, path := range paths {
		action := ActionWrote
		if _, err := os.Lstat(path); err == nil {
			action = ActionReplaced
		}
		if !dryRun {
			if err := writeSkillFile(path, content); err != nil {
				return nil, err
			}
		}
		results = append(results, Result{Path: path, Action: action, Version: version})
	}
	return results, nil
}

// writeSkillFile replaces one skill file. It writes a temporary file in the
// same directory and renames it, so a failed write never leaves a partial
// skill, and a rename replaces a symlink instead of writing through it.
func writeSkillFile(path, content string) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create the skill directory %q: %w", directory, err)
	}
	temporary, err := os.CreateTemp(directory, "SKILL.md.*")
	if err != nil {
		return fmt.Errorf("write the skill file %q: %w", path, err)
	}
	name := temporary.Name()
	_, writeErr := temporary.WriteString(content)
	closeErr := temporary.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write the skill file %q: %w", path, err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write the skill file %q: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("replace the skill file %q: %w", path, err)
	}
	return nil
}

// splitFrontmatter divides a skill file into its frontmatter lines and the
// body that follows. It reports false when the file has no frontmatter.
func splitFrontmatter(content string) ([]string, string, bool) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != fence {
		return nil, content, false
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == fence {
			return lines[1:index], strings.Join(lines[index+1:], "\n"), true
		}
	}
	return nil, content, false
}

// isVersionField reports whether one frontmatter line sets the version.
func isVersionField(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "version:")
}

// versionValue reads the value of one version frontmatter line.
func versionValue(line string) string {
	value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "version:"))
	return strings.Trim(value, `"'`)
}
