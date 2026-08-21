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
	provider, providerSessionID, existing, err := s.validateRegistration(project, provider, pane, providerSessionID, resumeCommand)
	if err != nil {
		return domain.AgentSession{}, err
	}
	agent, err := newSession(project, provider, label, pane, providerSessionID, resumeCommand, existing, s.now())
	if err != nil {
		return domain.AgentSession{}, err
	}
	if pane != "" {
		if err := s.attachPane(project, &agent, pane); err != nil {
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
// Project setup also uses it, because the mutation lock is already held
// there. The caller gives the Agent Sessions that the Project has now, and
// must record the pane identity and save the record.
func BuildSession(project domain.Project, provider, label, pane, providerSessionID string, resumeCommand []string, existing []domain.AgentSession, now time.Time) (domain.AgentSession, error) {
	provider, providerSessionID, err := inferRegistration(provider, providerSessionID, resumeCommand)
	if err != nil {
		return domain.AgentSession{}, err
	}
	if !validProvider(provider) {
		return domain.AgentSession{}, clierr.New(clierr.InvalidUsage, "unsupported agent provider %q", provider)
	}
	return newSession(project, provider, label, pane, providerSessionID, resumeCommand, existing, now)
}

// newSession makes the Agent Session record from a normalized provider and
// provider session ID.
func newSession(project domain.Project, provider, label, pane, providerSessionID string, resumeCommand []string, existing []domain.AgentSession, now time.Time) (domain.AgentSession, error) {
	label, err := resolveLabel(label, provider, existing)
	if err != nil {
		return domain.AgentSession{}, err
	}
	id, err := agentID()
	if err != nil {
		return domain.AgentSession{}, err
	}
	return domain.AgentSession{
		Version: domain.AgentVersion, ID: id, ProjectID: project.ID, Provider: provider, Label: label,
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

func (s *Service) ValidateRegistration(project domain.Project, provider, pane, providerSessionID string, resumeCommand []string) error {
	provider, _, _, err := s.validateRegistration(project, provider, pane, providerSessionID, resumeCommand)
	if err != nil {
		return err
	}
	if pane == "" {
		return nil
	}
	command, start, err := s.tmux.PaneProcess(pane, project.ID)
	if err != nil {
		return clierr.Wrap(clierr.PreconditionFailed, fmt.Errorf("tmux pane %q cannot be registered for Project %q: %w", pane, project.Name, err))
	}
	if !MatchesProvider(command, start, provider, resumeCommand) {
		return clierr.New(clierr.PreconditionFailed, "pane command %q does not match provider %q", command, provider)
	}
	return nil
}

// validateRegistration checks one registration and returns the normalized
// provider and provider session ID with the Agent Sessions that the Project
// has now. Inference and validation run once; Register consumes the result.
func (s *Service) validateRegistration(project domain.Project, provider, pane, providerSessionID string, resumeCommand []string) (string, string, []domain.AgentSession, error) {
	if project.Status != domain.ProjectActive {
		return "", "", nil, projectNotActiveError(project)
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
	existing, err := s.store.List(project.ID)
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
func (s *Service) attachPane(project domain.Project, session *domain.AgentSession, pane string) error {
	command, start, err := s.tmux.PaneProcess(pane, project.ID)
	if err != nil {
		return err
	}
	if !MatchesProvider(command, start, session.Provider, session.ResumeCommand) {
		return clierr.New(clierr.PreconditionFailed, "pane command %q does not match provider %q", command, session.Provider)
	}
	session.TmuxPane = pane
	session.PaneCommand, session.PaneStart = command, start
	return s.tmux.ClaimAgentPane(pane, project.ID, session.ID)
}

// StartDeclared starts the Agent Session process in its own Project window,
// verifies and claims the new pane, and saves the record. Like BuildSession,
// it takes no mutation lock: Project setup calls it while the caller already
// holds the global mutation lock, so a lock here would deadlock.
func (s *Service) StartDeclared(project domain.Project, session domain.AgentSession, start []string) (domain.AgentSession, error) {
	pane, err := s.tmux.StartAgent(project, session.Label, start)
	if err != nil {
		return session, err
	}
	if err := s.attachPane(project, &session, pane); err != nil {
		return session, err
	}
	session.UpdatedAt = s.now()
	if err := s.store.Save(session); err != nil {
		return session, err
	}
	return session, nil
}

// ValidateLabel checks that the display label is free inside the Project. An
// empty label always passes, because twt makes a unique default label.
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
	for _, provider := range domain.AgentProviders {
		if commandMatchesProvider(resumeCommand[0], provider, resumeCommand) {
			return provider
		}
	}
	return ""
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

func projectNotActiveError(project domain.Project) error {
	if project.Status == domain.ProjectArchived {
		return clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Project %q is archived", project.Name),
			"Run 'twt projects open %s' to open it.", project.Name,
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
		return domain.AgentSession{}, transcript.NotInProjectError(agent.ID, projectID)
	}
	if !transcript.SupportsProvider(agent.Provider) {
		return domain.AgentSession{}, clierr.New(clierr.InvalidUsage, "provider %q does not support verifiable linked transcripts", agent.Provider)
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
			return domain.AgentSession{}, clierr.New(clierr.AlreadyExists, "provider session %q is already linked to Agent Session %q", providerSessionID, existing.ID)
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
	return s.StartDeclared(project, agent, agent.ResumeCommand)
}

// Live returns the Agent Sessions of the Project that run live in their
// owned tmux panes.
func (s *Service) Live(projectID string) ([]domain.AgentSession, error) {
	agents, err := s.store.List(projectID)
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

func (s *Service) ValidateResume(agent domain.AgentSession, project domain.Project) error {
	if project.ID != agent.ProjectID {
		return transcript.NotInProjectError(agent.ID, project.Name)
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
		return domain.AgentSession{}, transcript.NotInProjectError(agent.ID, projectID)
	}
	return agent, nil
}

func (s *Service) Focus(agent domain.AgentSession) error {
	return s.tmux.Focus(agent.TmuxPane, agent.ProjectID, agent.ID, agent.PaneCommand, agent.PaneStart)
}

func (s *Service) Send(agent domain.AgentSession, projectID, text string) error {
	if agent.ProjectID != projectID {
		return transcript.NotInProjectError(agent.ID, projectID)
	}
	if text == "" {
		return clierr.New(clierr.InvalidUsage, "feedback input is empty")
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
	case "codex", "claude", "cursor", "grok":
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

func validProvider(provider string) bool { return domain.ValidAgentProvider(provider) }

func agentID() (string, error) {
	data := make([]byte, 12)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("create Agent Session ID: %w", err)
	}
	return hex.EncodeToString(data), nil
}
