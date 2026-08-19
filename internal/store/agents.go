package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

type AgentStore struct {
	dir string
}

func NewAgentStore(stateDir string) AgentStore {
	return AgentStore{dir: filepath.Join(stateDir, "agents")}
}

func (s AgentStore) Save(agent domain.AgentSession) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create Agent Session state directory: %w", err)
	}
	data, err := json.MarshalIndent(agent, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Agent Session: %w", err)
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(s.dir, ".twt2-agent-*")
	if err != nil {
		return fmt.Errorf("create temporary Agent Session state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write Agent Session state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync Agent Session state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, filepath.Join(s.dir, agent.ID+".json")); err != nil {
		return fmt.Errorf("save Agent Session: %w", err)
	}
	return nil
}

func (s AgentStore) List(projectID string) ([]domain.AgentSession, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.AgentSession{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list Agent Sessions: %w", err)
	}
	agents := make([]domain.AgentSession, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		agent, err := s.load(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if projectID == "" || agent.ProjectID == projectID {
			agents = append(agents, agent)
		}
	}
	sort.Slice(agents, func(i, j int) bool { return agents[i].UpdatedAt.After(agents[j].UpdatedAt) })
	return agents, nil
}

func (s AgentStore) Find(reference string) (domain.AgentSession, error) {
	agents, err := s.List("")
	if err != nil {
		return domain.AgentSession{}, err
	}
	var prefixMatches []domain.AgentSession
	for _, agent := range agents {
		if agent.ID == reference {
			return agent, nil
		}
		if strings.HasPrefix(agent.ID, reference) {
			prefixMatches = append(prefixMatches, agent)
		}
	}
	if len(prefixMatches) == 1 {
		return prefixMatches[0], nil
	}
	if len(prefixMatches) > 1 {
		return domain.AgentSession{}, fmt.Errorf("Agent Session ID prefix %q is ambiguous", reference)
	}
	return domain.AgentSession{}, fmt.Errorf("Agent Session %q does not exist", reference)
}

func (s AgentStore) DeleteProject(projectID string) error {
	agents, err := s.List(projectID)
	if err != nil {
		return err
	}
	for _, agent := range agents {
		if err := os.Remove(filepath.Join(s.dir, agent.ID+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete Agent Session %q: %w", agent.ID, err)
		}
	}
	return nil
}

func (s AgentStore) load(path string) (domain.AgentSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.AgentSession{}, err
	}
	var agent domain.AgentSession
	if err := json.Unmarshal(data, &agent); err != nil {
		return domain.AgentSession{}, fmt.Errorf("decode Agent Session %q: %w", path, err)
	}
	if agent.Version != domain.AgentVersion {
		return domain.AgentSession{}, fmt.Errorf("Agent Session %q uses unsupported version %d", agent.ID, agent.Version)
	}
	return agent, nil
}
