package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ensureCodexTrust pre-trusts one Workspace checkout in codex's config,
// writing exactly the entry that answering codex's directory-trust prompt
// persists. Codex keys trust by the exact worktree path, and a dispatched
// Workspace path is always new, so an unattended agent would otherwise sit
// on the prompt forever. The operator opted into autonomy when they
// configured dispatch. Other providers handle trust through flags
// (cursor-agent --force --trust); claude may need the same treatment if it
// gains a blocking trust prompt under bypassPermissions.
func ensureCodexTrust(configPath, directory string) error {
	header := fmt.Sprintf("[projects.%q]", directory)
	if data, err := os.ReadFile(configPath); err == nil {
		if strings.Contains(string(data), header) {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create codex config directory: %w", err)
	}
	file, err := os.OpenFile(configPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open codex config: %w", err)
	}
	defer file.Close()
	if _, err := fmt.Fprintf(file, "\n%s\ntrust_level = \"trusted\"\n", header); err != nil {
		return fmt.Errorf("write codex trust entry: %w", err)
	}
	return nil
}

// codexConfigPath is the codex CLI config file of this user.
func codexConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the home directory for codex trust: %w", err)
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}
