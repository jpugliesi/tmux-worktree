package domain

import "time"

const ProjectVersion = 1

type ProjectStatus string

const (
	ProjectInitializing ProjectStatus = "initializing"
	ProjectActive       ProjectStatus = "active"
	ProjectArchived     ProjectStatus = "archived"
	ProjectSetupFailed  ProjectStatus = "setup_failed"
	ProjectRemoving     ProjectStatus = "removing"
)

type StepStatus string

type StepKind string

const (
	StepProjectRoot    StepKind = "project_root"
	StepCache          StepKind = "cache"
	StepCheckout       StepKind = "checkout"
	StepRepositoryInit StepKind = "repository_init"
	StepTmux           StepKind = "tmux"
	StepProjectInit    StepKind = "project_init"
	StepAgent          StepKind = "agent"
)

const (
	StepPending   StepStatus = "pending"
	StepRunning   StepStatus = "running"
	StepSucceeded StepStatus = "succeeded"
	StepFailed    StepStatus = "failed"
	StepUnknown   StepStatus = "unknown"
)

type Project struct {
	Version          int                 `json:"version"`
	ID               string              `json:"id"`
	EnvironmentID    string              `json:"environmentId,omitempty"`
	Name             string              `json:"name"`
	TemplateName     string              `json:"templateName"`
	TemplateSnapshot Template            `json:"templateSnapshot"`
	Status           ProjectStatus       `json:"status"`
	Root             string              `json:"root"`
	TmuxSession      string              `json:"tmuxSession"`
	Repositories     []ProjectRepository `json:"repositories"`
	Steps            []SetupStep         `json:"steps"`
	CreatedAt        time.Time           `json:"createdAt"`
	UpdatedAt        time.Time           `json:"updatedAt"`
	ArchivedAt       *time.Time          `json:"archivedAt,omitempty"`
}

type ProjectRepository struct {
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
