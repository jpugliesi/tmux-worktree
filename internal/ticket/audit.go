package ticket

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

const (
	issueClosedRootConflict = "closed_root_conflict"
	issueDestinationExists  = "destination_exists"
	issueDuplicateSlug      = "duplicate_slug"
	issueInvalidLocation    = "invalid_location"
	issueInvalidStatus      = "invalid_status"
	issueLocationMismatch   = "location_mismatch"
	issueMissingProject     = "missing_project"
	issueParseFailure       = "parse_failure"
)

// TicketIssue is one deterministic doctor finding. Repairable findings become
// moves. A blocker prevents repair from applying any move.
type TicketIssue struct {
	Code        string `json:"code"`
	Slug        string `json:"slug,omitempty"`
	Path        string `json:"path"`
	Destination string `json:"destination,omitempty"`
	Message     string `json:"message"`
	Repairable  bool   `json:"repairable"`
	Blocker     string `json:"blocker,omitempty"`
}

// TicketDoctorReport describes the complete Tickets home scan.
type TicketDoctorReport struct {
	Healthy     bool          `json:"healthy"`
	TicketCount int           `json:"ticketCount"`
	Issues      []TicketIssue `json:"issues"`
	// Sync reports the git sync state when ticketsSync is enabled. Its
	// findings never block repair: repair blockers come from Issues only.
	Sync *SyncDoctorInfo `json:"sync,omitempty"`
}

// SyncDoctorIssue is one local-only git sync finding.
type SyncDoctorIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SyncDoctorInfo is the additive git sync block of the doctor report. Every
// check is local; doctor never reaches the remote.
type SyncDoctorInfo struct {
	Remote          string            `json:"remote"`
	Branch          string            `json:"branch,omitempty"`
	Dirty           bool              `json:"dirty"`
	UnpushedCommits int               `json:"unpushedCommits"`
	Issues          []SyncDoctorIssue `json:"issues"`
}

