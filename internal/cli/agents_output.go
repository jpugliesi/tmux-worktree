package cli

import (
	"fmt"
	"sort"
	"time"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	transcriptservice "github.com/jpugliesi/tmux-worktree/internal/transcript"
)

type agentOutput struct {
	ID string `json:"id"`
	// ProviderSessionID is the raw provider session ID. twt never returns
	// the provider file path.
	ProviderSessionID string `json:"providerSessionId,omitempty"`
	ProjectID         string `json:"projectId"`
	Provider          string `json:"provider"`
	Label             string `json:"label"`
	Status            string `json:"status"`
	CreatedAt         string `json:"createdAt,omitempty"`
	UpdatedAt         string `json:"updatedAt,omitempty"`
	// LastActivity is set for a discovered provider session only. It is the
	// last write time of the provider transcript.
	LastActivity string            `json:"lastActivity,omitempty"`
	Capabilities agentCapabilities `json:"capabilities"`
	// recency is the sort key: UpdatedAt for a registered session, or
	// LastActivity for a discovered session.
	recency time.Time `json:"-"`
}

type agentCapabilities struct {
	CanResume         bool `json:"canResume"`
	CanSend           bool `json:"canSend"`
	CanFocus          bool `json:"canFocus"`
	CanReadTranscript bool `json:"canReadTranscript"`
}

type agentsListOutput struct {
	SchemaVersion int           `json:"schemaVersion"`
	ProjectID     string        `json:"projectId"`
	Agents        []agentOutput `json:"agents"`
	TotalCount    int           `json:"totalCount"`
	Truncated     bool          `json:"truncated,omitempty"`
}

type agentShowOutput struct {
	SchemaVersion int          `json:"schemaVersion"`
	Agent         agentOutput  `json:"agent"`
	Liveness      []agentCheck `json:"liveness"`
}

type agentCheck struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Advisory bool   `json:"advisory,omitempty"`
}

type agentsDiscoverOutput struct {
	SchemaVersion int                `json:"schemaVersion"`
	ProjectID     string             `json:"projectId"`
	Sessions      []discoveredOutput `json:"sessions"`
	TotalCount    int                `json:"totalCount"`
	Truncated     bool               `json:"truncated,omitempty"`
	Adopted       []string           `json:"adopted,omitempty"`
	Status        string             `json:"status,omitempty"`
}

type discoveredOutput struct {
	Provider     string `json:"provider"`
	SessionID    string `json:"sessionId"`
	Repository   string `json:"repository"`
	LastActivity string `json:"lastActivity"`
}

type agentActionOutput struct {
	SchemaVersion int         `json:"schemaVersion"`
	Agent         agentOutput `json:"agent"`
}

type agentSendOutput struct {
	SchemaVersion int    `json:"schemaVersion"`
	AgentID       string `json:"agentId"`
	Status        string `json:"status"`
}

// toAgentOutput describes one Agent Session. With probeLive false, twt does
// not ask tmux for the state of the pane: the status is "unknown" and the
// capabilities that need a live pane are false.
func toAgentOutput(service *agentservice.Service, agent domain.AgentSession, projectActive, probeLive bool) agentOutput {
	live := false
	status := "unknown"
	if probeLive {
		live = service.IsLive(agent)
		status = "stopped"
		if live {
			status = "live"
		}
	}
	return agentOutput{
		ID: agent.ID, ProviderSessionID: agent.ProviderSessionID, ProjectID: agent.ProjectID,
		Provider: agent.Provider, Label: agent.Label, Status: status,
		CreatedAt: agent.CreatedAt.Format(time.RFC3339),
		UpdatedAt: agent.UpdatedAt.Format(time.RFC3339),
		recency:   agent.UpdatedAt,
		Capabilities: agentCapabilities{
			CanResume: projectActive && (live || len(agent.ResumeCommand) > 0), CanSend: live, CanFocus: live,
			CanReadTranscript: agent.ProviderSessionID != "" && transcriptservice.SupportsProvider(agent.Provider),
		},
	}
}

// discoveredAgentOutput describes one discovered provider session in the
// Agent Session list. The session is not registered yet: its ID is the
// provider session ID, and the first action on it adopts it.
func discoveredAgentOutput(project domain.Project, session transcriptservice.DiscoveredSession) agentOutput {
	return agentOutput{
		ID: session.SessionID, ProviderSessionID: session.SessionID, ProjectID: project.ID,
		Provider: session.Provider, Label: session.Provider, Status: "discovered",
		LastActivity: session.LastActivity.UTC().Format(time.RFC3339),
		recency:      session.LastActivity.UTC(),
		Capabilities: agentCapabilities{
			CanResume: project.Status == domain.ProjectActive, CanSend: false, CanFocus: false, CanReadTranscript: true,
		},
	}
}

// sortAgentsForDisplay puts the newest Agent Session first. Registered and
// discovered sessions share one recency order.
func sortAgentsForDisplay(outputs []agentOutput) {
	sort.SliceStable(outputs, func(i, j int) bool {
		return outputs[i].recency.After(outputs[j].recency)
	})
}

// agentListLine writes one Agent Session list row: provider, ID, and age.
func agentListLine(output agentOutput, now time.Time) string {
	return fmt.Sprintf("%s\t%s\t%s", output.Provider, output.ID, formatAge(now.Sub(output.recency)))
}

func boolText(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
