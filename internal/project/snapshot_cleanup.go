package project

import (
	"fmt"

	"github.com/jpugliesi/tmux-worktree/internal/store"
)

type StorageCleanupPlan struct {
	Environments []EnvironmentCleanupItem `json:"environments"`
	Snapshots    []SnapshotCleanupItem    `json:"snapshots"`
	Agents       []AgentCleanupItem       `json:"agents"`
}

type SnapshotCleanupItem struct {
	ProjectID string `json:"projectId,omitempty"`
	Reason    string `json:"reason"`
	Root      string `json:"root"`
	Bytes     int64  `json:"bytes"`
}

// AgentCleanupItem describes one Agent Session record whose Project record no
// longer exists.
type AgentCleanupItem struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Label     string `json:"label,omitempty"`
	Reason    string `json:"reason"`
}

func (s *Service) StorageCleanupPlan(templates store.TemplateCatalog) (StorageCleanupPlan, error) {
	prepared, err := s.PreparedCleanupPlan(templates)
	if err != nil {
		return StorageCleanupPlan{}, err
	}
	active, err := s.activeProjectSet()
	if err != nil {
		return StorageCleanupPlan{}, err
	}
	snapshots, err := s.snapshots.List()
	if err != nil {
		return StorageCleanupPlan{}, err
	}
	plan := StorageCleanupPlan{Environments: prepared.Environments}
	for _, snapshot := range snapshots {
		if !active[snapshot.ProjectID] {
			plan.Snapshots = append(plan.Snapshots, SnapshotCleanupItem{
				ProjectID: snapshot.ProjectID, Reason: "orphan Transcript Snapshot", Root: snapshot.Directory, Bytes: snapshot.Bytes,
			})
		}
	}
	temporaryFiles, err := s.snapshots.ListTemporaryFiles()
	if err != nil {
		return StorageCleanupPlan{}, err
	}
	for _, temporary := range temporaryFiles {
		plan.Snapshots = append(plan.Snapshots, SnapshotCleanupItem{
			Reason: "incomplete Transcript Snapshot write", Root: temporary.Path, Bytes: temporary.Bytes,
		})
	}
	agents, err := store.NewAgentStore(s.options.StateDir).List("")
	if err != nil {
		return StorageCleanupPlan{}, err
	}
	for _, agent := range agents {
		if active[agent.ProjectID] {
			continue
		}
		plan.Agents = append(plan.Agents, AgentCleanupItem{
			ID: agent.ID, ProjectID: agent.ProjectID, Label: agent.Label, Reason: "orphan Agent Session record",
		})
	}
	return plan, nil
}

func (s *Service) CleanStorage(templates store.TemplateCatalog) (StorageCleanupPlan, error) {
	plan, err := s.StorageCleanupPlan(templates)
	if err != nil {
		return plan, err
	}
	if _, err := s.CleanPrepared(templates); err != nil {
		return plan, err
	}
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return plan, err
	}
	defer lock.Release()
	// One project set serves the whole batch. It is read again inside the
	// mutation lock, so a Project that appeared after the plan stays safe.
	active, err := s.activeProjectSet()
	if err != nil {
		return plan, err
	}
	for _, snapshot := range plan.Snapshots {
		if snapshot.ProjectID == "" {
			if err := s.snapshots.DeleteTemporaryFile(snapshot.Root); err != nil {
				return plan, err
			}
			continue
		}
		err := withOrphanCheck(active, "Transcript Snapshot", snapshot.ProjectID, snapshot.ProjectID, func() error {
			return s.snapshots.DeleteProject(snapshot.ProjectID, false)
		})
		if err != nil {
			return plan, err
		}
	}
	agents := store.NewAgentStore(s.options.StateDir)
	for _, agent := range plan.Agents {
		err := withOrphanCheck(active, "Agent Session", agent.ID, agent.ProjectID, func() error {
			return agents.Delete(agent.ID)
		})
		if err != nil {
			return plan, err
		}
	}
	return plan, nil
}

// activeProjectSet returns the IDs of all recorded Projects.
func (s *Service) activeProjectSet() (map[string]bool, error) {
	projects, err := s.store.List()
	if err != nil {
		return nil, err
	}
	active := make(map[string]bool, len(projects))
	for _, project := range projects {
		active[project.ID] = true
	}
	return active, nil
}

// withOrphanCheck runs remove only when the active project set does not
// contain projectID. The caller must hold the mutation lock.
func withOrphanCheck(active map[string]bool, kind, name, projectID string, remove func() error) error {
	if active[projectID] {
		return fmt.Errorf("%s %q belongs to an existing Project", kind, name)
	}
	return remove()
}
