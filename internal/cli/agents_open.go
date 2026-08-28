package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	workspaceservice "github.com/jpugliesi/tmux-worktree/internal/workspace"
	"github.com/spf13/cobra"
)

func newAgentsOpenCommand(options Options, agents *agentservice.Service, workspaces *workspaceservice.Service, stateDir string) *cobra.Command {
	var workspaceReference string
	var preview bool
	command := &cobra.Command{
		Use:   "open [AGENT_ID]",
		Short: "Pick an Agent Session and resume it",
		Args: func(command *cobra.Command, args []string) error {
			if preview {
				if len(args) == 0 {
					return invalidUsage(command, "missing required argument AGENT_ID")
				}
				if len(args) > 1 {
					return invalidUsage(command, "unexpected argument %q; expected AGENT_ID", args[1])
				}
				return nil
			}
			return optionalArg("AGENT_ID")(command, args)
		},
		PreRunE: func(command *cobra.Command, _ []string) error {
			if preview {
				return nil
			}
			if WantsJSON(command) {
				return invalidUsage(command, "open is an interactive command and does not support JSON output")
			}
			return nil
		},
		RunE: func(command *cobra.Command, args []string) error {
			workspace, err := resolveWorkspace(workspaces, workspaceReference)
			if err != nil {
				return err
			}
			if preview {
				if len(args) == 0 {
					return invalidUsage(command, "missing required argument AGENT_ID")
				}
				return previewAgent(command, agents, workspace, args[0])
			}
			reference := ""
			if len(args) == 1 {
				reference = args[0]
			} else {
				reference, err = pickOpenAgent(command, options, agents, workspace)
				if err != nil {
					return err
				}
			}
			return openAgentSession(command, options, agents, workspaces, stateDir, reference, workspace.ID)
		},
	}
	command.Flags().StringVar(&workspaceReference, "workspace", "current", "Select the Workspace by name or ID")
	command.Flags().BoolVar(&preview, "preview", false, "Preview the Agent Session. Text output writes markdown. This path never registers a session and never writes a snapshot")
	setArguments(command, optionalArgument("agent_id", "the interactive picker asks for it when absent. --preview requires it"))
	command.ValidArgsFunction = agentReferenceCompletion(agents, workspaces, stateDir)
	_ = command.RegisterFlagCompletionFunc("workspace", workspaceFlagCompletion(workspaces))
	return command
}

// openAgentSession adopts a discovered session when needed, then replaces
// this process with the provider resume command. The command runs in the
// current pane. It does not start a new tmux window.
func openAgentSession(command *cobra.Command, options Options, agents *agentservice.Service, workspaces *workspaceservice.Service, stateDir, agentID, workspaceReference string) error {
	agent, err := agents.Find(agentID)
	if err != nil {
		agent, err = adoptForResume(command, agents, workspaces, stateDir, agentID, workspaceReference, err)
		if err != nil {
			return err
		}
	}
	if _, err := findAgentWorkspace(workspaces, agent, workspaceReference); err != nil {
		return err
	}
	if agents.IsLive(agent) {
		if isDryRun(command) {
			return writeMutation(command, "agents.open", statusValid, agent.ID, agent.Label)
		}
		return agents.Focus(agent)
	}
	resumeCommand := agentResumeCommand(agent)
	if len(resumeCommand) == 0 {
		return clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Agent Session %q has no resume command", agent.ID),
			"Register the session with a resume command after --.",
		)
	}
	if isDryRun(command) {
		return writeMutation(command, "agents.open", statusValid, agent.ID, strings.Join(resumeCommand, " "))
	}
	execFn := options.AgentOpenExec
	if execFn == nil {
		execFn = realAgentOpenExec
	}
	return execFn(resumeCommand[0], resumeCommand, os.Environ())
}

// agentResumeCommand is the command that starts the Agent Session again in
// this pane. New prompt-bearing declarations can prefer a linked provider
// session. Other records use the saved resume command first.
func agentResumeCommand(agent domain.AgentSession) []string {
	return agentservice.EffectiveResumeCommand(agent)
}

// realAgentOpenExec replaces this process with the provider resume command.
func realAgentOpenExec(name string, argv []string, env []string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "cannot find %q on PATH", name),
			"Install the provider CLI, then run the resume command in this pane.",
		)
	}
	return syscall.Exec(path, argv, env)
}

