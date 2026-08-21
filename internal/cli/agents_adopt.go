package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	transcriptservice "github.com/jpugliesi/tmux-worktree/internal/transcript"
	"github.com/spf13/cobra"
)

// discoverProjectSessions finds the provider sessions of the Project that no
// linked Agent Session uses. It only reads: the provider stores and the twt
// state stay unchanged. When the provider roots are absent, the scan finds
// nothing and costs nothing.
func discoverProjectSessions(project domain.Project, stateDir string, linked []domain.AgentSession) ([]transcriptservice.DiscoveredSession, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find home directory: %w", err)
	}
	return transcriptservice.New(home, stateDir).Discover(project, transcriptservice.DiscoverOptions{Linked: linked})
}

// adoptDiscoveredSession registers one discovered provider session as an
// Agent Session with its generated resume command. The discover --adopt path
// and adopt on first touch both use this one registration.
func adoptDiscoveredSession(agents *agentservice.Service, project domain.Project, session transcriptservice.DiscoveredSession) (domain.AgentSession, error) {
	resumeCommand := transcriptservice.ResumeCommand(session.Provider, session.SessionID)
	if len(resumeCommand) == 0 {
		return domain.AgentSession{}, clierr.New(clierr.PreconditionFailed, "provider %q gives no resume command", session.Provider)
	}
	return agents.Register(project, session.Provider, "", "", session.SessionID, resumeCommand)
}

// findOrAdoptAgent resolves one AGENT reference for a Project. A registered
// Agent Session always wins, also when the reference is a prefix. When the
// registered lookup misses, twt scans the provider stores of the Project: a
// discovered session that the reference names, by its exact session ID or by
// a unique session ID prefix, is adopted first. A dry run validates the
// adoption, writes nothing, and returns an unsaved record. The second result
// is true when the reference resolved through discovery.
func findOrAdoptAgent(command *cobra.Command, agents *agentservice.Service, project domain.Project, stateDir, reference string) (domain.AgentSession, bool, error) {
	agent, err := agents.Find(reference)
	if err == nil || clierr.CodeOf(err) != clierr.NotFound {
		return agent, false, err
	}
	session, found, matchErr := matchDiscoveredSession(agents, project, stateDir, reference)
	if matchErr != nil {
		return domain.AgentSession{}, false, matchErr
	}
	if !found {
		return domain.AgentSession{}, false, err
	}
	if isDryRun(command) {
		agent, err := validateAdoption(agents, project, session)
		return agent, true, err
	}
	agent, err = adoptDiscoveredSession(agents, project, session)
	return agent, true, err
}

// matchDiscoveredSession finds the one discovered provider session that the
// reference names. An exact session ID match wins. A prefix must match one
// session only; an ambiguous prefix reports the candidates.
func matchDiscoveredSession(agents *agentservice.Service, project domain.Project, stateDir, reference string) (transcriptservice.DiscoveredSession, bool, error) {
	registered, err := agents.List(project.ID)
	if err != nil {
		return transcriptservice.DiscoveredSession{}, false, err
	}
	sessions, err := discoverProjectSessions(project, stateDir, registered)
	if err != nil {
		return transcriptservice.DiscoveredSession{}, false, err
	}
	matches := []transcriptservice.DiscoveredSession{}
	for _, session := range sessions {
		if session.SessionID == reference {
			return session, true, nil
		}
		if strings.HasPrefix(session.SessionID, reference) {
			matches = append(matches, session)
		}
	}
	switch len(matches) {
	case 0:
		return transcriptservice.DiscoveredSession{}, false, nil
	case 1:
		return matches[0], true, nil
	default:
		ids := make([]string, 0, len(matches))
		for _, match := range matches {
			ids = append(ids, match.SessionID)
		}
		return transcriptservice.DiscoveredSession{}, false, clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "provider session ID prefix %q is ambiguous", reference),
			"Give more characters. The candidates are: %s.", strings.Join(ids, ", "),
		)
	}
}

// validateAdoption checks that twt can adopt the discovered session, and
// returns the Agent Session record that a real run would save. It writes
// nothing.
func validateAdoption(agents *agentservice.Service, project domain.Project, session transcriptservice.DiscoveredSession) (domain.AgentSession, error) {
	resumeCommand := transcriptservice.ResumeCommand(session.Provider, session.SessionID)
	if len(resumeCommand) == 0 {
		return domain.AgentSession{}, clierr.New(clierr.PreconditionFailed, "provider %q gives no resume command", session.Provider)
	}
	if err := agents.ValidateRegistration(project, session.Provider, "", session.SessionID, resumeCommand); err != nil {
		return domain.AgentSession{}, err
	}
	existing, err := agents.List(project.ID)
	if err != nil {
		return domain.AgentSession{}, err
	}
	return agentservice.BuildSession(project, session.Provider, "", "", session.SessionID, resumeCommand, existing, time.Now().UTC())
}

// notRegisteredForRemoval maps a not-found removal error to invalid usage
// when the reference names a discovered provider session. twt does not adopt
// a session only to delete its record.
func notRegisteredForRemoval(agents *agentservice.Service, project domain.Project, stateDir, reference string, err error) error {
	if err == nil || clierr.CodeOf(err) != clierr.NotFound {
		return err
	}
	if _, found, matchErr := matchDiscoveredSession(agents, project, stateDir, reference); matchErr == nil && found {
		return clierr.WithHint(
			clierr.New(clierr.InvalidUsage, "the session %q is not registered", reference),
			"twt deletes only registered Agent Session records. The provider keeps the session.",
		)
	}
	return err
}
