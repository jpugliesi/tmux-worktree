package localdispatch

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// AgentObservation is one batched liveness read of a session's agent.
type AgentObservation struct {
	// Complete is false when the observer could not look (tmux unreachable,
	// store error). An incomplete observation never means "stopped".
	Complete bool
	// Found is true when the agent record exists.
	Found bool
	// Live is true when the agent process runs in its pane.
	Live bool
	// Diagnostics carries the observer's own findings.
	Diagnostics []string
}

// WorkspaceObserver resolves one Workspace and its agent liveness. The CLI
// adapts the real workspace and agent services; tests fake it.
type WorkspaceObserver interface {
	// Observe returns the agent observation. workspaceGone reports a missing
	// or archived Workspace; that is a finding, not an error.
	Observe(workspaceID, agentSessionID, agentLabel string) (observation AgentObservation, workspaceGone bool, err error)
}

// Stable local sync diagnostic codes. These are states, not errors.
const (
	SyncFindingStuck             = "stuck"
	SyncFindingObserveIncomplete = "observe_incomplete"
	SyncFindingWorkspaceMissing  = "workspace_missing"
	SyncFindingTicketMissing     = "ticket_missing"
	SyncFindingClaimReleased     = "claim_released"
	SyncFindingSyncFailed        = "sync_failed"
)

