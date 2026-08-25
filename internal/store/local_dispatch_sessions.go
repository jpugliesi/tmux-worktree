package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

type LocalDispatchSessionStore struct {
	dir string
}

func NewLocalDispatchSessionStore(stateDir string) LocalDispatchSessionStore {
	return LocalDispatchSessionStore{dir: filepath.Join(stateDir, "local-dispatch-sessions")}
}

func (s LocalDispatchSessionStore) Save(session domain.LocalDispatchSession) error {
	if err := session.Validate(); err != nil {
		return fmt.Errorf("invalid local dispatch Session: %w", err)
	}
	if err := ValidateResourceName(session.ID); err != nil {
		return fmt.Errorf("invalid local dispatch Session ID: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create local dispatch Session state directory: %w", err)
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("encode local dispatch Session state: %w", err)
	}
	data = append(data, '\n')
	return WriteFileAtomic(filepath.Join(s.dir, session.ID+".json"), data, 0o600, "local dispatch Session state")
}

func (s LocalDispatchSessionStore) List() ([]domain.LocalDispatchSession, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.LocalDispatchSession{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read local dispatch Session state directory: %w", err)
	}
	sessions := make([]domain.LocalDispatchSession, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		session, loadErr := s.loadPath(filepath.Join(s.dir, entry.Name()))
		if loadErr != nil {
			return nil, loadErr
		}
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].CreatedAt.Equal(sessions[j].CreatedAt) {
			return sessions[i].ID < sessions[j].ID
		}
		return sessions[i].CreatedAt.Before(sessions[j].CreatedAt)
	})
	return sessions, nil
}

func (s LocalDispatchSessionStore) Find(reference string) (domain.LocalDispatchSession, error) {
	sessions, err := s.List()
	if err != nil {
		return domain.LocalDispatchSession{}, err
	}
	matches := make([]domain.LocalDispatchSession, 0, 1)
	for _, session := range sessions {
		if session.ID == reference {
			return session, nil
		}
		if reference != "" && strings.HasPrefix(session.ID, reference) {
			matches = append(matches, session)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return domain.LocalDispatchSession{}, clierr.New(clierr.InvalidUsage, "local dispatch Session reference %q is ambiguous", reference)
	}
	return domain.LocalDispatchSession{}, clierr.New(clierr.NotFound, "local dispatch Session %q does not exist", reference)
}

func (s LocalDispatchSessionStore) Delete(id string) error {
	if err := ValidateResourceName(id); err != nil {
		return fmt.Errorf("invalid local dispatch Session ID: %w", err)
	}
	if err := os.Remove(filepath.Join(s.dir, id+".json")); err != nil {
		return fmt.Errorf("delete local dispatch Session state: %w", err)
	}
	return nil
}

func (s LocalDispatchSessionStore) loadPath(path string) (domain.LocalDispatchSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.LocalDispatchSession{}, fmt.Errorf("read local dispatch Session state: %w", err)
	}
	var session domain.LocalDispatchSession
	if err := json.Unmarshal(data, &session); err != nil {
		return domain.LocalDispatchSession{}, fmt.Errorf("decode local dispatch Session state %q: %w", path, err)
	}
	if err := session.Validate(); err != nil {
		return domain.LocalDispatchSession{}, fmt.Errorf("decode local dispatch Session state %q: %w", path, err)
	}
	return session, nil
}
