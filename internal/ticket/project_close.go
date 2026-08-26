package ticket

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// ProjectCloseResult reports the Project and the open Tickets that close
// changed to wontfix.
type ProjectCloseResult struct {
	Project        domain.Project `json:"project"`
	WontfixTickets []string       `json:"wontfixTickets"`
}

// CloseProject marks one Project closed. Open Tickets require force. Close
// changes each open Ticket to wontfix and clears its active work fields.
func (s *Service) CloseProject(name string, force, dryRun bool) (ProjectCloseResult, error) {
	return syncWrite(s, syncRequired, dryRun, func() string {
		return fmt.Sprintf("twt: close project %s", name)
	}, func() (ProjectCloseResult, error) {
		return s.closeProjectOnce(name, force, dryRun)
	})
}

func (s *Service) closeProjectOnce(name string, force, dryRun bool) (ProjectCloseResult, error) {
	home, err := s.home()
	if err != nil {
		return ProjectCloseResult{}, err
	}
	lock, err := store.AcquireNamedLock(s.options.StateDir, "project", name)
	if err != nil {
		return ProjectCloseResult{}, err
	}
	defer lock.Release()
	project, err := s.Project(name)
	if err != nil {
		return ProjectCloseResult{}, err
	}
	if !project.HasIndex {
		return ProjectCloseResult{}, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Project %q has no index.md", name),
			"Run 'twt projects create %s' to add the Project index.", name)
	}

	idx, err := buildIndex(home)
	if err != nil {
		return ProjectCloseResult{}, err
	}
	projectDirectory := filepath.Join(home, name)
	for slug, paths := range idx.bySlug {
		for _, path := range paths {
			if filepath.Dir(path) == projectDirectory {
				if parseErr := idx.skipped[slug]; parseErr != nil {
					return ProjectCloseResult{}, parseErr
				}
			}
		}
	}
	open := make([]string, 0)
	for _, ticket := range idx.tickets {
		if ticket.Project == name && !closedStatus(ticket.Status) {
			open = append(open, ticket.Slug)
		}
	}
	sort.Strings(open)
	if len(open) > 0 && !force {
		return ProjectCloseResult{}, clierr.WithHint(
			clierr.New(clierr.PreconditionFailed, "Project %q has %d open Tickets", name, len(open)),
			"Pass --force to set the open Tickets to wontfix and close the Project.")
	}

	result := ProjectCloseResult{Project: project, WontfixTickets: open}
	result.Project.Closed = true
	for _, slug := range open {
		if _, err := s.mutateOnce(slug, true, true, closeProjectTicket); err != nil {
			return ProjectCloseResult{}, err
		}
	}
	if dryRun {
		return result, nil
	}

	indexPath := filepath.Join(projectDirectory, "index.md")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		return ProjectCloseResult{}, fmt.Errorf("read Project %q index: %w", name, err)
	}
	file, err := ParseTicketFile(indexPath, raw)
	if err != nil {
		return ProjectCloseResult{}, err
	}
	setMapBool(file.ensureMapping(), "twt_closed", true)
	content, err := file.Render()
	if err != nil {
		return ProjectCloseResult{}, err
	}
	if err := store.WriteFileAtomic(indexPath, content, 0o644, "Project index"); err != nil {
		return ProjectCloseResult{}, err
	}
	for _, slug := range open {
		if _, err := s.mutateOnce(slug, false, true, closeProjectTicket); err != nil {
			return ProjectCloseResult{}, err
		}
	}
	result.Project, err = s.projectInfo(home, name)
	if err != nil {
		return ProjectCloseResult{}, err
	}
	return result, nil
}

func closeProjectTicket(m *mutation) error {
	setMapString(m.mapping, "status", string(domain.TicketWontfix))
	setMapNull(m.mapping, "claimed_by")
	setMapNull(m.mapping, "claimed_at")
	setMapNull(m.mapping, "twt_workspace_id")
	m.relocate = true
	return nil
}

func closedProject(name string) error {
	return clierr.WithHint(
		clierr.New(clierr.PreconditionFailed, "Project %q is closed", name),
		"Select an active Project.")
}

func (s *Service) activeProject(name string) (domain.Project, error) {
	project, err := s.Project(name)
	if err != nil {
		return domain.Project{}, err
	}
	if project.Closed {
		return domain.Project{}, closedProject(name)
	}
	return project, nil
}
