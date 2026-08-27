package domain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

const (
	PreparedEnvironmentVersion = 3
	PreparationFormatVersion   = 1
)

type PreparedEnvironmentStatus string

const (
	EnvironmentQueued    PreparedEnvironmentStatus = "queued"
	EnvironmentPreparing PreparedEnvironmentStatus = "preparing"
	EnvironmentReady     PreparedEnvironmentStatus = "ready"
	EnvironmentClaiming  PreparedEnvironmentStatus = "claiming"
	EnvironmentClaimed   PreparedEnvironmentStatus = "claimed"
	EnvironmentReleasing PreparedEnvironmentStatus = "releasing"
	EnvironmentFailed    PreparedEnvironmentStatus = "failed"
)

type PreparedEnvironment struct {
	Version          int                       `json:"version"`
	FormatVersion    int                       `json:"formatVersion"`
	ID               string                    `json:"id"`
	TemplateName     string                    `json:"templateName"`
	TemplateDigest   string                    `json:"templateDigest"`
	TemplateSnapshot Template                  `json:"templateSnapshot"`
	Status           PreparedEnvironmentStatus `json:"status"`
	Root             string                    `json:"root"`
	Repositories     []PreparedRepository      `json:"repositories"`
	Steps            []SetupStep               `json:"steps"`
	QueueToken       string                    `json:"queueToken"`
	QueuedAt         time.Time                 `json:"queuedAt"`
	Generation       uint64                    `json:"generation,omitempty"`
	Assignment       *EnvironmentAssignment    `json:"assignment,omitempty"`
	ReadyAt          *time.Time                `json:"readyAt,omitempty"`
	CreatedAt        time.Time                 `json:"createdAt"`
	UpdatedAt        time.Time                 `json:"updatedAt"`
	Failure          string                    `json:"failure,omitempty"`
}

type PreparedRepository struct {
	Name       string `json:"name"`
	CachePath  string `json:"cachePath"`
	Path       string `json:"path"`
	WindowName string `json:"windowName"`
	BaseCommit string `json:"baseCommit"`
}

// EnvironmentClaim records the complete Workspace reserved for one claim. Code
// that resumes an interrupted claim must use this value without changing it.
type EnvironmentAssignmentKind string
type EnvironmentAssignmentPhase string

const (
	EnvironmentAssignmentClaim    EnvironmentAssignmentKind  = "claim"
	EnvironmentAssignmentRelease  EnvironmentAssignmentKind  = "release"
	EnvironmentAssignmentReserved EnvironmentAssignmentPhase = "reserved"
	EnvironmentAssignmentActive   EnvironmentAssignmentPhase = "active"
	// EnvironmentAssignmentSessionStopPending means cleanup finished, but the
	// source tmux session can still change the prepared worktrees.
	EnvironmentAssignmentSessionStopPending EnvironmentAssignmentPhase = "session_stop_pending"
)

type EnvironmentAssignment struct {
	Generation  uint64                     `json:"generation"`
	Kind        EnvironmentAssignmentKind  `json:"kind"`
	Phase       EnvironmentAssignmentPhase `json:"phase"`
	Workspace   Workspace                  `json:"workspace"`
	Fingerprint string                     `json:"fingerprint,omitempty"`
	Force       bool                       `json:"force,omitempty"`
	// SourceSessionID identifies the tmux session that must disappear before
	// twt can return a released Environment to the ready pool.
	SourceSessionID string    `json:"sourceSessionId,omitempty"`
	ReservedAt      time.Time `json:"reservedAt"`
}

