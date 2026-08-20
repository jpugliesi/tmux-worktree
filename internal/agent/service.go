package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

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

func (s *Service) Register(project domain.Project, provider, label, pane, providerSessionID string, resumeCommand []string) (domain.AgentSession, error) {
	lock, err := store.AcquireMutationLock(s.stateDir)
	if err != nil {
		return domain.AgentSession{}, err
	}
	defer lock.Release()
	project, err = store.NewProjectStore(s.stateDir).Find(project.ID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	if err := s.ValidateRegistration(project, provider, pane, providerSessionID, resumeCommand); err != nil {
		return domain.AgentSession{}, err
	}
	provider, providerSessionID, err = inferRegistration(provider, providerSessionID, resumeCommand)
	if err != nil {
		return domain.AgentSession{}, err
	}
	existing, err := s.store.List(project.ID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	label, err = resolveLabel(label, provider, existing)
	if err != nil {
		return domain.AgentSession{}, err
	}
	id, err := agentID()
	if err != nil {
		return domain.AgentSession{}, err
	}
	now := s.now()
	paneCommand := ""
	paneStart := ""
	if pane != "" {
		paneCommand, paneStart, err = s.tmux.PaneProcess(pane, project.ID)
		if err != nil {
			return domain.AgentSession{}, err
		}
		if err := s.tmux.ClaimAgentPane(pane, project.ID, id); err != nil {
			return domain.AgentSession{}, err
		}
	}
	agent := domain.AgentSession{Version: domain.AgentVersion, ID: id, ProjectID: project.ID, Provider: provider, Label: label, ProviderSessionID: providerSessionID, TmuxPane: pane, PaneCommand: paneCommand, PaneStart: paneStart, ResumeCommand: append([]string(nil), resumeCommand...), CreatedAt: now, UpdatedAt: now}
	if err := s.store.Save(agent); err != nil {
		return domain.AgentSession{}, err
	}
	return agent, nil
}

func (s *Service) ValidateRegistration(project domain.Project, provider, pane, providerSessionID string, resumeCommand []string) error {
	if project.Status != domain.ProjectActive {
		return projectNotActiveError(project)
	}
	if pane == "" && len(resumeCommand) == 0 {
		return clierr.New(clierr.InvalidUsage, "set a live --pane or give a resume command after --")
	}
	provider, providerSessionID, err := inferRegistration(provider, providerSessionID, resumeCommand)
	if err != nil {
		return err
	}
	if !validProvider(provider) {
		return clierr.New(clierr.InvalidUsage, "unsupported agent provider %q", provider)
	}
	if providerSessionID != "" {
		if !transcript.SupportsProvider(provider) {
			return fmt.Errorf("provider %q does not support verifiable linked transcripts", provider)
		}
		if err := transcript.ValidateSessionID(providerSessionID); err != nil {
			return err
		}
		existing, err := s.store.List(project.ID)
		if err != nil {
			return err
		}
		for _, agent := range existing {
			if agent.Provider == provider && agent.ProviderSessionID == providerSessionID {
				return clierr.New(clierr.AlreadyExists, "provider session %q is already linked to Agent Session %q", providerSessionID, agent.ID)
			}
		}
	}
	if pane != "" {
		command, start, err := s.tmux.PaneProcess(pane, project.ID)
		if err != nil {
			return fmt.Errorf("tmux pane %q cannot be registered for Project %q: %w", pane, project.Name, err)
		}
		if !commandMatchesProvider(command, provider, resumeCommand) || !commandMatchesProvider(startCommand(start), provider, resumeCommand) {
			return fmt.Errorf("pane command %q does not match provider %q", command, provider)
		}
	}
	return nil
}

// ValidateLabel checks that the display label is free inside the Project. An
// empty label always passes, because twt2 makes a unique default label.
func (s *Service) ValidateLabel(projectID, label string) error {
	if strings.TrimSpace(label) == "" {
		return nil
	}
	existing, err := s.store.List(projectID)
	if err != nil {
		return err
	}
	_, err = resolveLabel(label, "", existing)
	return err
}

// resolveLabel returns the label for a new Agent Session. An explicit label
// must be free. An empty label becomes the provider name, then the provider
// name with a number, inside the Project.
func resolveLabel(label, provider string, existing []domain.AgentSession) (string, error) {
	used := map[string]bool{}
	for _, agent := range existing {
		used[agent.Label] = true
	}
	label = strings.TrimSpace(label)
	if label != "" {
		if used[label] {
			return "", clierr.WithHint(
				clierr.New(clierr.AlreadyExists, "Agent Session label %q is already in use in this Project", label),
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
				clierr.New(clierr.InvalidUsage, "twt2 cannot infer the provider"),
				"Set --provider PROVIDER, or give a resume command after --.",
			)
		}
		provider = inferProvider(resumeCommand)
		if provider == "" {
			return "", "", clierr.WithHint(
				clierr.New(clierr.InvalidUsage, "twt2 cannot infer the provider from the resume command %q", resumeCommand[0]),
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
	for _, provider := range []string{"codex", "claude", "cursor", "command"} {
		if commandMatchesProvider(resumeCommand[0], provider, resumeCommand) {
			return provider
		}
	}
	return ""
}

// inferProviderSessionID reads the provider session ID from a resume command
// such as "codex resume ID", "claude --resume ID", or "claude --resume=ID".
func inferProviderSessionID(resumeCommand []string) string {
	for index := 1; index < len(resumeCommand); index++ {
		argument := resumeCommand[index]
		if value, found := strings.CutPrefix(argument, "--resume="); found {
			return sessionIDArgument(value)
		}
		if argument != "resume" && argument != "--resume" && argument != "-r" {
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

func projectNotActiveError(project domain.Project) error {
	if project.Status == domain.ProjectArchived {
		return clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Project %q is archived", project.Name),
			"Run 'twt2 projects open %s' to open it.", project.Name,
		)
	}
	return clierr.New(clierr.PreconditionFailed, "Project %q setup is not complete", project.Name)
}

func (s *Service) List(projectID string) ([]domain.AgentSession, error) {
	return s.store.List(projectID)
}

func (s *Service) Find(reference string) (domain.AgentSession, error) { return s.store.Find(reference) }

func (s *Service) LinkTranscript(agentID, projectID, providerSessionID string) (domain.AgentSession, error) {
	lock, err := store.AcquireMutationLock(s.stateDir)
	if err != nil {
		return domain.AgentSession{}, err
	}
	defer lock.Release()
	agent, err := s.validateTranscriptLink(agentID, projectID, providerSessionID)
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

func (s *Service) ValidateTranscriptLink(agentID, projectID, providerSessionID string) error {
	_, err := s.validateTranscriptLink(agentID, projectID, providerSessionID)
	return err
}

func (s *Service) validateTranscriptLink(agentID, projectID, providerSessionID string) (domain.AgentSession, error) {
	if _, err := store.NewProjectStore(s.stateDir).Find(projectID); err != nil {
		return domain.AgentSession{}, err
	}
	agent, err := s.store.Find(agentID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	if agent.ProjectID != projectID {
		return domain.AgentSession{}, fmt.Errorf("Agent Session %q does not belong to Project %q", agent.ID, projectID)
	}
	if !transcript.SupportsProvider(agent.Provider) {
		return domain.AgentSession{}, fmt.Errorf("provider %q does not support verifiable linked transcripts", agent.Provider)
	}
	if err := transcript.ValidateSessionID(providerSessionID); err != nil {
		return domain.AgentSession{}, err
	}
	agents, err := s.store.List(projectID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	for _, existing := range agents {
		if existing.ID != agent.ID && existing.Provider == agent.Provider && existing.ProviderSessionID == providerSessionID {
			return domain.AgentSession{}, fmt.Errorf("provider session %q is already linked to Agent Session %q", providerSessionID, existing.ID)
		}
	}
	return agent, nil
}

func (s *Service) IsLive(agent domain.AgentSession) bool {
	return s.tmux.PaneBelongsToAgent(agent.TmuxPane, agent.ProjectID, agent.ID, agent.PaneCommand, agent.PaneStart)
}

func (s *Service) Resume(agent domain.AgentSession, project domain.Project) (domain.AgentSession, error) {
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
	project, err = store.NewProjectStore(s.stateDir).Find(agent.ProjectID)
	if err != nil {
		return agent, err
	}
	if err := s.ValidateResume(agent, project); err != nil {
		return agent, err
	}
	if s.IsLive(agent) {
		return agent, s.tmux.Focus(agent.TmuxPane, project.ID, agent.ID, agent.PaneCommand, agent.PaneStart)
	}
	pane, err := s.tmux.StartAgent(project, agent.Label, agent.ResumeCommand)
	if err != nil {
		return agent, err
	}
	agent.TmuxPane = pane
	agent.PaneCommand, agent.PaneStart, err = s.tmux.PaneProcess(pane, project.ID)
	if err != nil {
		return agent, err
	}
	if !commandMatchesProvider(agent.PaneCommand, agent.Provider, agent.ResumeCommand) || !commandMatchesProvider(startCommand(agent.PaneStart), agent.Provider, agent.ResumeCommand) {
		return agent, fmt.Errorf("started pane command %q does not match provider %q", agent.PaneCommand, agent.Provider)
	}
	if err := s.tmux.ClaimAgentPane(pane, project.ID, agent.ID); err != nil {
		return agent, err
	}
	agent.UpdatedAt = s.now()
	if err := s.store.Save(agent); err != nil {
		return agent, err
	}
	return agent, nil
}

func (s *Service) ValidateResume(agent domain.AgentSession, project domain.Project) error {
	if project.ID != agent.ProjectID {
		return fmt.Errorf("Agent Session %q does not belong to Project %q", agent.ID, project.Name)
	}
	if project.Status != domain.ProjectActive {
		return projectNotActiveError(project)
	}
	if !s.IsLive(agent) && len(agent.ResumeCommand) == 0 {
		return clierr.New(clierr.PreconditionFailed, "Agent Session %q has no live pane or resume command", agent.ID)
	}
	return nil
}

// NotLiveError reports that the Agent Session process is not live in its
// owned pane, and tells the user how to start the Agent Session again.
func NotLiveError(agentID string) error { return tmuxclient.NotLiveError(agentID) }

// ExplainLiveness returns one check for each Agent Session pane predicate.
func (s *Service) ExplainLiveness(agent domain.AgentSession) []tmuxclient.PaneCheck {
	return s.tmux.ExplainPane(agent.TmuxPane, agent.ProjectID, agent.ID, agent.PaneCommand, agent.PaneStart)
}

// Remove deletes the Agent Session record. It does not stop a live process.
func (s *Service) Remove(reference, projectID string) (domain.AgentSession, error) {
	lock, err := store.AcquireMutationLock(s.stateDir)
	if err != nil {
		return domain.AgentSession{}, err
	}
	defer lock.Release()
	agent, err := s.ValidateRemove(reference, projectID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	if err := s.store.Delete(agent.ID); err != nil {
		return domain.AgentSession{}, err
	}
	return agent, nil
}

func (s *Service) ValidateRemove(reference, projectID string) (domain.AgentSession, error) {
	agent, err := s.store.Find(reference)
	if err != nil {
		return domain.AgentSession{}, err
	}
	if projectID != "" && agent.ProjectID != projectID {
		return domain.AgentSession{}, clierr.New(clierr.PreconditionFailed, "Agent Session %q does not belong to Project %q", agent.ID, projectID)
	}
	return agent, nil
}

func (s *Service) Focus(agent domain.AgentSession) error {
	return s.tmux.Focus(agent.TmuxPane, agent.ProjectID, agent.ID, agent.PaneCommand, agent.PaneStart)
}

func (s *Service) Send(agent domain.AgentSession, projectID, text string) error {
	if agent.ProjectID != projectID {
		return fmt.Errorf("Agent Session %q does not belong to Project %q", agent.ID, projectID)
	}
	if text == "" {
		return fmt.Errorf("feedback input is empty")
	}
	return s.tmux.Send(agent.TmuxPane, agent.ProjectID, agent.ID, agent.PaneCommand, agent.PaneStart, text)
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
	switch provider {
	case "codex", "claude", "cursor":
		return strings.Contains(command, provider)
	case "command":
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
	default:
		return false
	}
}

func validProvider(provider string) bool {
	switch provider {
	case "codex", "claude", "cursor", "command":
		return true
	default:
		return false
	}
}

func agentID() (string, error) {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("create Agent Session ID: %w", err)
	}
	return hex.EncodeToString(data), nil
}
