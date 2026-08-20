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

func (s *Service) StorageCleanupPlan(templates TemplateDigests) (StorageCleanupPlan, error) {
	prepared, err := s.PreparedCleanupPlan(templates)
	if err != nil {
		return StorageCleanupPlan{}, err
	}
	projects, err := s.store.List()
	if err != nil {
		return StorageCleanupPlan{}, err
	}
	active := make(map[string]bool, len(projects))
	for _, project := range projects {
		active[project.ID] = true
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

func (s *Service) CleanStorage(templates TemplateDigests) (StorageCleanupPlan, error) {
	plan, err := s.StorageCleanupPlan(templates)
	if err != nil {
		return plan, err
	}
	if _, err := s.CleanPrepared(templates); err != nil {
		return plan, err
	}
	for _, snapshot := range plan.Snapshots {
		if err := s.cleanSnapshot(snapshot); err != nil {
			return plan, err
		}
	}
	for _, agent := range plan.Agents {
		if err := s.cleanOrphanAgent(agent); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

// cleanOrphanAgent deletes one Agent Session record. It checks again, inside
// the mutation lock, that the Project record does not exist.
func (s *Service) cleanOrphanAgent(item AgentCleanupItem) error {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return err
	}
	defer lock.Release()
	projects, err := s.store.List()
	if err != nil {
		return err
	}
	for _, project := range projects {
		if project.ID == item.ProjectID {
			return fmt.Errorf("Agent Session %q belongs to an existing Project", item.ID)
		}
	}
	return store.NewAgentStore(s.options.StateDir).Delete(item.ID)
}

func (s *Service) cleanSnapshot(snapshot SnapshotCleanupItem) error {
	if snapshot.ProjectID == "" {
		lock, err := store.AcquireMutationLock(s.options.StateDir)
		if err != nil {
			return err
		}
		defer lock.Release()
		return s.snapshots.DeleteTemporaryFile(snapshot.Root)
	}
	return s.cleanOrphanSnapshot(snapshot.ProjectID)
}

func (s *Service) cleanOrphanSnapshot(projectID string) error {
	lock, err := store.AcquireMutationLock(s.options.StateDir)
	if err != nil {
		return err
	}
	defer lock.Release()
	projects, err := s.store.List()
	if err != nil {
		return err
	}
	for _, project := range projects {
		if project.ID == projectID {
			return fmt.Errorf("Transcript Snapshot %q belongs to an existing Project", projectID)
		}
	}
	return s.snapshots.DeleteProject(projectID, false)
}
