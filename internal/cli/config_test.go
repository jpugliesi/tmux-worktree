package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jpugliesi/tmux-worktree/internal/cli"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

type configSetting struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"`
	Origin string `json:"origin"`
}

type configEnvelope struct {
	SchemaVersion int             `json:"schemaVersion"`
	Config        []configSetting `json:"config"`
}

func configTestOptions(t *testing.T) cli.Options {
	t.Helper()
	root := t.TempDir()
	clearConfigEnv(t)
	return cli.Options{
		ConfigDir: filepath.Join(root, "config"),
		StateDir:  filepath.Join(root, "state"),
		DataDir:   filepath.Join(root, "data"),
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TWT_CONFIG_DIR", "")
	t.Setenv("TWT_STATE_DIR", "")
	t.Setenv("TWT_DATA_DIR", "")
	t.Setenv("TWT_TMUX_SOCKET", "")
	t.Setenv("TWT_TICKETS_HOME", "")
	t.Setenv("TWT_BRANCH_PREFIX", "")
	t.Setenv("TWT_TICKETS_SYNC", "")
	t.Setenv("TWT_TICKETS_SYNC_REMOTE", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
}

func decodeConfig(t *testing.T, output string) configEnvelope {
	t.Helper()
	var envelope configEnvelope
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("decode config: %v\n%s", err, output)
	}
	return envelope
}

func settingByKey(t *testing.T, envelope configEnvelope, key string) configSetting {
	t.Helper()
	for _, setting := range envelope.Config {
		if setting.Key == key {
			return setting
		}
	}
	t.Fatalf("config has no %q setting: %+v", key, envelope.Config)
	return configSetting{}
}

func TestConfigCommandShowsDefaultsWithSources(t *testing.T) {
	options := configTestOptions(t)

	stdout, _, err := executeRaw(t, options, "config")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	envelope := decodeConfig(t, stdout)
	if envelope.SchemaVersion != 2 {
		t.Fatalf("schemaVersion = %d, want 2", envelope.SchemaVersion)
	}

	want := []struct {
		key    string
		value  string
		source string
		origin string
	}{
		{"configDir", options.ConfigDir, "default", ""},
		{"stateDir", options.StateDir, "default", ""},
		{"dataDir", options.DataDir, "default", ""},
		{"configFile", filepath.Join(options.ConfigDir, "config.yaml"), "default", ""},
		{"tmuxSocket", "", "default", ""},
		{"ticketsHome", "", "default", ""},
		{"branchPrefix", "", "default", ""},
		{"ticketAgent.provider", "codex", "default", ""},
		{"ticketAgent.effort", "large", "default", ""},
		{"ticketAgent.instructions", "", "default", ""},
	}
	if len(envelope.Config) != len(want) {
		t.Fatalf("config setting count = %d, want %d\n%+v", len(envelope.Config), len(want), envelope.Config)
	}
	for _, item := range want {
		got := settingByKey(t, envelope, item.key)
		if got.Value != item.value || got.Source != item.source || got.Origin != item.origin {
			t.Errorf("%s = {value:%q source:%q origin:%q}, want {value:%q source:%q origin:%q}",
				item.key, got.Value, got.Source, got.Origin, item.value, item.source, item.origin)
		}
	}
}

func writeTwtConfigFile(t *testing.T, configDir, content string) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConfigCommandReportsEnvironmentSources(t *testing.T) {
	options := configTestOptions(t)
	t.Setenv("TWT_CONFIG_DIR", options.ConfigDir)
	t.Setenv("TWT_STATE_DIR", options.StateDir)
	t.Setenv("TWT_DATA_DIR", options.DataDir)
	t.Setenv("TWT_TMUX_SOCKET", "twt-test")
	t.Setenv("TWT_TICKETS_HOME", "/vault/tickets")
	t.Setenv("TWT_BRANCH_PREFIX", "jpugliesi/")
	options.TmuxSocket = "twt-test"

	envelope := decodeConfig(t, mustConfigJSON(t, options))
	want := map[string]configSetting{
		"configDir":    {Key: "configDir", Value: options.ConfigDir, Source: "env", Origin: "TWT_CONFIG_DIR"},
		"stateDir":     {Key: "stateDir", Value: options.StateDir, Source: "env", Origin: "TWT_STATE_DIR"},
		"dataDir":      {Key: "dataDir", Value: options.DataDir, Source: "env", Origin: "TWT_DATA_DIR"},
		"configFile":   {Key: "configFile", Value: filepath.Join(options.ConfigDir, "config.yaml"), Source: "env", Origin: "TWT_CONFIG_DIR"},
		"tmuxSocket":   {Key: "tmuxSocket", Value: "twt-test", Source: "env", Origin: "TWT_TMUX_SOCKET"},
		"ticketsHome":  {Key: "ticketsHome", Value: "/vault/tickets", Source: "env", Origin: "TWT_TICKETS_HOME"},
		"branchPrefix": {Key: "branchPrefix", Value: "jpugliesi/", Source: "env", Origin: "TWT_BRANCH_PREFIX"},
	}
	for key, item := range want {
		got := settingByKey(t, envelope, key)
		if got != item {
			t.Errorf("%s = %+v, want %+v", key, got, item)
		}
	}
}

func TestConfigCommandReportsXDGDirectorySources(t *testing.T) {
	options := configTestOptions(t)
	xdgConfig := t.TempDir()
	xdgState := t.TempDir()
	xdgData := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgConfig)
	t.Setenv("XDG_STATE_HOME", xdgState)
	t.Setenv("XDG_DATA_HOME", xdgData)
	options.ConfigDir = filepath.Join(xdgConfig, "twt")
	options.StateDir = filepath.Join(xdgState, "twt")
	options.DataDir = filepath.Join(xdgData, "twt")

	envelope := decodeConfig(t, mustConfigJSON(t, options))
	if got := settingByKey(t, envelope, "configDir"); got.Source != "env" || got.Origin != "XDG_CONFIG_HOME" {
		t.Errorf("configDir source = %s %s, want env XDG_CONFIG_HOME", got.Source, got.Origin)
	}
	if got := settingByKey(t, envelope, "stateDir"); got.Source != "env" || got.Origin != "XDG_STATE_HOME" {
		t.Errorf("stateDir source = %s %s, want env XDG_STATE_HOME", got.Source, got.Origin)
	}
	if got := settingByKey(t, envelope, "dataDir"); got.Source != "env" || got.Origin != "XDG_DATA_HOME" {
		t.Errorf("dataDir source = %s %s, want env XDG_DATA_HOME", got.Source, got.Origin)
	}
}

