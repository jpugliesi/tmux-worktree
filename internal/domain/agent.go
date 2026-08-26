package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

const AgentVersion = 1

type AgentSession struct {
	Version           int      `json:"version"`
	ID                string   `json:"id"`
	WorkspaceID       string   `json:"workspaceId"`
	Provider          string   `json:"provider"`
	Label             string   `json:"label"`
	ProviderSessionID string   `json:"providerSessionId,omitempty"`
	ResumeCommand     []string `json:"resumeCommand,omitempty"`
	// PreferProviderResume makes a linked provider session take precedence
	// over the saved fallback command. Old records keep the saved-command-first
	// behavior through the false zero value.
	PreferProviderResume bool `json:"preferProviderResume,omitempty"`
	// Env sets KEY=VALUE pairs in the Agent Session window on start and on
	// resume.
	Env               []string  `json:"env,omitempty"`
	TmuxPane          string    `json:"tmuxPane,omitempty"`
	PaneCommand       string    `json:"paneCommand,omitempty"`
	PaneStart         string    `json:"paneStart,omitempty"`
	RuntimeReference  string    `json:"runtimeReference,omitempty"`
	PaneRootProcessID int       `json:"paneRootProcessId,omitempty"`
	PaneRootStarted   string    `json:"paneRootStarted,omitempty"`
	ProcessID         int       `json:"processId,omitempty"`
	ProcessStarted    string    `json:"processStarted,omitempty"`
	ProcessCommand    string    `json:"processCommand,omitempty"`
	ProcessEvidence   string    `json:"processEvidence,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// UnmarshalJSON accepts the version-one projectId key. New records always
// write workspaceId.
func (a *AgentSession) UnmarshalJSON(data []byte) error {
	type agentSessionJSON AgentSession
	var value struct {
		agentSessionJSON
		ProjectID string `json:"projectId"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*a = AgentSession(value.agentSessionJSON)
	if a.WorkspaceID != "" && value.ProjectID != "" && a.WorkspaceID != value.ProjectID {
		return fmt.Errorf("Agent Session has conflicting workspaceId and projectId values")
	}
	if a.WorkspaceID == "" {
		a.WorkspaceID = value.ProjectID
	}
	return nil
}
