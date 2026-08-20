package transcript

import (
	"strings"
	"time"
	"unicode"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

type Service struct {
	home     string
	stateDir string
}

type Transcript struct {
	Provider       string
	SessionID      string
	RepositoryName string
	UpdatedAt      time.Time
	Markdown       string
}

type event struct {
	role string
	text string
}

func New(home, stateDir string) *Service { return &Service{home: home, stateDir: stateDir} }

// NotInProjectError reports that an Agent Session does not belong to the
// given Project. Every call path returns this one code and message.
func NotInProjectError(agentID, projectRef string) error {
	return clierr.New(clierr.PreconditionFailed, "Agent Session %q does not belong to Project %q", agentID, projectRef)
}

func (s *Service) Read(provider, sessionID string, project domain.Project) (Transcript, error) {
	if err := ValidateSessionID(sessionID); err != nil {
		return Transcript{}, err
	}
	if descriptor, ok := providers[provider]; ok {
		return descriptor.read(s, sessionID, project)
	}
	if provider == "cursor" {
		return Transcript{}, clierr.New(clierr.InvalidUsage, "Cursor transcripts cannot verify an exact Project directory")
	}
	return Transcript{}, clierr.New(clierr.InvalidUsage, "provider %q does not support transcripts", provider)
}

func (s *Service) ReadLinked(agent domain.AgentSession, project domain.Project) (Transcript, error) {
	agent, err := s.LinkedAgent(agent, project)
	if err != nil {
		return Transcript{}, err
	}
	return s.Read(agent.Provider, agent.ProviderSessionID, project)
}

// LinkedAgent returns the Agent Session with a provider session ID. When the
// record has no link, twt discovers the provider sessions of the Project. It
// saves the link when exactly one new provider session matches.
func (s *Service) LinkedAgent(agent domain.AgentSession, project domain.Project) (domain.AgentSession, error) {
	if agent.ProjectID != project.ID {
		return domain.AgentSession{}, NotInProjectError(agent.ID, project.Name)
	}
	if agent.ProviderSessionID != "" {
		return agent, nil
	}
	if !SupportsProvider(agent.Provider) {
		return domain.AgentSession{}, notLinkedError(agent, project)
	}
	agents, err := store.NewAgentStore(s.stateDir).List(project.ID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	found, err := s.Discover(project, DiscoverOptions{Provider: agent.Provider, Linked: agents, Since: agent.CreatedAt})
	if err != nil {
		return domain.AgentSession{}, err
	}
	switch len(found) {
	case 0:
		return domain.AgentSession{}, notLinkedError(agent, project)
	case 1:
		return s.saveLink(agent, project, found[0].SessionID)
	default:
		return domain.AgentSession{}, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Agent Session %q matches %d provider sessions: %s", agent.ID, len(found), strings.Join(sessionIDs(found), ", ")),
			"Run 'twt agents transcript link %s --project %s --session SESSION_ID' to select one.", agent.ID, project.ID,
		)
	}
}

// saveLink writes the discovered provider session ID. It reads the record
// again inside the mutation lock, because another twt process can change or
// delete the record while twt reads the provider files.
func (s *Service) saveLink(agent domain.AgentSession, project domain.Project, providerSessionID string) (domain.AgentSession, error) {
	lock, err := store.AcquireMutationLockBlocking(s.stateDir)
	if err != nil {
		return domain.AgentSession{}, err
	}
	defer lock.Release()
	if _, err := store.NewProjectStore(s.stateDir).Find(project.ID); err != nil {
		return domain.AgentSession{}, err
	}
	agents := store.NewAgentStore(s.stateDir)
	current, err := agents.Find(agent.ID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	if current.ProjectID != agent.ProjectID || current.Provider != agent.Provider {
		return domain.AgentSession{}, clierr.New(clierr.PreconditionFailed, "Agent Session %q changed while twt read its provider sessions", agent.ID)
	}
	if current.ProviderSessionID != "" {
		return current, nil
	}
	linked, err := agents.List(project.ID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	for _, other := range linked {
		if other.ID != current.ID && other.Provider == current.Provider && other.ProviderSessionID == providerSessionID {
			return domain.AgentSession{}, clierr.New(clierr.AlreadyExists, "provider session %q is already linked to Agent Session %q", providerSessionID, other.ID)
		}
	}
	current.ProviderSessionID = providerSessionID
	current.UpdatedAt = time.Now().UTC()
	if err := agents.Save(current); err != nil {
		return domain.AgentSession{}, err
	}
	return current, nil
}

func notLinkedError(agent domain.AgentSession, project domain.Project) error {
	return clierr.WithHint(
		clierr.New(clierr.PreconditionFailed, "Agent Session %q has no linked provider session ID", agent.ID),
		"Run 'twt agents discover --project %s' to find sessions.", project.ID,
	)
}

func sessionIDs(sessions []DiscoveredSession) []string {
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, session.SessionID)
	}
	return ids
}

// SnapshotResult gives the transcript, its Agent Session, and the files that
// twt wrote. Path is the private file of the Agent Session. LatestPath is a
// plain copy of this most recent Project snapshot. Both paths are empty when
// twt does not save.
type SnapshotResult struct {
	Transcript Transcript
	Agent      domain.AgentSession
	Path       string
	LatestPath string
}

func (s *Service) Snapshot(agentReference, projectID string, save bool) (SnapshotResult, error) {
	project, err := store.NewProjectStore(s.stateDir).Find(projectID)
	if err != nil {
		return SnapshotResult{}, err
	}
	agent, err := store.NewAgentStore(s.stateDir).Find(agentReference)
	if err != nil {
		return SnapshotResult{}, err
	}
	agent, err = s.LinkedAgent(agent, project)
	if err != nil {
		return SnapshotResult{}, err
	}
	value, err := s.Read(agent.Provider, agent.ProviderSessionID, project)
	if err != nil {
		return SnapshotResult{}, err
	}
	if !save {
		return SnapshotResult{Transcript: value, Agent: agent}, nil
	}
	lock, err := store.AcquireMutationLockBlocking(s.stateDir)
	if err != nil {
		return SnapshotResult{}, err
	}
	defer lock.Release()
	if _, err := store.NewProjectStore(s.stateDir).Find(project.ID); err != nil {
		return SnapshotResult{}, err
	}
	currentAgent, err := store.NewAgentStore(s.stateDir).Find(agent.ID)
	if err != nil {
		return SnapshotResult{}, err
	}
	if currentAgent.ProjectID != agent.ProjectID || currentAgent.Provider != agent.Provider || currentAgent.ProviderSessionID != agent.ProviderSessionID {
		return SnapshotResult{}, clierr.New(clierr.PreconditionFailed, "Agent Session %q changed while twt read its transcript", agent.ID)
	}
	paths, err := store.NewSnapshotStore(s.stateDir).Save(project.ID, agent.ID, value.Markdown)
	if err != nil {
		return SnapshotResult{}, err
	}
	return SnapshotResult{Transcript: value, Agent: agent, Path: paths.Agent, LatestPath: paths.Latest}, nil
}

func ValidateSessionID(value string) error {
	if len(value) < 3 || len(value) > 256 || strings.Contains(value, "..") || strings.ContainsAny(value, `/\`) {
		return clierr.New(clierr.InvalidUsage, "invalid provider session ID")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return clierr.New(clierr.InvalidUsage, "invalid provider session ID")
		}
	}
	return nil
}

func providerName(provider string) string {
	if provider == "" {
		return "Agent"
	}
	return strings.ToUpper(provider[:1]) + provider[1:]
}