// Diagnostic is one per-session sync finding.
type Diagnostic struct {
	SessionID string `json:"sessionId"`
	Ticket    string `json:"ticket"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Hint      string `json:"hint,omitempty"`
}

// Capacity is the local dispatch budget of one Project.
type Capacity struct {
	Maximum   int  `json:"maximum"`
	Active    int  `json:"active"`
	Available int  `json:"available"`
	Known     bool `json:"known"`
}

// SyncResult reports one local reconciliation round.
type SyncResult struct {
	Project     string                        `json:"project"`
	Capacity    Capacity                      `json:"capacity"`
	Sessions    []domain.LocalDispatchSession `json:"sessions"`
	Diagnostics []Diagnostic                  `json:"diagnostics,omitempty"`
}

// finding is a reconciliation state that becomes a diagnostic, not an error.
type finding struct {
	code    string
	message string
	hint    string
}

func (f *finding) Error() string { return f.message }

// Sync reconciles the local dispatch sessions of one Project against agent
// liveness and ticket state. One session's failure becomes a diagnostic and
// the loop continues. A stuck session is reported, never auto-released.
func (s *Service) Sync(project string, dryRun bool) (SyncResult, error) {
	if s.options.Tickets == nil {
		return SyncResult{}, clierr.New(clierr.PreconditionFailed, "local dispatch sync is not configured")
	}
	if s.options.Observer == nil {
		return SyncResult{}, clierr.New(clierr.PreconditionFailed, "local dispatch sync is not configured with an observer")
	}
	maximum, err := s.projectMaxConcurrency(project)
	if err != nil {
		return SyncResult{}, err
	}
	all, err := s.sessions.List()
	if err != nil {
		return SyncResult{}, err
	}
	result := SyncResult{Project: project, Sessions: []domain.LocalDispatchSession{}}
	for _, session := range all {
		if session.Project != project || (!session.Active() && session.TicketTransitioned) {
			continue
		}
		updated, updateErr := s.updateSavedSession(session.ID, func(current *domain.LocalDispatchSession) error {
			if !current.Active() {
				if current.TicketTransitioned {
					return nil
				}
				return s.reconcileTerminal(current, dryRun)
			}
			return s.reconcileActive(current, dryRun)
		})
		if updated.ID != "" {
			session = updated
		}
		if updateErr != nil {
			result.Diagnostics = append(result.Diagnostics, localDiagnostic(session, updateErr))
		}
		result.Sessions = append(result.Sessions, session)
	}
	sort.Slice(result.Sessions, func(i, j int) bool { return result.Sessions[i].ID < result.Sessions[j].ID })
	sort.Slice(result.Diagnostics, func(i, j int) bool { return result.Diagnostics[i].SessionID < result.Diagnostics[j].SessionID })
	setLocalCapacity(&result, maximum)
	return result, nil
}

// reconcileActive joins one active session with its ticket claim and its
// agent liveness.
func (s *Service) reconcileActive(session *domain.LocalDispatchSession, dryRun bool) error {
	ticket, err := s.options.Tickets.Resolve(session.TicketSlug)
	if err != nil {
		if clierr.CodeOf(err) == clierr.NotFound {
			return &finding{code: SyncFindingTicketMissing,
				message: fmt.Sprintf("ticket %q of Session %q no longer exists", session.TicketSlug, session.ID),
				hint:    "Abandon the Session with 'twt tickets abandon " + session.ID[:8] + " --force'."}
		}
		return err
	}
	now := s.options.Now().UTC()
	switch {
	case ticket.ClaimedBy == session.Claimant:
		observation, workspaceGone, err := s.options.Observer.Observe(session.WorkspaceID, session.AgentSessionID, session.AgentLabel)
		if err != nil {
			return &finding{code: SyncFindingObserveIncomplete,
				message: fmt.Sprintf("twt could not observe Session %q: %v", session.ID, err)}
		}
		if !observation.Complete {
			return &finding{code: SyncFindingObserveIncomplete,
				message: fmt.Sprintf("the observation of Session %q is incomplete: %s", session.ID, strings.Join(observation.Diagnostics, "; "))}
		}
		if workspaceGone {
			return &finding{code: SyncFindingWorkspaceMissing,
				message: fmt.Sprintf("the Workspace %q of Session %q is missing or archived while the Ticket stays claimed", session.WorkspaceID, session.ID),
				hint:    "Release the Ticket with 'twt tickets abandon " + session.ID[:8] + " --force'."}
		}
		if observation.Found && observation.Live {
			if dryRun {
				return nil
			}
			session.Status = domain.LocalDispatchRunning
			session.UpdatedAt = now
			return s.sessions.Save(*session)
		}
		return &finding{code: SyncFindingStuck,
			message: fmt.Sprintf("the agent of Session %q stopped while the Ticket stays claimed", session.ID),
			hint:    "Resume it with 'twt agents resume', or release the Ticket with 'twt tickets abandon " + session.ID[:8] + " --force'."}
	case ticket.ClaimedBy == "":
		// The worker released the claim itself; never call CompleteClaim
		// here, the claim is already gone.
		if dryRun {
			return nil
		}
		if ticket.Status == domain.TicketReadyForHuman || ticket.Status == domain.TicketDone {
			session.Status = domain.LocalDispatchFinished
		} else {
			session.Status = domain.LocalDispatchFailed
		}
		session.CompletedAt = &now
		session.TicketTransitioned = true
		session.UpdatedAt = now
		return s.sessions.Save(*session)
	default:
		// Someone else claimed the ticket: user state, leave it alone.
		if dryRun {
			return nil
		}
		session.Status = domain.LocalDispatchCancelled
		session.Error = &domain.CursorCloudError{Kind: "superseded",
			Message: fmt.Sprintf("Ticket %q was re-claimed by %q.", session.TicketSlug, ticket.ClaimedBy)}
		session.CompletedAt = &now
		session.TicketTransitioned = true
		session.UpdatedAt = now
		return s.sessions.Save(*session)
	}
}

// reconcileTerminal finishes the ticket transition of a terminal session
// after a crash between the status write and the transition.
func (s *Service) reconcileTerminal(session *domain.LocalDispatchSession, dryRun bool) error {
	if dryRun {
		return nil
	}
	ticket, err := s.options.Tickets.Resolve(session.TicketSlug)
	if err != nil {
		if clierr.CodeOf(err) != clierr.NotFound {
			return err
		}
		return s.markTransitioned(session)
	}
	if ticket.ClaimedBy != session.Claimant {
		if err := s.markTransitioned(session); err != nil {
			return err
		}
		return &finding{code: SyncFindingClaimReleased,
			message: fmt.Sprintf("the claim of Session %q was already released or taken over", session.ID)}
	}
	target := domain.TicketReadyForAgent
	if session.Status == domain.LocalDispatchFinished {
		target = domain.TicketReadyForHuman
	}
	if _, err := s.options.Tickets.CompleteClaim(session.TicketSlug, session.Claimant, target, false); err != nil {
		return err
	}
	return s.markTransitioned(session)
}

func (s *Service) markTransitioned(session *domain.LocalDispatchSession) error {
	session.TicketTransitioned = true
	session.UpdatedAt = s.options.Now().UTC()
	return s.sessions.Save(*session)
}

// projectMaxConcurrency reads the local dispatch budget of one Project. A
// Template without a local_dispatch block uses the default budget.
func (s *Service) projectMaxConcurrency(projectName string) (int, error) {
	project, err := s.options.Tickets.Project(projectName)
	if err != nil {
		return 0, err
	}
	if project.TemplateName == "" {
		return 0, clierr.New(clierr.PreconditionFailed, "Project %q has no Workspace Template", projectName)
	}
	template, err := s.options.Templates.Load(project.TemplateName)
	if err != nil {
		return 0, err
	}
	return template.LocalDispatch.EffectiveMaxConcurrency(), nil
}

func setLocalCapacity(result *SyncResult, maximum int) {
	active := 0
	for _, session := range result.Sessions {
		if session.Active() {
			active++
		}
	}
	available := maximum - active
	if available < 0 {
		available = 0
	}
	known := len(result.Diagnostics) == 0
	if !known {
		available = 0
	}
	result.Capacity = Capacity{Maximum: maximum, Active: active, Available: available, Known: known}
}

func localDiagnostic(session domain.LocalDispatchSession, err error) Diagnostic {
	var found *finding
	if errors.As(err, &found) {
		return Diagnostic{SessionID: session.ID, Ticket: session.TicketSlug,
			Code: found.code, Message: found.message, Hint: found.hint}
	}
	code := string(clierr.CodeOf(err))
	if code == string(clierr.Internal) {
		code = SyncFindingSyncFailed
	}
	return Diagnostic{SessionID: session.ID, Ticket: session.TicketSlug,
		Code: code, Message: err.Error(), Hint: clierr.HintOf(err)}
}

// updateSavedSession applies one change under the session lock with a fresh
// read from disk, so a concurrent abandon cannot be overwritten.
func (s *Service) updateSavedSession(
	id string,
	update func(*domain.LocalDispatchSession) error,
) (session domain.LocalDispatchSession, returnErr error) {
	lock, err := store.AcquireNamedLock(s.options.StateDir, "local-dispatch-session", id)
	if err != nil {
		return domain.LocalDispatchSession{}, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, lock.Release())
	}()
	session, err = s.sessions.Find(id)
	if err != nil {
		return session, err
	}
	if err := update(&session); err != nil {
		return session, err
	}
	return session, nil
}
