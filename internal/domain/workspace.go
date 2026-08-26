package domain

import (
	"encoding/json"
	"fmt"
	"time"
)

const WorkspaceVersion = 1

type WorkspaceStatus string

const (
	WorkspaceInitializing WorkspaceStatus = "initializing"
	WorkspaceActive       WorkspaceStatus = "active"
	WorkspaceArchived     WorkspaceStatus = "archived"
	WorkspaceSetupFailed  WorkspaceStatus = "setup_failed"
	WorkspaceRemoving     WorkspaceStatus = "removing"
)

type StepStatus string

type StepKind string

const (
	StepWorkspaceRoot  StepKind = "workspace_root"
	StepCache          StepKind = "cache"
	StepCheckout       StepKind = "checkout"
	StepRepositoryInit StepKind = "repository_init"
	StepTmux           StepKind = "tmux"
	StepWorkspaceInit  StepKind = "workspace_init"
	StepAgent          StepKind = "agent"
)

const (
	StepPending   StepStatus = "pending"
	StepRunning   StepStatus = "running"
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
	StepUnknown   StepStatus = "unknown"
)

type Workspace struct {
	Version          int             `json:"version"`
	ID               string          `json:"id"`
	EnvironmentID    string          `json:"environmentId,omitempty"`
	Name             string          `json:"name"`
	TemplateName     string          `json:"templateName"`
	TemplateSnapshot Template        `json:"templateSnapshot"`
	Status           WorkspaceStatus `json:"status"`
	// Adopted marks a Workspace that twt made from an existing tmux session.
	// twt did not create its directories, and removal never deletes them:
	// removal only deletes the twt state and releases the session marker.
	Adopted bool `json:"adopted,omitempty"`
	// Project is the durable Ticket Project of this Workspace. It is empty
	// when the Workspace has no linked Tickets or comes from version-one state.
	Project string `json:"project,omitempty"`
	// Tickets are the unique Ticket slugs that this Workspace works on.
	Tickets []string `json:"tickets,omitempty"`
	// BaseRef is the origin branch the checkouts started from when the
	// Workspace was created for a stacked dispatch. Empty means the default
	// branch.
	BaseRef      string                `json:"baseRef,omitempty"`
	Root         string                `json:"root"`
	TmuxSession  string                `json:"tmuxSession"`
	Repositories []WorkspaceRepository `json:"repositories"`
	Steps        []SetupStep           `json:"steps"`
	CreatedAt    time.Time             `json:"createdAt"`
	UpdatedAt    time.Time             `json:"updatedAt"`
	ArchivedAt   *time.Time            `json:"archivedAt,omitempty"`
}

// UnmarshalJSON accepts the version-one singular ticket field. New records
// always write the tickets field.
func (w *Workspace) UnmarshalJSON(data []byte) error {
	type workspaceJSON Workspace
	var value struct {
		workspaceJSON
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*w = Workspace(value.workspaceJSON)
	if value.Ticket != "" && len(w.Tickets) > 0 && (len(w.Tickets) != 1 || w.Tickets[0] != value.Ticket) {
		return fmt.Errorf("Workspace has conflicting ticket and tickets values")
	}
	if len(w.Tickets) == 0 && value.Ticket != "" {
		w.Tickets = []string{value.Ticket}
	}
	return nil
}

type WorkspaceRepository struct {
	Name       string `json:"name"`
	CachePath  string `json:"cachePath"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	WindowName string `json:"windowName"`
}

type SetupStep struct {
	ID         string   `json:"id"`
	Kind       StepKind `json:"kind"`
	Repository string   `json:"repository,omitempty"`
	// Agent is the label of the declared Agent Session of an agent step.
	Agent      string     `json:"agent,omitempty"`
	Status     StepStatus `json:"status"`
	Attempts   int        `json:"attempts"`
	Error      string     `json:"error,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}
