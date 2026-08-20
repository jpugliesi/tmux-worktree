package transcript

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

type Service struct{ home string }

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

func New(home string) *Service { return &Service{home: home} }

func SupportsProvider(provider string) bool { return provider == "codex" || provider == "claude" }

func (s *Service) Read(provider, sessionID string, project domain.Project) (Transcript, error) {
	if err := ValidateSessionID(sessionID); err != nil {
		return Transcript{}, err
	}
	switch provider {
	case "codex":
		return s.readCodex(sessionID, project)
	case "claude":
		return s.readClaude(sessionID, project)
	case "cursor":
		return Transcript{}, fmt.Errorf("Cursor transcripts cannot verify an exact Project directory")
	default:
		return Transcript{}, fmt.Errorf("provider %q does not support transcripts", provider)
	}
}

func (s *Service) ReadLinked(agent domain.AgentSession, project domain.Project) (Transcript, error) {
	if agent.ProjectID != project.ID {
		return Transcript{}, fmt.Errorf("Agent Session %q does not belong to Project %q", agent.ID, project.Name)
	}
	if agent.ProviderSessionID == "" {
		return Transcript{}, fmt.Errorf("Agent Session %q has no linked provider session ID; register it with --session", agent.ID)
	}
	return s.Read(agent.Provider, agent.ProviderSessionID, project)
}

func ValidateSessionID(value string) error {
	if len(value) < 3 || len(value) > 256 || strings.Contains(value, "..") || strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("invalid provider session ID")
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return fmt.Errorf("invalid provider session ID")
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
