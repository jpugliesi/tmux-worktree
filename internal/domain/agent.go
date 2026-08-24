package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

const AgentVersion = 1

type AgentSession struct {
	Version           int       `json:"version"`
	ID                string    `json:"id"`
	WorkspaceID       string    `json:"workspaceId"`
	Provider          string    `json:"provider"`
	Label             string    `json:"label"`
	ProviderSessionID string    `json:"providerSessionId,omitempty"`
	ResumeCommand     []string  `json:"resumeCommand,omitempty"`
	TmuxPane          string    `json:"tmuxPane,omitempty"`
	PaneCommand       string    `json:"paneCommand,omitempty"`
	PaneStart         string    `json:"paneStart,omitempty"`
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