func TestConfigCommandReportsTicketsSyncDefaults(t *testing.T) {
	options := configTestOptions(t)
	envelope := decodeConfig(t, mustConfigJSON(t, options))
	if got := settingByKey(t, envelope, "ticketsSync.mode"); got.Value != "off" || got.Source != "default" {
		t.Errorf("ticketsSync.mode = %+v, want default off", got)
	}
	if got := settingByKey(t, envelope, "ticketsSync.remote"); got.Value != "origin" || got.Source != "default" {
		t.Errorf("ticketsSync.remote = %+v, want default origin", got)
	}
}

func TestConfigCommandReportsTicketsSyncSources(t *testing.T) {
	options := configTestOptions(t)
	writeTwtConfigFile(t, options.ConfigDir, "ticketsSync:\n  mode: git\n  remote: vault\n")
	configFile := filepath.Join(options.ConfigDir, "config.yaml")

	envelope := decodeConfig(t, mustConfigJSON(t, options))
	if got := settingByKey(t, envelope, "ticketsSync.mode"); got.Value != "git" || got.Source != "file" || got.Origin != configFile {
		t.Errorf("ticketsSync.mode = %+v, want file %s", got, configFile)
	}
	if got := settingByKey(t, envelope, "ticketsSync.remote"); got.Value != "vault" || got.Source != "file" || got.Origin != configFile {
		t.Errorf("ticketsSync.remote = %+v, want file %s", got, configFile)
	}

	t.Setenv("TWT_TICKETS_SYNC", "off")
	t.Setenv("TWT_TICKETS_SYNC_REMOTE", "github")
	envelope = decodeConfig(t, mustConfigJSON(t, options))
	if got := settingByKey(t, envelope, "ticketsSync.mode"); got.Value != "off" || got.Source != "env" || got.Origin != "TWT_TICKETS_SYNC" {
		t.Errorf("ticketsSync.mode = %+v, want env TWT_TICKETS_SYNC", got)
	}
	if got := settingByKey(t, envelope, "ticketsSync.remote"); got.Value != "github" || got.Source != "env" || got.Origin != "TWT_TICKETS_SYNC_REMOTE" {
		t.Errorf("ticketsSync.remote = %+v, want env TWT_TICKETS_SYNC_REMOTE", got)
	}
}

