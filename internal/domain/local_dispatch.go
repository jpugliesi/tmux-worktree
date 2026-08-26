package domain

import (
	"fmt"
	"strings"
	"time"
)

const LocalDispatchSessionVersion = 1

// DefaultLocalDispatchMaxConcurrency bounds concurrent local dispatch
// sessions per Project. Local Workspaces cost worktrees, tmux windows, and a
// running agent process each, so the default is lower than the cloud one.
const DefaultLocalDispatchMaxConcurrency = 2

type LocalDispatchStatus string

// Local dispatch statuses. There is no *_unknown pair: a local launch fails
// definitely, never uncertainly.
const (
	LocalDispatchCreating  LocalDispatchStatus = "creating"
	LocalDispatchRunning   LocalDispatchStatus = "running"
	LocalDispatchFinished  LocalDispatchStatus = "finished"
	LocalDispatchFailed    LocalDispatchStatus = "failed"
	LocalDispatchCancelled LocalDispatchStatus = "cancelled"
)

// LocalDispatchSession is one local implementation run for one Ticket: a
// Workspace plus one implementation Agent Session, tracked as a durable
// session record. The Workspace carries its own Template snapshot, so the
// session stores only the Template name.
type LocalDispatchSession struct {
	Version            int                 `json:"version"`
	ID                 string              `json:"id"`
	TicketSlug         string              `json:"ticketSlug"`
	Project            string              `json:"project"`
	TemplateName       string              `json:"templateName"`
	Mode               DispatchMode        `json:"mode"`
	Provider           string              `json:"provider"`
	Status             LocalDispatchStatus `json:"status"`
	Claimant           string              `json:"claimant"`
	PromptSnapshot     string              `json:"promptSnapshot"`
	WorkspaceID        string              `json:"workspaceId,omitempty"`
	WorkspaceName      string              `json:"workspaceName,omitempty"`
	TmuxSession        string              `json:"tmuxSession,omitempty"`
	AgentSessionID     string              `json:"agentSessionId,omitempty"`
	AgentLabel         string              `json:"agentLabel,omitempty"`
	Error              *DispatchError      `json:"error,omitempty"`
	TicketTransitioned bool                `json:"ticketTransitioned,omitempty"`
	// StackBase records the stack parent ("blocker-slug@branch") when this
	// session was dispatched stacked.
	StackBase   string     `json:"stackBase,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

func (s LocalDispatchSession) Active() bool {
	switch s.Status {
	case LocalDispatchCreating, LocalDispatchRunning:
		return true
	default:
		return false
	}
}

func (s LocalDispatchSession) Validate() error {
	if s.Version != LocalDispatchSessionVersion {
		return fmt.Errorf("unsupported local dispatch Session version %d", s.Version)
	}
	for name, value := range map[string]string{
		"ID": s.ID, "Ticket slug": s.TicketSlug, "Project": s.Project,
		"Workspace Template": s.TemplateName, "claimant": s.Claimant, "provider": s.Provider,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("local dispatch Session has no %s", name)
		}
	}
	switch s.Mode {
	case DispatchModeAgent, DispatchModePlan:
	default:
		return fmt.Errorf("local dispatch Session mode %q is invalid", s.Mode)
	}
	switch s.Status {
	case LocalDispatchCreating, LocalDispatchRunning,
		LocalDispatchFinished, LocalDispatchFailed, LocalDispatchCancelled:
	default:
		return fmt.Errorf("local dispatch Session status %q is invalid", s.Status)
	}
	if s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() {
		return fmt.Errorf("local dispatch Session timestamps are incomplete")
	}
	return nil
}
