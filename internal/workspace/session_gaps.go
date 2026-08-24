package workspace

import (
	"fmt"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// Session gap codes. Doctor and open --all-active use these to describe
// tmux drift after a reboot or a tmux-resurrect restore.
const (
	SessionGapMissing      = "missing_session"
	SessionGapUnownedName  = "unowned_name_match"
	SessionGapArchivedLive = "archived_owned"
)

// SessionGap is one Workspace whose tmux session does not match twt state.
type SessionGap struct {
	Workspace domain.Workspace
	Code      string
	Message   string
}

// SessionGaps lists active Workspaces with no owned session, unowned sessions
// whose name matches a Workspace, and archived Workspaces that still own a
// session.
func (s *Service) SessionGaps() ([]SessionGap, error) {
	workspaces, err := s.store.List()
	if err != nil {
		return nil, err
	}
	rows, err := s.workspaceSessionRows(true)
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(err.Error(), "no sessions") || strings.Contains(err.Error(), "error connecting to") {
			rows = nil
		} else {
			return nil, err
		}
	}
	ownedBy := make(map[string]tmuxSessionRow, len(rows))
	byName := make(map[string]tmuxSessionRow, len(rows))
	for _, row := range rows {
		if row.ownerID != "" {
			ownedBy[row.ownerID] = row
		}
		if row.name != "" {
			byName[row.name] = row
		}
	}
	gaps := make([]SessionGap, 0)
	for _, workspace := range workspaces {
		name := sessionName(workspace.TemplateName, workspace.Name)
		_, owned := ownedBy[workspace.ID]
		named, hasName := byName[name]
		switch workspace.Status {
		case domain.WorkspaceActive:
			if owned {
				continue
			}
			if hasName && named.ownerID == "" {
				gaps = append(gaps, SessionGap{
					Workspace: workspace,
					Code:      SessionGapUnownedName,
					Message:   fmt.Sprintf("active Workspace %q has unowned tmux session %q", workspace.Name, name),
				})
				continue
			}
			gaps = append(gaps, SessionGap{
				Workspace: workspace,
				Code:      SessionGapMissing,
				Message:   fmt.Sprintf("active Workspace %q has no owned tmux session", workspace.Name),
			})
		case domain.WorkspaceArchived:
			if owned {
				gaps = append(gaps, SessionGap{
					Workspace: workspace,
					Code:      SessionGapArchivedLive,
					Message:   fmt.Sprintf("archived Workspace %q still has an owned tmux session", workspace.Name),
				})
			}
		}
	}
	return gaps, nil
}
