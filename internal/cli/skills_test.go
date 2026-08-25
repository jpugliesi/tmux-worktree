package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/version"
	skillasset "github.com/jpugliesi/tmux-worktree/skills"
)

// userTreePaths returns the three user skill files of one fake home.
func userTreePaths(home string) []string {
	return []string{
		filepath.Join(home, ".cursor", "skills", "twt", "SKILL.md"),
		filepath.Join(home, ".claude", "skills", "twt", "SKILL.md"),
		filepath.Join(home, ".agents", "skills", "twt", "SKILL.md"),
	}
}

func TestSkillsInstallWritesEveryUserTreeAsARealFile(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	output, err := execute(t, root, "skills", "install")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range userTreePaths(home) {
		if !strings.Contains(output, path) {
			t.Fatalf("install output does not name %q:\n%s", path, output)
		}
		info, statErr := os.Lstat(path)
		if statErr != nil {
			t.Fatalf("stat %q: %v", path, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("%q is a symlink; twt must write a real file", path)
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if got := skillasset.Version(string(content)); got != version.Version {
			t.Fatalf("installed version = %q, want %q", got, version.Version)
		}
		if !strings.Contains(string(content), "installed by twt skills install") {
			t.Fatalf("installed skill %q has no install mark", path)
		}
	}
	if !strings.Contains(output, "wrote") {
		t.Fatalf("install output has no action word:\n%s", output)
	}
}

func TestSkillsInstallReplacesASymlinkAndReportsJSON(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	target := userTreePaths(home)[1]
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "checkout-SKILL.md")
	if err := os.WriteFile(source, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, target); err != nil {
		t.Fatal(err)
	}

	output, err := execute(t, root, "skills", "install", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		SchemaVersion int    `json:"schemaVersion"`
		Status        string `json:"status"`
		Skills        []struct {
			Path    string `json:"path"`
			Action  string `json:"action"`
			Version string `json:"version"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if result.SchemaVersion != 2 || result.Status != "applied" || len(result.Skills) != 3 {
		t.Fatalf("install result = %+v", result)
	}
	actions := map[string]string{}
	for _, skill := range result.Skills {
		if skill.Version != version.Version {
			t.Fatalf("skill %+v does not carry the build version %q", skill, version.Version)
		}
		actions[skill.Path] = skill.Action
	}
	if actions[target] != "replaced" {
		t.Fatalf("action for the symlink path = %q, want replaced", actions[target])
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("install kept the symlink")
	}
	if content, err := os.ReadFile(source); err != nil || string(content) != "old" {
		t.Fatalf("install wrote through the symlink: content %q, err %v", content, err)
	}
}

func TestSkillsInstallDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	tree := filepath.Join(root, "tree")

	output, err := execute(t, root, "skills", "install", "--dir", tree, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tree, "twt", "SKILL.md")
	if !strings.Contains(output, path) {
		t.Fatalf("dry run does not name %q:\n%s", path, output)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote %q", path)
	}
	for _, userPath := range userTreePaths(home) {
		if _, err := os.Lstat(userPath); !os.IsNotExist(err) {
			t.Fatalf("--dir alone installed the user tree file %q", userPath)
		}
	}
}

func TestSkillsShowPrintsTheStampedSkill(t *testing.T) {
	root := t.TempDir()
	text, err := execute(t, root, "skills", "show")
	if err != nil {
		t.Fatal(err)
	}
	if skillasset.Version(text) != version.Version {
		t.Fatalf("skills show has no version stamp:\n%s", firstLines(text))
	}
	if !strings.Contains(text, "name: twt") {
		t.Fatalf("skills show lost the skill frontmatter:\n%s", firstLines(text))
	}

	output, err := execute(t, root, "skills", "show", "--output", "json")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		SchemaVersion int `json:"schemaVersion"`
		Skill         struct {
			Version string `json:"version"`
			Content string `json:"content"`
		} `json:"skill"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if result.SchemaVersion != 2 || result.Skill.Version != version.Version || result.Skill.Content != text {
		t.Fatalf("skills show JSON = %+v", result)
	}
}

func TestSkillDocumentsTicketBlockedBy(t *testing.T) {
	content := skillasset.Canonical()
	for _, want := range []string{"--blocked-by", "ticket.blockedBy", "tickets list --ready"} {
		if !strings.Contains(content, want) {
			t.Fatalf("canonical skill misses %q", want)
		}
	}
}

// firstLines shortens a long failure message.
func firstLines(text string) string {
	lines := strings.Split(text, "\n")
	if len(lines) > 8 {
		lines = lines[:8]
	}
	return strings.Join(lines, "\n")
}