// UnmarshalJSON accepts the version-one project key. New records always use
// workspace. A record with two different owners is invalid.
func (c *EnvironmentAssignment) UnmarshalJSON(data []byte) error {
	var value struct {
		Generation      uint64                     `json:"generation"`
		Kind            EnvironmentAssignmentKind  `json:"kind"`
		Phase           EnvironmentAssignmentPhase `json:"phase"`
		Workspace       *Workspace                 `json:"workspace"`
		Project         *Workspace                 `json:"project"`
		Fingerprint     string                     `json:"fingerprint"`
		Force           bool                       `json:"force"`
		SourceSessionID string                     `json:"sourceSessionId"`
		ReservedAt      time.Time                  `json:"reservedAt"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Workspace != nil && value.Project != nil && !reflect.DeepEqual(value.Workspace, value.Project) {
		return fmt.Errorf("Prepared Environment claim has conflicting workspace and project values")
	}
	switch {
	case value.Workspace != nil:
		c.Workspace = *value.Workspace
	case value.Project != nil:
		c.Workspace = *value.Project
	}
	c.Generation = value.Generation
	c.Kind = value.Kind
	c.Phase = value.Phase
	c.Fingerprint = value.Fingerprint
	c.Force = value.Force
	c.SourceSessionID = value.SourceSessionID
	c.ReservedAt = value.ReservedAt
	return nil
}

// UnmarshalJSON reads version-one claimReservation state and normalizes it in
// memory. A read alone does not change the state file.
func (e *PreparedEnvironment) UnmarshalJSON(data []byte) error {
	type environmentJSON PreparedEnvironment
	var value struct {
		environmentJSON
		ClaimReservation *EnvironmentAssignment `json:"claimReservation"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*e = PreparedEnvironment(value.environmentJSON)
	if e.Version != 1 && e.Version != 2 && e.Version != PreparedEnvironmentVersion {
		return fmt.Errorf("unsupported Prepared Environment version %d", e.Version)
	}
	if e.Assignment != nil && value.ClaimReservation != nil && !reflect.DeepEqual(e.Assignment, value.ClaimReservation) {
		return fmt.Errorf("Prepared Environment has conflicting assignment and claimReservation values")
	}
	if e.Assignment == nil {
		e.Assignment = value.ClaimReservation
	}
	if e.Assignment != nil {
		if e.Assignment.Generation == 0 {
			e.Assignment.Generation = 1
		}
		if e.Assignment.Kind == "" {
			e.Assignment.Kind = EnvironmentAssignmentClaim
		}
		if e.Assignment.Phase == "" {
			e.Assignment.Phase = EnvironmentAssignmentReserved
			if e.Status == EnvironmentClaimed {
				e.Assignment.Phase = EnvironmentAssignmentActive
			}
		}
	}
	if e.Generation == 0 && e.Assignment != nil {
		e.Generation = e.Assignment.Generation
	}
	e.Version = PreparedEnvironmentVersion
	return nil
}

func (e PreparedEnvironment) Validate() error {
	if e.Version != PreparedEnvironmentVersion {
		return fmt.Errorf("unsupported Prepared Environment version %d: expected %d", e.Version, PreparedEnvironmentVersion)
	}
	if e.FormatVersion != PreparationFormatVersion {
		return fmt.Errorf("unsupported preparation format version %d: expected %d", e.FormatVersion, PreparationFormatVersion)
	}
	if e.ID == "" {
		return fmt.Errorf("Prepared Environment ID is empty")
	}
	if e.TemplateName == "" {
		return fmt.Errorf("Prepared Environment %q has no Workspace Template name", e.ID)
	}
	if e.TemplateDigest == "" {
		return fmt.Errorf("Prepared Environment %q has no Workspace Template digest", e.ID)
	}
	if e.TemplateSnapshot.Name != e.TemplateName {
		return fmt.Errorf("Prepared Environment %q has Workspace Template name %q but its snapshot has name %q", e.ID, e.TemplateName, e.TemplateSnapshot.Name)
	}
	if err := e.TemplateSnapshot.Validate(); err != nil {
		return fmt.Errorf("Prepared Environment %q has an invalid Workspace Template: %w", e.ID, err)
	}
	if !validPreparedEnvironmentStatus(e.Status) {
		return fmt.Errorf("Prepared Environment %q has invalid status %q", e.ID, e.Status)
	}
	if e.Root == "" {
		return fmt.Errorf("Prepared Environment %q has no root", e.ID)
	}
	if e.QueueToken == "" {
		return fmt.Errorf("Prepared Environment %q has no queue token", e.ID)
	}
	if e.QueuedAt.IsZero() {
		return fmt.Errorf("Prepared Environment %q has no queue time", e.ID)
	}
	if e.CreatedAt.IsZero() || e.UpdatedAt.IsZero() {
		return fmt.Errorf("Prepared Environment %q has incomplete timestamps", e.ID)
	}
	// ReadyAt is optional. Records that twt wrote before this field exists do
	// not have it.
	if e.ReadyAt != nil && e.ReadyAt.IsZero() {
		return fmt.Errorf("Prepared Environment %q has an empty ready time", e.ID)
	}
	assignment := e.Assignment
	if assignment != nil {
		if assignment.Workspace.ID == "" || assignment.Workspace.Version != WorkspaceVersion {
			return fmt.Errorf("Prepared Environment %q has an invalid Workspace claim reservation", e.ID)
		}
		if assignment.Workspace.EnvironmentID != e.ID {
			return fmt.Errorf("Prepared Environment %q has an assignment for environment %q", e.ID, assignment.Workspace.EnvironmentID)
		}
		if assignment.ReservedAt.IsZero() {
			return fmt.Errorf("Prepared Environment %q has no assignment time", e.ID)
		}
		if assignment.Generation == 0 || assignment.Generation != e.Generation {
			return fmt.Errorf("Prepared Environment %q has an invalid assignment generation", e.ID)
		}
		if assignment.Kind != EnvironmentAssignmentClaim && assignment.Kind != EnvironmentAssignmentRelease {
			return fmt.Errorf("Prepared Environment %q has invalid assignment kind %q", e.ID, assignment.Kind)
		}
		if assignment.Phase == "" {
			return fmt.Errorf("Prepared Environment %q has no assignment phase", e.ID)
		}
		if assignment.Phase == EnvironmentAssignmentSessionStopPending && assignment.SourceSessionID == "" {
			return fmt.Errorf("Prepared Environment %q has a pending session stop without a source session ID", e.ID)
		}
	}
	switch e.Status {
	case EnvironmentClaiming, EnvironmentClaimed, EnvironmentReleasing:
		if assignment == nil {
			return fmt.Errorf("Prepared Environment %q has status %q without a Workspace assignment", e.ID, e.Status)
		}
		if e.Status == EnvironmentReleasing && assignment.Kind != EnvironmentAssignmentRelease {
			return fmt.Errorf("Prepared Environment %q has releasing status with assignment kind %q", e.ID, assignment.Kind)
		}
		if e.Status != EnvironmentReleasing && assignment.Kind != EnvironmentAssignmentClaim {
			return fmt.Errorf("Prepared Environment %q has status %q with assignment kind %q", e.ID, e.Status, assignment.Kind)
		}
		if e.Status == EnvironmentClaimed && assignment.Phase != EnvironmentAssignmentActive {
			return fmt.Errorf("Prepared Environment %q has claimed status with assignment phase %q", e.ID, assignment.Phase)
		}
		if e.Status == EnvironmentReleasing && assignment.Phase != EnvironmentAssignmentReserved && assignment.Phase != EnvironmentAssignmentSessionStopPending {
			return fmt.Errorf("Prepared Environment %q has releasing status with assignment phase %q", e.ID, assignment.Phase)
		}
		if e.Status != EnvironmentClaimed && e.Status != EnvironmentReleasing && assignment.Phase != EnvironmentAssignmentReserved {
			return fmt.Errorf("Prepared Environment %q has status %q with assignment phase %q", e.ID, e.Status, assignment.Phase)
		}
	case EnvironmentQueued, EnvironmentPreparing, EnvironmentReady, EnvironmentFailed:
		if assignment != nil {
			return fmt.Errorf("Prepared Environment %q has status %q with a Workspace assignment", e.ID, e.Status)
		}
	}
	if e.Status == EnvironmentReady || e.Status == EnvironmentClaiming || e.Status == EnvironmentClaimed || e.Status == EnvironmentReleasing {
		if len(e.Repositories) != len(e.TemplateSnapshot.Repositories) {
			return fmt.Errorf("Prepared Environment %q has incomplete prepared repositories", e.ID)
		}
		for _, repository := range e.Repositories {
			if repository.Name == "" || repository.CachePath == "" || repository.Path == "" || repository.BaseCommit == "" {
				return fmt.Errorf("Prepared Environment %q has incomplete prepared repository %q", e.ID, repository.Name)
			}
		}
		if len(e.Steps) == 0 {
			return fmt.Errorf("Prepared Environment %q has no preparation steps", e.ID)
		}
		for _, step := range e.Steps {
			if step.Status != StepSucceeded {
				return fmt.Errorf("Prepared Environment %q has incomplete preparation step %q", e.ID, step.ID)
			}
		}
	}
	return nil
}

func validPreparedEnvironmentStatus(status PreparedEnvironmentStatus) bool {
	switch status {
	case EnvironmentQueued, EnvironmentPreparing, EnvironmentReady, EnvironmentClaiming, EnvironmentClaimed, EnvironmentReleasing, EnvironmentFailed:
		return true
	default:
		return false
	}
}