// TicketMove is one byte-preserving path repair.
type TicketMove struct {
	Slug        string `json:"slug"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

// TicketRepairPlan is the shared result of doctor analysis for repair.
type TicketRepairPlan struct {
	Moves    []TicketMove  `json:"moves"`
	Blockers []TicketIssue `json:"blockers"`
}

// TicketRepairResult reports the plan and how many planned moves completed.
type TicketRepairResult struct {
	Applied    bool             `json:"applied"`
	MovedCount int              `json:"movedCount"`
	Plan       TicketRepairPlan `json:"plan"`
}

type auditedTicket struct {
	ticket   domain.Ticket
	location ticketLocation
}

// Doctor scans ticket files without changing the Tickets home.
func (s *Service) Doctor() (TicketDoctorReport, error) {
	home, err := s.home()
	if err != nil {
		return TicketDoctorReport{}, err
	}
	report, err := auditTickets(home)
	if err != nil {
		return report, err
	}
	report.Sync = s.syncDoctor(home)
	return report, nil
}

// Repair moves every repairable location mismatch. It applies no move when
// the audit has a blocker. A dry run returns the same plan without writes.
func (s *Service) Repair(dryRun bool) (TicketRepairResult, error) {
	return syncWrite(s, syncBestEffort, dryRun, func() string {
		return "twt: repair"
	}, func() (TicketRepairResult, error) {
		return s.repairOnce(dryRun)
	})
}

func (s *Service) repairOnce(dryRun bool) (TicketRepairResult, error) {
	report, err := s.Doctor()
	if err != nil {
		return TicketRepairResult{}, err
	}
	result := TicketRepairResult{Plan: repairPlan(report)}
	if len(result.Plan.Blockers) > 0 {
		return result, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "ticket repair has %d blocker(s)", len(result.Plan.Blockers)),
			"Run 'twt tickets doctor' and correct every blocker before repair.")
	}
	if dryRun {
		return result, nil
	}
	for _, move := range result.Plan.Moves {
		moved, moveErr := s.repairMove(move)
		if moveErr != nil {
			return result, moveErr
		}
		if moved {
			result.MovedCount++
		}
	}
	result.Applied = true
	return result, nil
}

func auditTickets(home string) (TicketDoctorReport, error) {
	report := TicketDoctorReport{Issues: []TicketIssue{}}
	_, closedErr := closedRootExists(home)
	closedConflict := closedErr != nil
	if closedErr != nil {
		report.Issues = append(report.Issues, TicketIssue{
			Code: issueClosedRootConflict, Path: filepath.Join(home, closedDirectoryName),
			Message: closedErr.Error(), Blocker: "the closed Tickets directory is not owned by twt",
		})
	}
	records := map[string]*auditedTicket{}
	slugPaths := map[string][]string{}
	err := filepath.WalkDir(filepath.Clean(home), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == filepath.Clean(home) && errors.Is(walkErr, os.ErrNotExist) {
				return homeMissing(home)
			}
			return walkErr
		}
		relative, relErr := filepath.Rel(home, path)
		if relErr != nil {
			return relErr
		}
		if entry.IsDir() {
			if relative == "." {
				return nil
			}
			if strings.HasPrefix(entry.Name(), ".") || relative == "templates" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 && unsafeTicketSymlink(relative, entry.Name()) {
			slug := ""
			if strings.HasSuffix(entry.Name(), ".md") {
				slug = strings.TrimSuffix(entry.Name(), ".md")
			}
			report.Issues = append(report.Issues, TicketIssue{
				Code: issueInvalidLocation, Slug: slug, Path: path,
				Message: "a Ticket or Project path is a symbolic link",
				Blocker: "ticket storage paths must not be symbolic links",
			})
			return nil
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".md") || reservedProjectFile(entry.Name()) {
			return nil
		}
		slug := strings.TrimSuffix(entry.Name(), ".md")
		location, locationErr := classifyTicketPath(home, path)
		if locationErr != nil {
			report.Issues = append(report.Issues, TicketIssue{
				Code: issueInvalidLocation, Slug: slug, Path: path, Message: locationErr.Error(),
				Blocker: "the Ticket path is not supported",
			})
			return nil
		}
		slugPaths[slug] = append(slugPaths[slug], path)
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		file, parseErr := ParseTicketFile(path, raw)
		if parseErr != nil {
			report.Issues = append(report.Issues, TicketIssue{
				Code: issueParseFailure, Slug: slug, Path: path, Message: parseErr.Error(),
				Blocker: "the Ticket file cannot be parsed",
			})
			return nil
		}
		ticket, decodeErr := decodeTicket(file, slug)
		if decodeErr != nil {
			report.Issues = append(report.Issues, TicketIssue{
				Code: issueParseFailure, Slug: slug, Path: path, Message: decodeErr.Error(),
				Blocker: "the Ticket frontmatter cannot be decoded",
			})
			return nil
		}
		ticket.Project = location.Project
		report.TicketCount++
		if !domain.ValidTicketStatus(ticket.Status) {
			report.Issues = append(report.Issues, TicketIssue{
				Code: issueInvalidStatus, Slug: slug, Path: path,
				Message: fmt.Sprintf("ticket %q has unrecognized status %q", slug, ticket.Status),
				Blocker: "the Ticket status is not valid",
			})
			return nil
		}
		records[path] = &auditedTicket{ticket: ticket, location: location}
		return nil
	})
	if err != nil {
		return TicketDoctorReport{}, err
	}

	duplicateSlugs := map[string]bool{}
	for slug, paths := range slugPaths {
		if len(paths) < 2 {
			continue
		}
		duplicateSlugs[slug] = true
		sort.Strings(paths)
		for _, path := range paths {
			report.Issues = append(report.Issues, TicketIssue{
				Code: issueDuplicateSlug, Slug: slug, Path: path,
				Message: fmt.Sprintf("ticket slug %q exists at %d paths", slug, len(paths)),
				Blocker: "duplicate Ticket slugs require a manual choice",
			})
		}
	}

	paths := make([]string, 0, len(records))
	for path := range records {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		record := records[path]
		if duplicateSlugs[record.ticket.Slug] || closedConflict && record.location.Closed {
			continue
		}
		shouldBeClosed := closedStatus(record.ticket.Status)
		if shouldBeClosed == record.location.Closed {
			continue
		}
		destination := canonicalTicketPath(home, record.ticket.Status, record.location.Project, record.ticket.Slug)
		if !shouldBeClosed && record.location.Project != "" {
			info, statErr := os.Stat(filepath.Join(home, record.location.Project))
			if statErr != nil || !info.IsDir() {
				report.Issues = append(report.Issues, TicketIssue{
					Code: issueMissingProject, Slug: record.ticket.Slug, Path: path, Destination: destination,
					Message: fmt.Sprintf("Project %q does not exist for reopened ticket %q", record.location.Project, record.ticket.Slug),
					Blocker: "the active Project directory is missing",
				})
				continue
			}
		}
		if _, statErr := os.Lstat(destination); statErr == nil {
			report.Issues = append(report.Issues, TicketIssue{
				Code: issueDestinationExists, Slug: record.ticket.Slug, Path: path, Destination: destination,
				Message: fmt.Sprintf("ticket destination %q already exists", destination),
				Blocker: "repair never replaces an existing destination",
			})
			continue
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return TicketDoctorReport{}, statErr
		}
		report.Issues = append(report.Issues, TicketIssue{
			Code: issueLocationMismatch, Slug: record.ticket.Slug, Path: path, Destination: destination,
			Message:    fmt.Sprintf("ticket %q is not in its correct location", record.ticket.Slug),
			Repairable: true,
		})
	}
	sort.Slice(report.Issues, func(i, j int) bool {
		if report.Issues[i].Path != report.Issues[j].Path {
			return report.Issues[i].Path < report.Issues[j].Path
		}
		return report.Issues[i].Code < report.Issues[j].Code
	})
	report.Healthy = len(report.Issues) == 0
	return report, nil
}

func unsafeTicketSymlink(relative, name string) bool {
	if strings.HasPrefix(name, ".") || relative == "templates" {
		return false
	}
	if strings.HasSuffix(name, ".md") {
		return true
	}
	parts := strings.Split(relative, string(filepath.Separator))
	return len(parts) == 1 || len(parts) == 2 && parts[0] == closedDirectoryName
}

func repairPlan(report TicketDoctorReport) TicketRepairPlan {
	plan := TicketRepairPlan{Moves: []TicketMove{}, Blockers: []TicketIssue{}}
	for _, issue := range report.Issues {
		if issue.Repairable && issue.Code == issueLocationMismatch {
			plan.Moves = append(plan.Moves, TicketMove{Slug: issue.Slug, Source: issue.Path, Destination: issue.Destination})
			continue
		}
		plan.Blockers = append(plan.Blockers, issue)
	}
	return plan
}

func (s *Service) repairMove(planned TicketMove) (bool, error) {
	lock, err := store.AcquireNamedLock(s.options.StateDir, "ticket", planned.Slug)
	if err != nil {
		return false, err
	}
	defer lock.Release()
	raw, err := os.ReadFile(planned.Source)
	if errors.Is(err, os.ErrNotExist) {
		return false, clierr.New(clierr.PreconditionFailed, "ticket %q changed after the repair plan", planned.Slug)
	}
	if err != nil {
		return false, err
	}
	file, err := ParseTicketFile(planned.Source, raw)
	if err != nil {
		return false, err
	}
	ticket, err := decodeTicket(file, planned.Slug)
	if err != nil {
		return false, err
	}
	if !domain.ValidTicketStatus(ticket.Status) {
		return false, clierr.New(clierr.PreconditionFailed, "ticket %q has unrecognized status %q", planned.Slug, ticket.Status)
	}
	location, err := classifyTicketPath(s.options.Home, planned.Source)
	if err != nil {
		return false, clierr.Wrap(clierr.PreconditionFailed, err)
	}
	destination := canonicalTicketPath(s.options.Home, ticket.Status, location.Project, planned.Slug)
	if destination == planned.Source {
		return false, nil
	}
	if destination != planned.Destination {
		return false, clierr.New(clierr.PreconditionFailed, "ticket %q changed after the repair plan", planned.Slug)
	}
	if err := ensureTicketDirectory(s.options.Home, ticket.Status, location.Project, false); err != nil {
		return false, err
	}
	info, err := os.Stat(planned.Source)
	if err != nil {
		return false, err
	}
	if err := moveTicketFile(planned.Source, destination, raw, info.Mode().Perm()); err != nil {
		return false, err
	}
	return true, nil
}
