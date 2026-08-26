package ticket

import "github.com/jpugliesi/tmux-worktree/internal/domain"

// Store is the ticket backend contract. Every consumer of tickets — the CLI,
// the dispatch backends, and future clients — depends on this interface, not
// on a concrete implementation. The markdown-plus-git Service is the
// reference implementation.
//
// The contract behind the signatures, enforced by the conformance suite:
//
//   - Claim, ClaimReady, CompleteWork, Unclaim, and Close are atomic
//     compare-and-set operations on the claim. Two concurrent clients cannot
//     both win a claim when Capabilities().StrongClaims is true.
//   - A retry of a mutation that already succeeded is a no-op success, never
//     an error (same-claimant Claim, already-recorded CompleteWork).
//   - Readiness = status ready-for-agent, no claim, and every blocker done
//     or wontfix. Queue computes it from one consistent snapshot.
//   - Every mutation supports dryRun: full validation, no state change.
type Store interface {
	// Reads.
	Show(ref string) (ShowResult, error)
	Resolve(ref string) (domain.Ticket, error)
	List(filter ListFilter) ([]domain.Ticket, error)
	Queue(projectName string, limit int) (QueueResult, error)
	Slugs() ([]string, error)
	Projects() ([]domain.Project, error)
	Project(name string) (domain.Project, error)
	HomePath() (string, error)

	// Writes.
	Init(dryRun bool) (InitResult, error)
	Create(req CreateRequest, dryRun bool) (CreateResult, error)
	CreateProject(name string, dryRun bool) (domain.Project, error)
	CreateProjectWithTemplate(name, templateName string, dryRun bool) (domain.Project, error)
	SetProjectTemplate(name, templateName string, dryRun bool) (domain.Project, error)
	Set(ref string, req SetRequest, dryRun bool) (domain.Ticket, error)
	Edit(ref, body string, dryRun bool) (domain.Ticket, error)
	Comment(ref, text string, dryRun bool) (domain.Ticket, error)
	SetWorkspace(ref, workspaceID string, dryRun bool) (domain.Ticket, error)

	// Claim lifecycle (compare-and-set).
	Claim(ref, claimant string, dryRun bool) (domain.Ticket, error)
	ClaimReady(ref, claimant string, dryRun bool) (domain.Ticket, error)
	CompleteClaim(ref, claimant string, status domain.TicketStatus, dryRun bool) (domain.Ticket, error)
	CompleteWork(ref, claimant string, status domain.TicketStatus, pullRequests []string, dryRun bool) (domain.Ticket, error)
	Unclaim(ref, claimant string, dryRun bool) (domain.Ticket, error)
	Close(ref, claimant string, dryRun bool) (domain.Ticket, error)

	// Maintenance.
	Doctor() (TicketDoctorReport, error)
	Repair(dryRun bool) (TicketRepairResult, error)
	// Sync reconciles the store with its shared backend (for the markdown
	// store: the git remote). Backends without a remote return a no-op
	// status.
	Sync(dryRun bool) (SyncStatus, error)

	// Capabilities reports what this backend can honestly promise.
	Capabilities() Capabilities
}

// Capabilities describes backend guarantees that callers must not assume.
type Capabilities struct {
	// StrongClaims: concurrent clients racing a claim get exactly one
	// winner (compare-and-set). A coordinator must not run unattended
	// dispatch against a backend without strong claims.
	StrongClaims bool `json:"strongClaims"`
	// Offline: reads and best-effort writes work without a network.
	Offline bool `json:"offline"`
	// BodySections: tickets carry free-form bodies with named sections
	// (Plan, Questions, Comments) that section-aware verbs can address.
	BodySections bool `json:"bodySections"`
}

// Compile-time check: the markdown-plus-git Service is a Store.
var _ Store = (*Service)(nil)

// Capabilities reports the markdown store's guarantees. Claims are strong on
// one machine through file locks, and across machines through the git
// push-as-CAS handshake when ticketsSync is enabled.
func (s *Service) Capabilities() Capabilities {
	return Capabilities{StrongClaims: true, Offline: true, BodySections: true}
}
