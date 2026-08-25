package domain

import (
	"fmt"
	"strings"
	"time"
)

const CursorCloudSessionVersion = 1

type CursorCloudMode string

const (
	CursorCloudModeAgent CursorCloudMode = "agent"
	CursorCloudModePlan  CursorCloudMode = "plan"
)

type CursorCloudStatus string

const (
	CursorCloudCreating        CursorCloudStatus = "creating"
	CursorCloudCreatingUnknown CursorCloudStatus = "creating_unknown"
	CursorCloudRunning         CursorCloudStatus = "running"
	CursorCloudRunUnknown      CursorCloudStatus = "run_unknown"
	CursorCloudFinished        CursorCloudStatus = "finished"
	CursorCloudFailed          CursorCloudStatus = "failed"
	CursorCloudCancelled       CursorCloudStatus = "cancelled"
)

// CursorCloudSession is one remote Cursor Agent conversation for one Ticket.
// Runs on the same remote agent append to RunHistory.
type CursorCloudSession struct {
	Version              int                     `json:"version"`
	ID                   string                  `json:"id"`
	TicketSlug           string                  `json:"ticketSlug"`
	Project              string                  `json:"project"`
	TemplateName         string                  `json:"templateName"`
	TemplateSnapshot     Template                `json:"templateSnapshot"`
	Mode                 CursorCloudMode         `json:"mode"`
	Status               CursorCloudStatus       `json:"status"`
	Claimant             string                  `json:"claimant"`
	PromptSnapshot       string                  `json:"promptSnapshot"`
	CreateIdempotencyKey string                  `json:"createIdempotencyKey"`
	SendIdempotencyKey   string                  `json:"sendIdempotencyKey"`
	CursorAgentID        string                  `json:"cursorAgentId,omitempty"`
	RunID                string                  `json:"runId,omitempty"`
	RequestID            string                  `json:"requestId,omitempty"`
	EffectiveEffort      CursorCloudEffort       `json:"effectiveEffort,omitempty"`
	RunHistory           []CursorCloudRun        `json:"runHistory,omitempty"`
	Repositories         []CursorCloudRepository `json:"repositories"`
	Result               string                  `json:"result,omitempty"`
	Error                *CursorCloudError       `json:"error,omitempty"`
	HandoffIncomplete    bool                    `json:"handoffIncomplete,omitempty"`
	TicketTransitioned   bool                    `json:"ticketTransitioned,omitempty"`
	CreatedAt            time.Time               `json:"createdAt"`
	UpdatedAt            time.Time               `json:"updatedAt"`
	CompletedAt          *time.Time              `json:"completedAt,omitempty"`
}

type CursorCloudRun struct {
	ID        string            `json:"id"`
	RequestID string            `json:"requestId,omitempty"`
	Status    CursorCloudStatus `json:"status"`
	CreatedAt time.Time         `json:"createdAt"`
}

type CursorCloudRepository struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	StartingRef string `json:"startingRef,omitempty"`
	Branch      string `json:"branch,omitempty"`
	PRURL       string `json:"prUrl,omitempty"`
}

type CursorCloudError struct {
	Kind      string `json:"kind,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
	HelpURL   string `json:"helpUrl,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

type CursorCloudEffort struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

func (s CursorCloudSession) Active() bool {
	switch s.Status {
	case CursorCloudCreating, CursorCloudCreatingUnknown, CursorCloudRunning, CursorCloudRunUnknown:
		return true
	default:
		return false
	}
}

func (s CursorCloudSession) Validate() error {
	if s.Version != CursorCloudSessionVersion {
		return fmt.Errorf("unsupported Cursor Cloud Session version %d", s.Version)
	}
	for name, value := range map[string]string{
		"ID": s.ID, "Ticket slug": s.TicketSlug, "Project": s.Project,
		"Workspace Template": s.TemplateName, "claimant": s.Claimant,
		"create idempotency key": s.CreateIdempotencyKey, "send idempotency key": s.SendIdempotencyKey,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("Cursor Cloud Session has no %s", name)
		}
	}
	if s.TemplateSnapshot.Name != s.TemplateName {
		return fmt.Errorf("Cursor Cloud Session Template snapshot %q does not match %q", s.TemplateSnapshot.Name, s.TemplateName)
	}
	switch s.Mode {
	case CursorCloudModeAgent, CursorCloudModePlan:
	default:
		return fmt.Errorf("Cursor Cloud Session mode %q is invalid", s.Mode)
	}
	switch s.Status {
	case CursorCloudCreating, CursorCloudCreatingUnknown, CursorCloudRunning, CursorCloudRunUnknown,
		CursorCloudFinished, CursorCloudFailed, CursorCloudCancelled:
	default:
		return fmt.Errorf("Cursor Cloud Session status %q is invalid", s.Status)
	}
	if s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() {
		return fmt.Errorf("Cursor Cloud Session timestamps are incomplete")
	}
	return nil
}
