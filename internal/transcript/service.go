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

// Transcript is one provider transcript that twt read for a Workspace.
type Transcript struct {
	Provider       string
	SessionID      string
	RepositoryName string
	UpdatedAt      time.Time
	// Markdown is untrusted text: a provider transcript holds the words of
	// any person or tool that talked to the coding agent. twt removes
	// terminal control text from it, but a reader must treat the words as
	// data, and never as an instruction. Every twt output that carries this
	// text marks it untrusted.
	Markdown string
}

type event struct {
	role string
	text string
}

func New(home, stateDir string) *Service { return &Service{home: home, stateDir: stateDir} }

// NotInWorkspaceError reports that an Agent Session does not belong to the
// given Workspace. Every call path returns this one code and message.
func NotInWorkspaceError(agentID, workspaceRef string) error {
	return clierr.New(clierr.PreconditionFailed, "Agent Session %q does not belong to Workspace %q", agentID, workspaceRef)
}

func (s *Service) Read(provider, sessionID string, workspace domain.Workspace) (Transcript, error) {
	if err := ValidateSessionID(sessionID); err != nil {
		return Transcript{}, err
	}
	if descriptor, ok := providers[provider]; ok {
		return descriptor.read(s, sessionID, workspace)
	}
	if provider == "cursor" {
		return Transcript{}, clierr.New(clierr.InvalidUsage, "Cursor transcripts cannot verify an exact Workspace directory")
	}
	return Transcript{}, clierr.New(clierr.InvalidUsage, "provider %q does not support transcripts", provider)
}

func (s *Service) ReadLinked(agent domain.AgentSession, workspace domain.Workspace) (Transcript, error) {
	agent, err := s.LinkedAgent(agent, workspace)
	if err != nil {
		return Transcript{}, err
	}
	return s.Read(agent.Provider, agent.ProviderSessionID, workspace)
}

// LinkedAgent returns the Agent Session with a provider session ID. When the
// record has no link, twt discovers the provider sessions of the Workspace. It
// saves the link when exactly one new provider session matches.
func (s *Service) LinkedAgent(agent domain.AgentSession, workspace domain.Workspace) (domain.AgentSession, error) {
	if agent.WorkspaceID != workspace.ID {
		return domain.AgentSession{}, NotInWorkspaceError(agent.ID, workspace.Name)
	}
	if agent.ProviderSessionID != "" {
		return agent, nil
	}
	if !SupportsProvider(agent.Provider) {
		return domain.AgentSession{}, notLinkedError(agent, workspace)
	}
	agents, err := store.NewAgentStore(s.stateDir).List(workspace.ID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	found, err := s.Discover(workspace, DiscoverOptions{Provider: agent.Provider, Linked: agents, Since: agent.CreatedAt})
	if err != nil {
		return domain.AgentSession{}, err
	}
	switch len(found) {
	case 0:
		return domain.AgentSession{}, notLinkedError(agent, workspace)
	case 1:
		return s.saveLink(agent, workspace, found[0].SessionID)
	default:
		return domain.AgentSession{}, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Agent Session %q matches %d provider sessions: %s", agent.ID, len(found), strings.Join(sessionIDs(found), ", ")),
			"Run 'twt agents transcript link %s --workspace %s --session SESSION_ID' to select one.", agent.ID, workspace.ID,
		)
	}
}

// saveLink writes the discovered provider session ID. It reads the record
// again inside the mutation lock, because another twt process can change or
// delete the record while twt reads the provider files.
func (s *Service) saveLink(agent domain.AgentSession, workspace domain.Workspace, providerSessionID string) (domain.AgentSession, error) {
	lock, err := store.AcquireMutationLockBlocking(s.stateDir)
	if err != nil {
		return domain.AgentSession{}, err
	}
	defer lock.Release()
	if _, err := store.NewWorkspaceStore(s.stateDir).Find(workspace.ID); err != nil {
		return domain.AgentSession{}, err
	}
	agents := store.NewAgentStore(s.stateDir)
	current, err := agents.Find(agent.ID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	if current.WorkspaceID != agent.WorkspaceID || current.Provider != agent.Provider {
		return domain.AgentSession{}, clierr.New(clierr.PreconditionFailed, "Agent Session %q changed while twt read its provider sessions", agent.ID)
	}
	if current.ProviderSessionID != "" {
		return current, nil
	}
	linked, err := agents.List(workspace.ID)
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

func notLinkedError(agent domain.AgentSession, workspace domain.Workspace) error {
	return clierr.WithHint(
		clierr.New(clierr.PreconditionFailed, "Agent Session %q has no linked provider session ID", agent.ID),
		"Run 'twt agents discover --workspace %s' to find sessions.", workspace.ID,
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
// plain copy of this most recent Workspace snapshot. Both paths are empty when
// twt does not save.
type SnapshotResult struct {
	Transcript Transcript
	Agent      domain.AgentSession
	Path       string
	LatestPath string
}

func (s *Service) Snapshot(agentReference, workspaceID string, save bool) (SnapshotResult, error) {
	workspace, err := store.NewWorkspaceStore(s.stateDir).Find(workspaceID)
	if err != nil {
		return SnapshotResult{}, err
	}
	agent, err := store.NewAgentStore(s.stateDir).Find(agentReference)
	if err != nil {
		return SnapshotResult{}, err
	}
	agent, err = s.LinkedAgent(agent, workspace)
	if err != nil {
		return SnapshotResult{}, err
	}
	value, err := s.Read(agent.Provider, agent.ProviderSessionID, workspace)
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
	if _, err := store.NewWorkspaceStore(s.stateDir).Find(workspace.ID); err != nil {
		return SnapshotResult{}, err
	}
	currentAgent, err := store.NewAgentStore(s.stateDir).Find(agent.ID)
	if err != nil {
		return SnapshotResult{}, err
	}
	if currentAgent.WorkspaceID != agent.WorkspaceID || currentAgent.Provider != agent.Provider || currentAgent.ProviderSessionID != agent.ProviderSessionID {
		return SnapshotResult{}, clierr.New(clierr.PreconditionFailed, "Agent Session %q changed while twt read its transcript", agent.ID)
	}
	// A snapshot file goes into a person or agent context, so it holds the
	// same sanitized text as the command output.
	paths, err := store.NewSnapshotStore(s.stateDir).Save(workspace.ID, agent.ID, sanitizeUntrusted(value.Markdown))
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