func TestConfigCommandRejectsAnUnsupportedTicketsSyncMode(t *testing.T) {
	options := configTestOptions(t)
	writeTwtConfigFile(t, options.ConfigDir, "ticketsSync:\n  mode: svn\n")
	_, _, err := executeRaw(t, options, "config")
	if err == nil {
		t.Fatal("config accepted ticketsSync.mode svn")
	}
	if clierr.CodeOf(err) != clierr.InvalidUsage {
		t.Fatalf("invalid mode code = %q, want %q", clierr.CodeOf(err), clierr.InvalidUsage)
	}
}

func TestConfigCommandReportsFileSources(t *testing.T) {
	options := configTestOptions(t)
	writeTwtConfigFile(t, options.ConfigDir, "ticketsHome: /vault/tickets\nbranchPrefix: jpugliesi/\n")
	configFile := filepath.Join(options.ConfigDir, "config.yaml")

	envelope := decodeConfig(t, mustConfigJSON(t, options))
	if got := settingByKey(t, envelope, "ticketsHome"); got.Value != "/vault/tickets" || got.Source != "file" || got.Origin != configFile {
		t.Errorf("ticketsHome = %+v, want file %s", got, configFile)
	}
	if got := settingByKey(t, envelope, "branchPrefix"); got.Value != "jpugliesi/" || got.Source != "file" || got.Origin != configFile {
		t.Errorf("branchPrefix = %+v, want file %s", got, configFile)
	}
	if got := settingByKey(t, envelope, "configFile"); got.Value != configFile || got.Source != "default" {
		t.Errorf("configFile = %+v, want default path %s", got, configFile)
	}
}

func TestConfigCommandReportsTicketAgentSettings(t *testing.T) {
	options := configTestOptions(t)
	writeTwtConfigFile(t, options.ConfigDir, "ticketAgent:\n  provider: grok\n  effort: xlarge\n  instructions: |\n    Read CONTEXT.md first.\n    Check the CLI.\n")
	configFile := filepath.Join(options.ConfigDir, "config.yaml")

	envelope := decodeConfig(t, mustConfigJSON(t, options))
	for key, value := range map[string]string{
		"ticketAgent.provider":     "grok",
		"ticketAgent.effort":       "xlarge",
		"ticketAgent.instructions": "Read CONTEXT.md first.\nCheck the CLI.\n",
	} {
		got := settingByKey(t, envelope, key)
		if got.Value != value || got.Source != "file" || got.Origin != configFile {
			t.Errorf("%s = %+v", key, got)
		}
	}
	textOutput, _, err := executeRaw(t, options, "config", "--output", "text")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(textOutput, `Read CONTEXT.md first.\nCheck the CLI.\n`) {
		t.Fatalf("text config did not escape the multiline instructions:\n%s", textOutput)
	}
}

func TestConfigCommandPrefersEnvironmentOverTheConfigFile(t *testing.T) {
	options := configTestOptions(t)
	writeTwtConfigFile(t, options.ConfigDir, "ticketsHome: /from-file\nbranchPrefix: file/\n")
	t.Setenv("TWT_TICKETS_HOME", "/from-env")
	t.Setenv("TWT_BRANCH_PREFIX", "env/")

	envelope := decodeConfig(t, mustConfigJSON(t, options))
	if got := settingByKey(t, envelope, "ticketsHome"); got.Value != "/from-env" || got.Source != "env" || got.Origin != "TWT_TICKETS_HOME" {
		t.Errorf("ticketsHome = %+v, want env /from-env", got)
	}
	if got := settingByKey(t, envelope, "branchPrefix"); got.Value != "env/" || got.Source != "env" || got.Origin != "TWT_BRANCH_PREFIX" {
		t.Errorf("branchPrefix = %+v, want env env/", got)
	}
}

