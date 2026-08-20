package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	projectservice "github.com/jpugliesi/tmux-worktree/internal/project"
)

const prepareWorkerArgument = "__twt2_prepare_worker"

func startPreparationRefill(options Options, templateName string, template domain.Template) error {
	executable := options.PreparationExecutable
	var err error
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return fmt.Errorf("find twt2 executable for environment preparation: %w", err)
		}
		if strings.HasSuffix(executable, ".test") {
			return nil
		}
	}
	service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
	queued, err := service.TopUpPool(templateName, template, template.EffectivePoolDepth())
	if err != nil {
		return err
	}
	for _, environment := range queued {
		if err := startPrepareWorker(options, service, executable, environment); err != nil {
			return err
		}
	}
	return nil
}

// startPrepareWorker starts one detached background preparation process for a
// queued Prepared Environment.
func startPrepareWorker(options Options, service *projectservice.Service, executable string, environment domain.PreparedEnvironment) error {
	logPath := projectservice.PrepareLogPath(options.StateDir, environment.ID)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("create preparation log directory: %w", err)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open preparation log: %w", err)
	}
	command := exec.Command(executable, prepareWorkerArgument, environment.ID, environment.QueueToken)
	command.Env = append(os.Environ(),
		"TWT2_CONFIG_DIR="+options.ConfigDir,
		"TWT2_STATE_DIR="+options.StateDir,
		"TWT2_DATA_DIR="+options.DataDir,
		"TWT2_TMUX_SOCKET="+options.TmuxSocket,
	)
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		logFile.Close()
		cause := fmt.Errorf("start background preparation: %w", err)
		_ = service.FailQueuedPreparation(environment.ID, environment.QueueToken, cause)
		return cause
	}
	if err := command.Process.Release(); err != nil {
		logFile.Close()
		return fmt.Errorf("release background preparation process: %w", err)
	}
	return logFile.Close()
}

func RunPrepareWorker(options Options, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("invalid Prepared Environment worker request")
	}
	service := projectservice.NewService(projectservice.Options{StateDir: options.StateDir, DataDir: options.DataDir, TmuxSocket: options.TmuxSocket})
	environment, err := service.PrepareQueued(args[0], args[1])
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(os.Stdout, "Prepared Environment %q for Project Template %q\n", environment.ID, environment.TemplateName)
	return err
}
