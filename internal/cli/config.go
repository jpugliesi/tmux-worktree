package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/jpugliesi/tmux-worktree/internal/store"
	"github.com/spf13/cobra"
)

const (
	configSourceEnv     = "env"
	configSourceFile    = "file"
	configSourceDefault = "default"
)

type configSettingOutput struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"`
	Origin string `json:"origin,omitempty"`
}

type configOutput struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Config        []configSettingOutput `json:"config"`
}

func newConfigCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Show the resolved twt configuration",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			settings, err := options.resolvedConfig()
			if err != nil {
				return err
			}
			if WantsJSON(command) {
				return writeReadJSON(command, configOutput{SchemaVersion: jsonSchemaVersion, Config: settings}, "config")
			}
			for _, setting := range settings {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%s\t%s\n", setting.Key, setting.Value, setting.Source, setting.Origin); err != nil {
					return err
				}
			}
			return nil
		},
	}
	addFieldsFlag(command, configSettingOutput{})
	return command
}

// resolvedConfig returns every twt setting with its effective value and the
// source that supplied that value.
func (o Options) resolvedConfig() ([]configSettingOutput, error) {
	file, err := store.LoadConfig(o.ConfigDir)
	if err != nil {
		return nil, err
	}
	ticketsHome, err := o.resolveTicketsHome()
	if err != nil {
		return nil, err
	}
	branchPrefix, err := o.resolveBranchPrefix()
	if err != nil {
		return nil, err
	}
	configDir := envSetting("configDir", o.ConfigDir, "TWT_CONFIG_DIR", "XDG_CONFIG_HOME")
	return []configSettingOutput{
		configDir,
		envSetting("stateDir", o.StateDir, "TWT_STATE_DIR", "XDG_STATE_HOME"),
		envSetting("dataDir", o.DataDir, "TWT_DATA_DIR", "XDG_DATA_HOME"),
		{Key: "configFile", Value: filepath.Join(o.ConfigDir, "config.yaml"), Source: configDir.Source, Origin: configDir.Origin},
		envSetting("tmuxSocket", o.TmuxSocket, "TWT_TMUX_SOCKET", ""),
		fileOrEnvSetting("ticketsHome", ticketsHome, "TWT_TICKETS_HOME", file.TicketsHome, o.ConfigDir),
		fileOrEnvSetting("branchPrefix", branchPrefix, "TWT_BRANCH_PREFIX", file.BranchPrefix, o.ConfigDir),
	}, nil
}

// envSetting classifies a value from a TWT_* variable, then an XDG variable,
// then the default.
func envSetting(key, value, envName, xdgName string) configSettingOutput {
	if envValue := os.Getenv(envName); envValue != "" && envValue == value {
		return configSettingOutput{Key: key, Value: value, Source: configSourceEnv, Origin: envName}
	}
	if xdgName != "" {
		if xdgValue := os.Getenv(xdgName); xdgValue != "" && value == filepath.Join(xdgValue, "twt") {
			return configSettingOutput{Key: key, Value: value, Source: configSourceEnv, Origin: xdgName}
		}
	}
	return configSettingOutput{Key: key, Value: value, Source: configSourceDefault}
}

// fileOrEnvSetting classifies a value that an environment variable, then
// config.yaml, then the empty default can set.
func fileOrEnvSetting(key, value, envName, fileValue, configDir string) configSettingOutput {
	if envValue := os.Getenv(envName); envValue != "" && envValue == value {
		return configSettingOutput{Key: key, Value: value, Source: configSourceEnv, Origin: envName}
	}
	if fileValue != "" && fileValue == value {
		return configSettingOutput{Key: key, Value: value, Source: configSourceFile, Origin: filepath.Join(configDir, "config.yaml")}
	}
	return configSettingOutput{Key: key, Value: value, Source: configSourceDefault}
}
