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
	WorkspaceID       string `json:"workspaceId"`
	Provider          string `json:"provider"`
	Label             string `json:"label"`
	Status            string `json:"status"`
	Registration      string `json:"registration,omitempty"`
	Runtime           string `json:"runtime,omitempty"`
	RepositoryName    string `json:"repositoryName,omitempty"`
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
	CanResume             bool `json:"canResume"`
	CanSend               bool `json:"canSend"`
	CanFocus              bool `json:"canFocus"`
	CanPreview            bool `json:"canPreview"`
	CanReadTranscript     bool `json:"canReadTranscript"`
	CanSnapshotTranscript bool `json:"canSnapshotTranscript"`
}

type agentsListOutput struct {
	SchemaVersion int           `json:"schemaVersion"`
	WorkspaceID   string        `json:"workspaceId"`
	Agents        []agentOutput `json:"agents"`
	Complete      bool          `json:"complete"`
	Diagnostics   []string      `json:"diagnostics,omitempty"`
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
	WorkspaceID   string             `json:"workspaceId"`
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
func toAgentOutput(service *agentservice.Service, agent domain.AgentSession, workspaceActive, probeLive bool) agentOutput {
	if !probeLive {
		return toAgentOutputFields(agent, workspaceActive, false, false, "unknown")
	}
	return toAgentOutputWithProbe(agent, workspaceActive, service.Probe(agent))
}

func toAgentOutputWithProbe(agent domain.AgentSession, workspaceActive bool, probe agentservice.ProbeResult) agentOutput {
	output := toAgentOutputFields(agent, workspaceActive, probe.Live, probe.Ready, "stopped")
	if probe.Live {
		output.Status = "live"
	}
	return output
}

func toAgentOutputFields(agent domain.AgentSession, workspaceActive, live, ready bool, status string) agentOutput {
	return agentOutput{
		ID: agent.ID, ProviderSessionID: agent.ProviderSessionID, WorkspaceID: agent.WorkspaceID,
		Provider: agent.Provider, Label: agent.Label, Status: status,
		CreatedAt: agent.CreatedAt.Format(time.RFC3339), UpdatedAt: agent.UpdatedAt.Format(time.RFC3339), recency: agent.UpdatedAt,
		Capabilities: agentCapabilities{
			CanResume: workspaceActive && (live || len(agent.ResumeCommand) > 0), CanSend: ready, CanFocus: live,
			CanPreview:            agent.ProviderSessionID != "" && transcriptservice.SupportsProvider(agent.Provider),
			CanReadTranscript:     agent.ProviderSessionID != "" && transcriptservice.SupportsProvider(agent.Provider),
			CanSnapshotTranscript: agent.ProviderSessionID != "" && transcriptservice.SupportsProvider(agent.Provider),
		},
	}
}

func catalogAgentOutput(workspace domain.Workspace, entry agentservice.CatalogEntry) agentOutput {
	return agentOutput{
		ID: entry.Reference, ProviderSessionID: entry.ProviderSessionID, WorkspaceID: workspace.ID,
		Provider: entry.Provider, Label: entry.Label, Status: entry.Status,
		Registration: entry.Registration, Runtime: entry.Runtime, RepositoryName: entry.RepositoryName,
		CreatedAt: formatOptionalTime(entry.CreatedAt), UpdatedAt: formatOptionalTime(entry.UpdatedAt),
		LastActivity: formatOptionalTime(entry.LastActivity), recency: entry.LastActivity,
		Capabilities: agentCapabilities{
			CanResume: entry.CanResume, CanSend: entry.CanSend, CanFocus: entry.CanFocus, CanPreview: entry.CanPreview,
			CanReadTranscript: entry.CanSnapshot, CanSnapshotTranscript: entry.CanSnapshot,
		},
	}
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
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
