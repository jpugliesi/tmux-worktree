package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/spf13/cobra"
)

// daemonLabel is the launchd job label of the pool refresh daemon.
const daemonLabel = "com.twt.pool-refresh"

// daemonDefaultInterval is the default time between pool refresh runs.
const daemonDefaultInterval = 10 * time.Minute

func newDaemonCommand(options Options) *cobra.Command {
	daemon := groupCommand(&cobra.Command{
		Use:   "daemon",
		Short: "Manage the background pool refresh daemon",
	})
	daemon.AddCommand(newDaemonInstallCommand(options))
	daemon.AddCommand(newDaemonUninstallCommand(options))
	daemon.AddCommand(newDaemonRunCommand(options))
	return daemon
}

func newDaemonInstallCommand(options Options) *cobra.Command {
	var interval time.Duration
	command := &cobra.Command{
		Use:   "install",
		Short: "Install the launchd agent that refreshes the pools",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := requireLaunchd(); err != nil {
				return err
			}
			if interval < time.Minute {
				return invalidUsage(command, "--interval must be one minute or more")
			}
			plistPath, err := daemonPlistPath()
			if err != nil {
				return err
			}
			content, err := daemonPlist(options, interval)
			if err != nil {
				return err
			}
			return runMutation(command, "daemon.install",
				func() (string, string, error) { return "", daemonLabel, nil },
				func() (string, string, error) {
					if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
						return "", daemonLabel, fmt.Errorf("create LaunchAgents directory: %w", err)
					}
					if err := os.WriteFile(plistPath, []byte(content), 0o644); err != nil {
						return "", daemonLabel, fmt.Errorf("write launchd agent: %w", err)
					}
					return "", daemonLabel, reloadLaunchdAgent(plistPath)
				},
				func(out io.Writer, _, _ string) error {
					_, err := fmt.Fprintf(out, "Installed launchd agent %q (%s). It runs 'twt daemon run' every %s.\n",
						daemonLabel, plistPath, interval)
					return err
				})
		},
	}
	command.Flags().DurationVar(&interval, "interval", daemonDefaultInterval, "Set the time between pool refresh runs")
	return command
}

func newDaemonUninstallCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the launchd agent that refreshes the pools",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := requireLaunchd(); err != nil {
				return err
			}
			plistPath, err := daemonPlistPath()
			if err != nil {
				return err
			}
			if _, err := os.Stat(plistPath); errors.Is(err, os.ErrNotExist) {
				return clierr.New(clierr.NotFound, "launchd agent %q is not installed", daemonLabel)
			}
			return runMutation(command, "daemon.uninstall",
				func() (string, string, error) { return "", daemonLabel, nil },
				func() (string, string, error) {
					_ = bootoutLaunchdAgent()
					if err := os.Remove(plistPath); err != nil {
						return "", daemonLabel, fmt.Errorf("remove launchd agent: %w", err)
					}
					return "", daemonLabel, nil
				},
				func(out io.Writer, _, _ string) error {
					_, err := fmt.Fprintf(out, "Removed launchd agent %q\n", daemonLabel)
					return err
				})
		},
	}
	return command
}

func newDaemonRunCommand(options Options) *cobra.Command {
	command := &cobra.Command{
		Use:   "run",
		Short: "Run one pool refresh pass over every Workspace Template",
		Args:  noArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return runDaemonPass(command, options)
		},
	}
	return command
}

// runDaemonPass refreshes the Prepared Environment pool of every Workspace
// Template. One failed Workspace Template does not stop the others: the
// daemon logs the failure and the pool self-heals on a later pass.
func runDaemonPass(command *cobra.Command, options Options) error {
	templateStore := options.templateStore()
	names, err := templateStore.List()
	if err != nil {
		return err
	}
	failures := 0
	for _, name := range names {
		template, loadErr := templateStore.Load(name)
		if loadErr != nil || len(template.Repositories) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(command.ErrOrStderr(), "%s prepare %s\n", time.Now().UTC().Format(time.RFC3339), name); err != nil {
			return err
		}
		if err := prepareTemplateEnvironments(command, options, templateStore, name); err != nil {
			failures++
			if _, writeErr := fmt.Fprintf(command.ErrOrStderr(), "Warning: the pool refresh of Workspace Template %q failed: %v\n", name, err); writeErr != nil {
				return writeErr
			}
		}
	}
	if failures > 0 {
		return clierr.New(clierr.Internal, "the pool refresh failed for %d Workspace Templates", failures)
	}
	return nil
}

// requireLaunchd limits the daemon commands to macOS: the agent runs through
// launchd.
func requireLaunchd() error {
	if runtime.GOOS != "darwin" {
		return clierr.New(clierr.PreconditionFailed, "twt daemon supports only macOS launchd")
	}
	return nil
}

// daemonPlistPath returns the LaunchAgents path of the pool refresh daemon.
func daemonPlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", daemonLabel+".plist"), nil
}

// daemonPlist renders the launchd agent. It bakes the current executable
// path, the resolved twt directories, and the current PATH, because launchd
// starts jobs with a minimal environment and repository initialization needs
// the login tools.
func daemonPlist(options Options, interval time.Duration) (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find twt executable for the daemon: %w", err)
	}
	logPath := filepath.Join(options.StateDir, "logs", "daemon.log")
	environment := [][2]string{{"PATH", os.Getenv("PATH")}}
	for _, entry := range [][2]string{
		{"TWT_CONFIG_DIR", options.ConfigDir},
		{"TWT_STATE_DIR", options.StateDir},
		{"TWT_DATA_DIR", options.DataDir},
	} {
		environment = append(environment, entry)
	}
	var variables strings.Builder
	for _, entry := range environment {
		if entry[1] == "" {
			continue
		}
		variables.WriteString(fmt.Sprintf("    <key>%s</key><string>%s</string>\n", entry[0], xmlEscape(entry[1])))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>daemon</string>
    <string>run</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
%s  </dict>
  <key>StartInterval</key><integer>%d</integer>
  <key>RunAtLoad</key><true/>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, daemonLabel, xmlEscape(executable), variables.String(), int(interval.Seconds()), xmlEscape(logPath), xmlEscape(logPath)), nil
}

// xmlEscape escapes one value for a plist string element.
func xmlEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(value)
}

// reloadLaunchdAgent replaces a loaded agent with the plist on disk.
func reloadLaunchdAgent(plistPath string) error {
	_ = bootoutLaunchdAgent()
	domain, err := launchdDomain()
	if err != nil {
		return err
	}
	command := exec.Command("launchctl", "bootstrap", domain, plistPath)
	if data, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, strings.TrimSpace(string(data)))
	}
	return nil
}

// bootoutLaunchdAgent unloads the agent. A missing agent is not an error.
func bootoutLaunchdAgent() error {
	domain, err := launchdDomain()
	if err != nil {
		return err
	}
	return exec.Command("launchctl", "bootout", domain+"/"+daemonLabel).Run()
}

// launchdDomain returns the gui domain of the current user.
func launchdDomain() (string, error) {
	current, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("resolve current user: %w", err)
	}
	return "gui/" + current.Uid, nil
}