// pickOpenAgent shows the interactive Agent Session picker and returns the
// selected Agent Session ID. The newest session comes first.
func pickOpenAgent(command *cobra.Command, options Options, agents *agentservice.Service, workspace domain.Workspace) (string, error) {
	outputs, _, _, err := workspaceAgentOutputs(agents, workspace, true, false)
	if err != nil {
		return "", err
	}
	if len(outputs) == 0 {
		return "", clierr.New(clierr.NotFound, "no Agent Sessions exist for this Workspace")
	}
	now := time.Now()
	lines := make([]string, 0, len(outputs))
	for _, output := range outputs {
		lines = append(lines, agentListLine(output, now))
	}
	pick := options.AgentPick
	if pick == nil {
		pick = realAgentPick(workspace.ID)
	}
	index, err := pick(command, lines)
	if err != nil {
		return "", err
	}
	if index < 0 || index >= len(outputs) {
		return "", fmt.Errorf("the Agent Session picker returned an invalid selection")
	}
	return outputs[index].ID, nil
}

// realAgentPick selects one picker line with fzf when it is installed, or
// with a numbered list on the terminal. The fzf preview writes the same
// markdown as `twt agents transcript get`.
func realAgentPick(workspaceID string) func(*cobra.Command, []string) (int, error) {
	return func(command *cobra.Command, lines []string) (int, error) {
		return pickLine(command, lines, pickOptions{
			Noun:        "Agent Session",
			MissingHint: "missing AGENT_ID; use 'twt agents open AGENT_ID' in a script",
			FzfArgs: []string{
				"--delimiter", "\t",
				"--preview", agentOpenPreviewCommand(workspaceID),
				"--preview-window", "right:60%:wrap",
			},
		})
	}
}

// agentOpenPreviewCommand is the fzf --preview command. fzf runs it with a
// shell and replaces {2} with the Agent Session ID of the highlighted line.
func agentOpenPreviewCommand(workspaceID string) string {
	executable, err := os.Executable()
	if err != nil {
		executable = os.Args[0]
	}
	return fmt.Sprintf("%s agents open --preview --workspace %s --output text '{2}'", shellQuote(executable), shellQuote(workspaceID))
}

// previewAgent writes a verified transcript or a bounded visible-pane preview
// as markdown. It never registers a discovered session, writes a snapshot, or
// saves a provider session link. An error becomes text for the fzf preview.
func previewAgent(command *cobra.Command, agents *agentservice.Service, workspace domain.Workspace, reference string) error {
	value, err := agents.Preview(workspace, reference)
	if err != nil {
		return previewError(command, err)
	}
	if previewWantsJSON(command) {
		return writeReadJSON(command, agentTranscriptOutput{
			SchemaVersion: jsonSchemaVersion, WorkspaceID: workspace.ID, AgentID: reference,
			Provider: value.Provider, RepositoryName: value.RepositoryName,
			UpdatedAt: value.UpdatedAt.Format(time.RFC3339), Source: value.Source,
			Truncated: value.Truncated, Untrusted: true, Markdown: value.Markdown,
		}, "")
	}
	_, err = io.WriteString(command.OutOrStdout(), value.Markdown)
	return err
}

// previewError keeps fzf's text preview useful, but gives JSON callers the
// normal structured error and nonzero exit contract.
func previewError(command *cobra.Command, err error) error {
	if previewWantsJSON(command) {
		return err
	}
	return writePreviewMessage(command, err)
}

// A preview keeps its historical Markdown default for fzf and pipes. Only an
// explicit JSON request selects the structured Neovim contract.
func previewWantsJSON(command *cobra.Command) bool {
	flag := command.Flags().Lookup("output")
	return flag != nil && flag.Changed && flag.Value.String() == outputJSON
}

// writePreviewMessage writes one error as preview text. The command still
// succeeds, so fzf shows the message instead of a failed preview.
func writePreviewMessage(command *cobra.Command, err error) error {
	text := err.Error()
	if hint := clierr.HintOf(err); hint != "" {
		text += "\n" + hint
	}
	_, writeErr := fmt.Fprintln(command.OutOrStdout(), text)
	return writeErr
}