func TestConfigCommandWritesTextRows(t *testing.T) {
	options := configTestOptions(t)
	writeTwtConfigFile(t, options.ConfigDir, "ticketsHome: /vault/tickets\n")
	t.Setenv("TWT_BRANCH_PREFIX", "jpugliesi/")

	stdout, _, err := executeRaw(t, options, "config", "--output", "text")
	if err != nil {
		t.Fatalf("config --output text: %v", err)
	}
	if strings.Contains(stdout, "\t") {
		t.Fatalf("config text still contains tabs:\n%s", stdout)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) == 0 || !strings.Contains(lines[0], "KEY") || !strings.Contains(lines[0], "VALUE") || !strings.Contains(lines[0], "SOURCE") || !strings.Contains(lines[0], "ORIGIN") {
		t.Fatalf("config text missing headers:\n%s", stdout)
	}
	valueAt := strings.Index(lines[0], "VALUE")
	sourceAt := strings.Index(lines[0], "SOURCE")
	configFile := filepath.Join(options.ConfigDir, "config.yaml")
	for _, want := range []struct {
		key    string
		value  string
		source string
	}{
		{"configDir", options.ConfigDir, "default"},
		{"stateDir", options.StateDir, "default"},
		{"dataDir", options.DataDir, "default"},
		{"configFile", configFile, "default"},
		{"tmuxSocket", "", "default"},
		{"ticketsHome", "/vault/tickets", "file"},
		{"branchPrefix", "jpugliesi/", "env"},
	} {
		var line string
		for _, candidate := range lines[1:] {
			if strings.HasPrefix(strings.TrimRight(candidate, " "), want.key) || strings.HasPrefix(candidate, want.key+" ") {
				line = candidate
				break
			}
		}
		if line == "" {
			t.Fatalf("config text missing %s:\n%s", want.key, stdout)
		}
		if want.value != "" && (valueAt >= len(line) || !strings.HasPrefix(line[valueAt:], want.value)) {
			t.Fatalf("config text does not align %s value %q:\n%s", want.key, want.value, stdout)
		}
		if sourceAt >= len(line) || !strings.HasPrefix(line[sourceAt:], want.source) {
			t.Fatalf("config text does not align %s source %q:\n%s", want.key, want.source, stdout)
		}
	}
}

func TestConfigCommandPrefersTWTDirectoriesOverXDG(t *testing.T) {
	options := configTestOptions(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TWT_CONFIG_DIR", options.ConfigDir)

	got := settingByKey(t, decodeConfig(t, mustConfigJSON(t, options)), "configDir")
	if got.Source != "env" || got.Origin != "TWT_CONFIG_DIR" {
		t.Fatalf("configDir source = %s %s, want env TWT_CONFIG_DIR", got.Source, got.Origin)
	}
}

func TestConfigCommandIncludesEveryConfigFileKey(t *testing.T) {
	options := configTestOptions(t)
	reported := map[string]bool{}
	for _, setting := range decodeConfig(t, mustConfigJSON(t, options)).Config {
		reported[setting.Key] = true
	}
	configType := reflect.TypeOf(store.Config{})
	for index := 0; index < configType.NumField(); index++ {
		field := configType.Field(index)
		tag := strings.Split(field.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		if tag == "ticketAgent" {
			if !reported["ticketAgent.provider"] || !reported["ticketAgent.effort"] || !reported["ticketAgent.instructions"] {
				t.Errorf("config command does not report every %q setting", tag)
			}
			continue
		}
		if !reported[tag] {
			t.Errorf("config command does not report yaml field %q", tag)
		}
	}
}

func TestConfigCommandFailsOnAnInvalidConfigFile(t *testing.T) {
	options := configTestOptions(t)
	writeTwtConfigFile(t, options.ConfigDir, "ticketsHome: /vault\nunknownField: 1\n")
	_, _, err := executeRaw(t, options, "config")
	if err == nil || !strings.Contains(err.Error(), "unknownField") {
		t.Fatalf("invalid config error = %v", err)
	}
	if clierr.CodeOf(err) != clierr.Internal {
		t.Fatalf("invalid config code = %q, want %q", clierr.CodeOf(err), clierr.Internal)
	}
}

func mustConfigJSON(t *testing.T, options cli.Options) string {
	t.Helper()
	stdout, _, err := executeRaw(t, options, "config")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	return stdout
}
