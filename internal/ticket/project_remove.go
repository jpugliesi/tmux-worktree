package ticket

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
	"github.com/jpugliesi/tmux-worktree/internal/store"
)

// ProjectRemovalPlan lists the files a Project remove would delete.
type ProjectRemovalPlan struct {
	Project  domain.Project          `json:"project"`
	Tickets  []string                `json:"tickets"`
	Actions  []ProjectRemovalAction  `json:"actions"`
	Blockers []ProjectRemovalBlocker `json:"blockers,omitempty"`
}

// ProjectRemovalAction is one delete the plan would run.
type ProjectRemovalAction struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}

// ProjectRemovalBlocker is one condition that prevents Project remove.
type ProjectRemovalBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// PlanProjectRemoval reports what remove would delete. It writes nothing.
func (s *Service) PlanProjectRemoval(name string) (ProjectRemovalPlan, error) {
	return s.planProjectRemoval(name)
}

// RemoveProject deletes one Project directory and its Ticket files so the
// name can be created again.
func (s *Service) RemoveProject(name string, dryRun bool) (ProjectRemovalPlan, error) {
	return syncWrite(s, syncRequired, dryRun, func() string {
		return fmt.Sprintf("twt: remove project %s", name)
	}, func() (ProjectRemovalPlan, error) {
		return s.removeProjectOnce(name, dryRun)
	})
}

func (s *Service) removeProjectOnce(name string, dryRun bool) (ProjectRemovalPlan, error) {
	plan, err := s.planProjectRemoval(name)
	if err != nil {
		return plan, err
	}
	if dryRun {
		return plan, nil
	}
	home, err := s.home()
	if err != nil {
		return plan, err
	}
	lock, err := store.AcquireNamedLock(s.options.StateDir, "project", name)
	if err != nil {
		return plan, err
	}
	defer lock.Release()
	projectDirectory := filepath.Join(home, name)
	closedDirectory := filepath.Join(home, closedDirectoryName, name)
	if err := os.RemoveAll(projectDirectory); err != nil {
		return plan, fmt.Errorf("remove Project %q: %w", name, err)
	}
	if err := os.RemoveAll(closedDirectory); err != nil {
		return plan, fmt.Errorf("remove closed Tickets for Project %q: %w", name, err)
	}
	return plan, nil
}

func (s *Service) planProjectRemoval(name string) (ProjectRemovalPlan, error) {
	home, err := s.home()
	if err != nil {
		return ProjectRemovalPlan{}, err
	}
	project, err := s.Project(name)
	if err != nil {
		return ProjectRemovalPlan{}, err
	}
	idx, err := buildIndex(home)
	if err != nil {
		return ProjectRemovalPlan{}, err
	}
	tickets := make([]string, 0)
	for _, ticket := range idx.tickets {
		if ticket.Project == name {
			if parseErr := idx.skipped[ticket.Slug]; parseErr != nil {
				return ProjectRemovalPlan{}, parseErr
			}
			tickets = append(tickets, ticket.Slug)
		}
	}
	sort.Strings(tickets)
	plan := ProjectRemovalPlan{Project: project, Tickets: tickets, Actions: []ProjectRemovalAction{}}
	projectDirectory := filepath.Join(home, name)
	plan.Actions = append(plan.Actions, ProjectRemovalAction{Kind: "delete", Target: projectDirectory})
	closedDirectory := filepath.Join(home, closedDirectoryName, name)
	if exists, existsErr := regularTicketDirectory(closedDirectory, "closed Project"); existsErr != nil {
		return ProjectRemovalPlan{}, existsErr
	} else if exists {
		plan.Actions = append(plan.Actions, ProjectRemovalAction{Kind: "delete", Target: closedDirectory})
	}
	return plan, nil
}
