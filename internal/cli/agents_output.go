package cli

import (
	"time"

	agentservice "github.com/jpugliesi/tmux-worktree/internal/agent"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	transcriptservice "github.com/jpugliesi/tmux-worktree/internal/transcript"
)

type agentOutput struct {
	ID string `json:"id"`
	// ProviderSessionID is the raw provider session ID. twt2 never returns
	// the provider file path.
	ProviderSessionID string            `json:"providerSessionId,omitempty"`
	ProjectID         string            `json:"projectId"`
	Provider          string            `json:"provider"`
	Label             string            `json:"label"`
	Status            string            `json:"status"`
	CreatedAt         string            `json:"createdAt"`
	UpdatedAt         string            `json:"updatedAt"`
	Capabilities      agentCapabilities `json:"capabilities"`
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

// toAgentOutput describes one Agent Session. With probeLive false, twt2 does
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
		Capabilities: agentCapabilities{
			CanResume: projectActive && (live || len(agent.ResumeCommand) > 0), CanSend: live, CanFocus: live,
			CanReadTranscript: agent.ProviderSessionID != "" && transcriptservice.SupportsProvider(agent.Provider),
		},
	}
}

func boolText(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
