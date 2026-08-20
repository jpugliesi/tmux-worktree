package domain

import (
	"fmt"
	"time"
)

const (
	PreparedEnvironmentVersion = 1
	PreparationFormatVersion   = 1
)

type PreparedEnvironmentStatus string

const (
	EnvironmentQueued    PreparedEnvironmentStatus = "queued"
	EnvironmentPreparing PreparedEnvironmentStatus = "preparing"
	EnvironmentReady     PreparedEnvironmentStatus = "ready"
	EnvironmentClaiming  PreparedEnvironmentStatus = "claiming"
	EnvironmentClaimed   PreparedEnvironmentStatus = "claimed"
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
	ClaimReservation *EnvironmentClaim         `json:"claimReservation,omitempty"`
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

// EnvironmentClaim records the complete Project reserved for one claim. Code
// that resumes an interrupted claim must use this value without changing it.
type EnvironmentClaim struct {
	Project    Project   `json:"project"`
	ReservedAt time.Time `json:"reservedAt"`
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
		return fmt.Errorf("Prepared Environment %q has no Project Template name", e.ID)
	}
	if e.TemplateDigest == "" {
		return fmt.Errorf("Prepared Environment %q has no Project Template digest", e.ID)
	}
	if e.TemplateSnapshot.Name != e.TemplateName {
		return fmt.Errorf("Prepared Environment %q has Project Template name %q but its snapshot has name %q", e.ID, e.TemplateName, e.TemplateSnapshot.Name)
	}
	if err := e.TemplateSnapshot.Validate(); err != nil {
		return fmt.Errorf("Prepared Environment %q has an invalid Project Template: %w", e.ID, err)
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
	if e.ClaimReservation != nil {
		if e.ClaimReservation.Project.ID == "" || e.ClaimReservation.Project.Version != ProjectVersion {
			return fmt.Errorf("Prepared Environment %q has an invalid Project claim reservation", e.ID)
		}
		if e.ClaimReservation.Project.EnvironmentID != e.ID {
			return fmt.Errorf("Prepared Environment %q has a claim reservation for environment %q", e.ID, e.ClaimReservation.Project.EnvironmentID)
		}
		if e.ClaimReservation.ReservedAt.IsZero() {
			return fmt.Errorf("Prepared Environment %q has no claim reservation time", e.ID)
		}
	}
	switch e.Status {
	case EnvironmentClaiming, EnvironmentClaimed:
		if e.ClaimReservation == nil {
			return fmt.Errorf("Prepared Environment %q has status %q without a Project claim reservation", e.ID, e.Status)
		}
	case EnvironmentQueued, EnvironmentPreparing, EnvironmentReady, EnvironmentFailed:
		if e.ClaimReservation != nil {
			return fmt.Errorf("Prepared Environment %q has status %q with a Project claim reservation", e.ID, e.Status)
		}
	}
	if e.Status == EnvironmentReady || e.Status == EnvironmentClaiming || e.Status == EnvironmentClaimed {
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
	case EnvironmentQueued, EnvironmentPreparing, EnvironmentReady, EnvironmentClaiming, EnvironmentClaimed, EnvironmentFailed:
		return true
	default:
		return false
	}
}
