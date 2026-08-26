package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/agentprovider"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
	tmuxclient "github.com/jpugliesi/tmux-worktree/internal/tmux"
	"github.com/jpugliesi/tmux-worktree/internal/transcript"
)

type Service struct {
	stateDir string
	store    store.AgentStore
	tmux     tmuxclient.Client
	now      func() time.Time
}

func NewService(stateDir, tmuxSocket string) *Service {
	return &Service{stateDir: stateDir, store: store.NewAgentStore(stateDir), tmux: tmuxclient.Client{Socket: tmuxSocket}, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Register(workspace domain.Workspace, provider, label, pane, providerSessionID string, resumeCommand []string) (domain.AgentSession, error) {
	lock, err := store.AcquireMutationLock(s.stateDir)
	if err != nil {
		return domain.AgentSession{}, err
	}
	defer lock.Release()
	workspace, err = store.NewWorkspaceStore(s.stateDir).Find(workspace.ID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	provider, providerSessionID, existing, err := s.validateRegistration(workspace, provider, pane, providerSessionID, resumeCommand)
	if err != nil {
		return domain.AgentSession{}, err
	}
	agent, err := newSession(workspace, provider, label, pane, providerSessionID, resumeCommand, existing, s.now())
	if err != nil {
		return domain.AgentSession{}, err
	}
	if pane != "" {
		if err := s.attachPane(workspace, &agent, pane); err != nil {
			return domain.AgentSession{}, err
		}
	}
	if err := s.store.Save(agent); err != nil {
		return domain.AgentSession{}, err
	}
	return agent, nil
}

// BuildSession makes one new Agent Session record. It does not take a lock,
// read tmux, or write the store. Register uses it inside the mutation lock.
// Workspace setup also uses it, because the mutation lock is already held
// there. The caller gives the Agent Sessions that the Workspace has now, and
// must record the pane identity and save the record.
func BuildSession(workspace domain.Workspace, provider, label, pane, providerSessionID string, resumeCommand []string, existing []domain.AgentSession, now time.Time) (domain.AgentSession, error) {
	provider, providerSessionID, err := inferRegistration(provider, providerSessionID, resumeCommand)
	if err != nil {
		return domain.AgentSession{}, err
	}
	if !validProvider(provider) {
		return domain.AgentSession{}, clierr.New(clierr.InvalidUsage, "unsupported agent provider %q", provider)
	}
	return newSession(workspace, provider, label, pane, providerSessionID, resumeCommand, existing, now)
}

// newSession makes the Agent Session record from a normalized provider and
// provider session ID.
func newSession(workspace domain.Workspace, provider, label, pane, providerSessionID string, resumeCommand []string, existing []domain.AgentSession, now time.Time) (domain.AgentSession, error) {
	label, err := resolveLabel(label, provider, existing)
	if err != nil {
		return domain.AgentSession{}, err
	}
	id, err := agentID()
	if err != nil {
		return domain.AgentSession{}, err
	}
	return domain.AgentSession{
		Version: domain.AgentVersion, ID: id, WorkspaceID: workspace.ID, Provider: provider, Label: label,
		ProviderSessionID: providerSessionID, TmuxPane: pane,
		ResumeCommand: append([]string(nil), resumeCommand...), CreatedAt: now, UpdatedAt: now,
	}, nil
}

// MatchesProvider reports whether a pane that runs these commands is a safe
// direct process for the provider of the Agent Session.
func MatchesProvider(paneCommand, paneStart, provider string, resumeCommand []string) bool {
	return commandMatchesProvider(paneCommand, provider, resumeCommand) &&
		commandMatchesProvider(startCommand(paneStart), provider, resumeCommand)
}

func (s *Service) ValidateRegistration(workspace domain.Workspace, provider, pane, providerSessionID string, resumeCommand []string) error {
	provider, _, _, err := s.validateRegistration(workspace, provider, pane, providerSessionID, resumeCommand)
	if err != nil {
		return err
	}
	if pane == "" {
		return nil
	}
	command, start, err := s.tmux.PaneProcess(pane, workspace.ID)
	if err != nil {
		return clierr.Wrap(clierr.PreconditionFailed, fmt.Errorf("tmux pane %q cannot be registered for Workspace %q: %w", pane, workspace.Name, err))
	}
	if !MatchesProvider(command, start, provider, resumeCommand) {
		return clierr.New(clierr.PreconditionFailed, "pane command %q does not match provider %q", command, provider)
	}
	return nil
}

// validateRegistration checks one registration and returns the normalized
// provider and provider session ID with the Agent Sessions that the Workspace
// has now. Inference and validation run once; Register consumes the result.
func (s *Service) validateRegistration(workspace domain.Workspace, provider, pane, providerSessionID string, resumeCommand []string) (string, string, []domain.AgentSession, error) {
	if workspace.Status != domain.WorkspaceActive {
		return "", "", nil, workspaceNotActiveError(workspace)
	}
	if pane == "" && len(resumeCommand) == 0 {
		return "", "", nil, clierr.New(clierr.InvalidUsage, "set a live --pane or give a resume command after --")
	}
	provider, providerSessionID, err := inferRegistration(provider, providerSessionID, resumeCommand)
	if err != nil {
		return "", "", nil, err
	}
	if !validProvider(provider) {
		return "", "", nil, clierr.New(clierr.InvalidUsage, "unsupported agent provider %q", provider)
	}
	existing, err := s.store.List(workspace.ID)
	if err != nil {
		return "", "", nil, err
	}
	if providerSessionID != "" {
		if !transcript.SupportsProvider(provider) {
			return "", "", nil, clierr.New(clierr.InvalidUsage, "provider %q does not support verifiable linked transcripts", provider)
		}
		if err := transcript.ValidateSessionID(providerSessionID); err != nil {
			return "", "", nil, err
		}
		for _, agent := range existing {
			if agent.Provider == provider && agent.ProviderSessionID == providerSessionID {
				return "", "", nil, clierr.New(clierr.AlreadyExists, "provider session %q is already linked to Agent Session %q", providerSessionID, agent.ID)
			}
		}
	}
	return provider, providerSessionID, existing, nil
}

// attachPane records the pane process identity on the Agent Session and
// claims the pane, after it verifies that the pane runs the provider.
func (s *Service) attachPane(workspace domain.Workspace, session *domain.AgentSession, pane string) error {
	command, start, err := s.tmux.PaneProcess(pane, workspace.ID)
	if err != nil {
		return err
	}
	if !MatchesProvider(command, start, session.Provider, session.ResumeCommand) {
		return clierr.New(clierr.PreconditionFailed, "pane command %q does not match provider %q", command, session.Provider)
	}
	session.TmuxPane = pane
	session.PaneCommand, session.PaneStart = command, start
	session.RuntimeReference = ""
	session.PaneRootProcessID = 0
	session.PaneRootStarted = ""
	session.ProcessID = 0
	session.ProcessStarted = ""
	session.ProcessCommand = ""
	session.ProcessEvidence = ""
	return s.tmux.ClaimAgentPane(pane, workspace.ID, session.ID)
}

// StartDeclared starts the Agent Session process in its own Workspace window,
// verifies and claims the new pane, and saves the record. Like BuildSession,
// it takes no mutation lock: Workspace setup calls it while the caller already
// holds the global mutation lock, so a lock here would deadlock.
func (s *Service) StartDeclared(workspace domain.Workspace, session domain.AgentSession, start, env []string) (domain.AgentSession, error) {
	pane, err := s.tmux.StartAgent(workspace, session.Label, start, env)
	if err != nil {
		return session, err
	}
	if err := s.attachStartedPane(workspace, &session, pane); err != nil {
		return session, err
	}
	session.UpdatedAt = s.now()
	if err := s.store.Save(session); err != nil {
		return session, err
	}
	return session, nil
}

// attachStartedPane attaches a direct provider process. Cursor's agent command
// is a wrapper, so its verified provider can be a foreground child process.
// cursorStartTimeout bounds the wait for the cursor-agent wrapper script to
// exec the real provider process. A first run in a fresh Workspace can take
// several seconds before the pane shows the provider.
var cursorStartTimeout = 20 * time.Second

// debugAgentStart appends one line to the file that TWT_DEBUG_AGENT_START
// names. It is a temporary diagnostic and off by default.
func debugAgentStart(format string, a ...any) {
	path := os.Getenv("TWT_DEBUG_AGENT_START")
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	fmt.Fprintf(file, format+"\n", a...)
}

func (s *Service) attachStartedPane(workspace domain.Workspace, session *domain.AgentSession, pane string) error {
	directErr := s.attachPane(workspace, session, pane)
	if directErr == nil || session.Provider != "cursor" {
		return directErr
	}
	deadline := time.Now().Add(cursorStartTimeout)
	for time.Now().Before(deadline) {
		panes, err := s.tmux.ObserveWorkspace(workspace)
		debugAgentStart("observe err=%v panes=%d want=%s", err, len(panes), pane)
		if err == nil {
			for _, observedPane := range panes {
				if observedPane.ID != pane {
					continue
				}
				process, ok := observedProviderProcess(observedPane, session.Provider)
				debugAgentStart("pane=%s current=%q dead=%v foreground=%d ok=%v process=%+v",
					observedPane.ID, observedPane.CurrentCommand, observedPane.Dead, len(observedPane.Foreground), ok, process)
				if !ok {
					break
				}
				session.TmuxPane = pane
				session.PaneCommand = observedPane.CurrentCommand
				session.PaneStart = observedPane.StartCommand
				session.RuntimeReference = livePaneReference(workspace.ID, observedPane, process, session.Provider)
				session.PaneRootProcessID = observedPane.RootProcessID
				session.PaneRootStarted = observedPane.RootStarted
				session.ProcessID = process.ID
				session.ProcessStarted = process.Started
				session.ProcessCommand = process.Command
				session.ProcessEvidence = tmuxclient.ProcessEvidence(process)
				if err := s.tmux.ClaimAgentPane(pane, workspace.ID, session.ID); err != nil {
					return err
				}
				binding, _ := processBinding(*session)
				if s.tmux.ProcessPaneBelongs(workspace, pane, session.ID, binding, false) {
					return nil
				}
				_ = s.tmux.ReleaseAgentPane(pane, workspace.ID, session.ID)
				return clierr.New(clierr.PreconditionFailed, "the Cursor provider process changed during Agent Session start")
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return directErr
}

// ValidateLabel checks that the display label is free inside the Workspace. An
// empty label always passes, because twt makes a unique default label.
func (s *Service) ValidateLabel(workspaceID, label string) error {
	if strings.TrimSpace(label) == "" {
		return nil
	}
	existing, err := s.store.List(workspaceID)
	if err != nil {
		return err
	}
	_, err = resolveLabel(label, "", existing)
	return err
}

// resolveLabel returns the label for a new Agent Session. An explicit label
// must be free. An empty label becomes the provider name, then the provider
// name with a number, inside the Workspace.
func resolveLabel(label, provider string, existing []domain.AgentSession) (string, error) {
	used := map[string]bool{}
	for _, agent := range existing {
		used[agent.Label] = true
	}
	label = strings.TrimSpace(label)
	if label != "" {
		if used[label] {
			return "", clierr.WithHint(
				clierr.New(clierr.AlreadyExists, "Agent Session label %q is already in use in this Workspace", label),
				"Give a different --label, or do not set --label.",
			)
		}
		return label, nil
	}
	for number := 1; ; number++ {
		candidate := provider
		if number > 1 {
			candidate = fmt.Sprintf("%s-%d", provider, number)
		}
		if !used[candidate] {
			return candidate, nil
		}
	}
}

// inferRegistration fills in the provider and the provider session ID from
// the resume command. An explicit value always wins.
func inferRegistration(provider, providerSessionID string, resumeCommand []string) (string, string, error) {
	if provider == "" {
		if len(resumeCommand) == 0 {
			return "", "", clierr.WithHint(
				clierr.New(clierr.InvalidUsage, "twt cannot infer the provider"),
				"Set --provider PROVIDER, or give a resume command after --.",
			)
		}
		provider = inferProvider(resumeCommand)
		if provider == "" {
			return "", "", clierr.WithHint(
				clierr.New(clierr.InvalidUsage, "twt cannot infer the provider from the resume command %q", resumeCommand[0]),
				"Set --provider PROVIDER.",
			)
		}
	}
	if providerSessionID == "" && transcript.SupportsProvider(provider) {
		providerSessionID = inferProviderSessionID(resumeCommand)
	}
	return provider, providerSessionID, nil
}

// inferProvider reads the provider from the program name of the resume
// command. A shell program name gives no provider.
func inferProvider(resumeCommand []string) string {
	return agentprovider.IdentifyCommand(resumeCommand)
}

// inferProviderSessionID reads the provider session ID from a resume command
// such as "codex resume ID", "claude --resume ID", "claude --resume=ID",
// "grok --resume ID", or "grok --session ID".
func inferProviderSessionID(resumeCommand []string) string {
	for index := 1; index < len(resumeCommand); index++ {
		argument := resumeCommand[index]
		if value, found := strings.CutPrefix(argument, "--resume="); found {
			return sessionIDArgument(value)
		}
		if value, found := strings.CutPrefix(argument, "--session="); found {
			return sessionIDArgument(value)
		}
		if argument != "resume" && argument != "--resume" && argument != "-r" && argument != "--session" {
			continue
		}
		if index+1 < len(resumeCommand) {
			return sessionIDArgument(resumeCommand[index+1])
		}
	}
	return ""
}

func sessionIDArgument(value string) string {
	if value == "" || strings.HasPrefix(value, "-") || transcript.ValidateSessionID(value) != nil {
		return ""
	}
	return value
}

func workspaceNotActiveError(workspace domain.Workspace) error {
	if workspace.Status == domain.WorkspaceArchived {
		return clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Workspace %q is archived", workspace.Name),
			"Run 'twt workspaces open %s' to open it.", workspace.Name,
		)
	}
	return clierr.New(clierr.PreconditionFailed, "Workspace %q setup is not complete", workspace.Name)
}

func (s *Service) List(workspaceID string) ([]domain.AgentSession, error) {
	return s.store.List(workspaceID)
}

func (s *Service) Find(reference string) (domain.AgentSession, error) { return s.store.Find(reference) }

func (s *Service) LinkTranscript(agentID, workspaceID, providerSessionID string) (domain.AgentSession, error) {
	lock, err := store.AcquireMutationLock(s.stateDir)
	if err != nil {
		return domain.AgentSession{}, err
	}
	defer lock.Release()
	agent, err := s.validateTranscriptLink(agentID, workspaceID, providerSessionID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	agent.ProviderSessionID = providerSessionID
	agent.UpdatedAt = s.now()
	if err := s.store.Save(agent); err != nil {
		return domain.AgentSession{}, err
	}
	return agent, nil
}

func (s *Service) ValidateTranscriptLink(agentID, workspaceID, providerSessionID string) error {
	_, err := s.validateTranscriptLink(agentID, workspaceID, providerSessionID)
	return err
}

func (s *Service) validateTranscriptLink(agentID, workspaceID, providerSessionID string) (domain.AgentSession, error) {
	if _, err := store.NewWorkspaceStore(s.stateDir).Find(workspaceID); err != nil {
		return domain.AgentSession{}, err
	}
	agent, err := s.store.Find(agentID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	if agent.WorkspaceID != workspaceID {
		return domain.AgentSession{}, transcript.NotInWorkspaceError(agent.ID, workspaceID)
	}
	if !transcript.SupportsProvider(agent.Provider) {
		return domain.AgentSession{}, clierr.New(clierr.InvalidUsage, "provider %q does not support verifiable linked transcripts", agent.Provider)
	}
	if err := transcript.ValidateSessionID(providerSessionID); err != nil {
		return domain.AgentSession{}, err
	}
	agents, err := s.store.List(workspaceID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	for _, existing := range agents {
		if existing.ID != agent.ID && existing.Provider == agent.Provider && existing.ProviderSessionID == providerSessionID {
			return domain.AgentSession{}, clierr.New(clierr.AlreadyExists, "provider session %q is already linked to Agent Session %q", providerSessionID, existing.ID)
		}
	}
	return agent, nil
}

// ProbeResult is one reusable liveness observation for an Agent Session.
type ProbeResult struct {
	Live   bool
	Ready  bool
	Checks []tmuxclient.PaneCheck
}

// Probe reads all liveness checks once. Ready is stricter than Live for an
// adopted shell-hosted process because sending needs the Agent input target.
func (s *Service) Probe(agent domain.AgentSession) ProbeResult {
	if binding, ok := processBinding(agent); ok {
		workspace, err := store.NewWorkspaceStore(s.stateDir).Find(agent.WorkspaceID)
		if err != nil {
			return ProbeResult{Checks: []tmuxclient.PaneCheck{{Name: "Workspace", OK: false}}}
		}
		checks := s.tmux.ExplainProcessPane(workspace, agent.TmuxPane, agent.ID, binding, true)
		live, ready := true, true
		for index, check := range checks {
			if index < len(checks)-1 && !check.OK {
				live = false
			}
			if !check.OK {
				ready = false
			}
		}
		return ProbeResult{Live: live, Ready: ready, Checks: checks}
	}
	checks := s.tmux.ExplainPane(agent.TmuxPane, agent.WorkspaceID, agent.ID, agent.PaneCommand, agent.PaneStart)
	live := true
	for _, check := range checks {
		if !check.Advisory && !check.OK {
			live = false
		}
	}
	return ProbeResult{Live: live, Ready: live, Checks: checks}
}

func (s *Service) IsLive(agent domain.AgentSession) bool {
	return s.Probe(agent).Live
}

// CanSend reports whether the saved Agent Session is live and its provider
// process is ready to receive terminal input.
func (s *Service) CanSend(agent domain.AgentSession) bool {
	return s.Probe(agent).Ready
}

func (s *Service) Resume(agent domain.AgentSession, workspace domain.Workspace) (domain.AgentSession, error) {
	lock, err := store.AcquireMutationLock(s.stateDir)
	if err != nil {
		return agent, err
	}
	defer lock.Release()
	current, err := s.store.Find(agent.ID)
	if err != nil {
		return agent, err
	}
	agent = current
	workspace, err = store.NewWorkspaceStore(s.stateDir).Find(agent.WorkspaceID)
	if err != nil {
		return agent, err
	}
	if err := s.ValidateResume(agent, workspace); err != nil {
		return agent, err
	}
	if s.IsLive(agent) {
		return agent, s.Focus(agent)
	}
	return s.StartDeclared(workspace, agent, EffectiveResumeCommand(agent), agent.Env)
}

// Live returns the Agent Sessions of the Workspace that run live in their
// owned tmux panes.
func (s *Service) Live(workspaceID string) ([]domain.AgentSession, error) {
	agents, err := s.store.List(workspaceID)
	if err != nil {
		return nil, err
	}
	live := []domain.AgentSession{}
	for _, agent := range agents {
		if s.IsLive(agent) {
			live = append(live, agent)
		}
	}
	return live, nil
}

func (s *Service) ValidateResume(agent domain.AgentSession, workspace domain.Workspace) error {
	if workspace.ID != agent.WorkspaceID {
		return transcript.NotInWorkspaceError(agent.ID, workspace.Name)
	}
	if workspace.Status != domain.WorkspaceActive {
		return workspaceNotActiveError(workspace)
	}
	if !s.IsLive(agent) && len(EffectiveResumeCommand(agent)) == 0 {
		return clierr.New(clierr.PreconditionFailed, "Agent Session %q has no live pane or resume command", agent.ID)
	}
	return nil
}

// EffectiveResumeCommand selects the command that continues or restarts one
// Agent Session. New prompt-bearing declarations can prefer a verified linked
// session. Old records keep the saved-command-first rule.
func EffectiveResumeCommand(agent domain.AgentSession) []string {
	linked := transcript.ResumeCommand(agent.Provider, agent.ProviderSessionID)
	if agent.PreferProviderResume && len(linked) > 0 {
		return linked
	}
	if len(agent.ResumeCommand) > 0 {
		return append([]string(nil), agent.ResumeCommand...)
	}
	return linked
}

// NotLiveError reports that the Agent Session process is not live in its
// owned pane, and tells the user how to start the Agent Session again.
func NotLiveError(agentID string) error { return tmuxclient.NotLiveError(agentID) }

// ExplainLiveness returns one check for each Agent Session pane predicate.
func (s *Service) ExplainLiveness(agent domain.AgentSession) []tmuxclient.PaneCheck {
	return s.Probe(agent).Checks
}

// Remove deletes the Agent Session record. It does not stop a live process.
func (s *Service) Remove(reference, workspaceID string) (domain.AgentSession, error) {
	lock, err := store.AcquireMutationLock(s.stateDir)
	if err != nil {
		return domain.AgentSession{}, err
	}
	defer lock.Release()
	agent, err := s.ValidateRemove(reference, workspaceID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	if err := s.store.Delete(agent.ID); err != nil {
		return domain.AgentSession{}, err
	}
	return agent, nil
}

func (s *Service) ValidateRemove(reference, workspaceID string) (domain.AgentSession, error) {
	agent, err := s.store.Find(reference)
	if err != nil {
		return domain.AgentSession{}, err
	}
	if workspaceID != "" && agent.WorkspaceID != workspaceID {
		return domain.AgentSession{}, transcript.NotInWorkspaceError(agent.ID, workspaceID)
	}
	return agent, nil
}

func (s *Service) Focus(agent domain.AgentSession) error {
	if binding, ok := processBinding(agent); ok {
		workspace, err := store.NewWorkspaceStore(s.stateDir).Find(agent.WorkspaceID)
		if err != nil {
			return err
		}
		return s.tmux.FocusProcess(workspace, agent.TmuxPane, agent.ID, binding)
	}
	return s.tmux.Focus(agent.TmuxPane, agent.WorkspaceID, agent.ID, agent.PaneCommand, agent.PaneStart)
}

func (s *Service) Send(agent domain.AgentSession, workspaceID, text string) error {
	if agent.WorkspaceID != workspaceID {
		return transcript.NotInWorkspaceError(agent.ID, workspaceID)
	}
	if text == "" {
		return clierr.New(clierr.InvalidUsage, "feedback input is empty")
	}
	if binding, ok := processBinding(agent); ok {
		workspace, err := store.NewWorkspaceStore(s.stateDir).Find(agent.WorkspaceID)
		if err != nil {
			return err
		}
		return s.tmux.SendProcess(workspace, agent.TmuxPane, agent.ID, binding, text)
	}
	return s.tmux.Send(agent.TmuxPane, agent.WorkspaceID, agent.ID, agent.PaneCommand, agent.PaneStart, text)
}

func processBinding(agent domain.AgentSession) (tmuxclient.ProcessBinding, bool) {
	binding := tmuxclient.ProcessBinding{
		PaneRootID: agent.PaneRootProcessID, PaneRootStarted: agent.PaneRootStarted,
		ID: agent.ProcessID, Started: agent.ProcessStarted, Command: agent.ProcessCommand,
		Evidence: agent.ProcessEvidence, ReadyCommand: agent.PaneCommand,
	}
	return binding, binding.PaneRootID > 0 && binding.PaneRootStarted != "" &&
		binding.ID > 0 && binding.Started != "" && binding.Command != "" && binding.Evidence != ""
}

func startCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], "'\"")
}

func commandMatchesProvider(command, provider string, resumeCommand []string) bool {
	command = strings.ToLower(filepath.Base(command))
	if provider == "command" {
		if len(resumeCommand) == 0 {
			return false
		}
		expected := strings.ToLower(filepath.Base(resumeCommand[0]))
		if command != expected {
			return false
		}
		switch command {
		case "sh", "bash", "zsh", "fish", "dash", "ksh", "csh", "tcsh":
			return false
		default:
			return true
		}
	}
	return agentprovider.IdentifyCommand([]string{command}) == provider
}

func validProvider(provider string) bool { return domain.ValidAgentProvider(provider) }

func agentID() (string, error) {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("create Agent Session ID: %w", err)
	}
	return hex.EncodeToString(data), nil
}
