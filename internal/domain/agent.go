package domain

import "time"

const AgentVersion = 1

type AgentSession struct {
	Version       int       `json:"version"`
	ID            string    `json:"id"`
	ProjectID     string    `json:"projectId"`
	Provider      string    `json:"provider"`
	Label         string    `json:"label"`
	ResumeCommand []string  `json:"resumeCommand,omitempty"`
	TmuxPane      string    `json:"tmuxPane,omitempty"`
	PaneCommand   string    `json:"paneCommand,omitempty"`
	PaneStart     string    `json:"paneStart,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
