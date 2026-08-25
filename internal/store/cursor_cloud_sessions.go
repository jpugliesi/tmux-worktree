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

type CursorCloudSessionStore struct {
	dir string
}

func NewCursorCloudSessionStore(stateDir string) CursorCloudSessionStore {
	return CursorCloudSessionStore{dir: filepath.Join(stateDir, "cursor-cloud-sessions")}
}

func (s CursorCloudSessionStore) Save(session domain.CursorCloudSession) error {
	if err := session.Validate(); err != nil {
		return fmt.Errorf("invalid Cursor Cloud Session: %w", err)
	}
	if err := ValidateResourceName(session.ID); err != nil {
		return fmt.Errorf("invalid Cursor Cloud Session ID: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create Cursor Cloud Session state directory: %w", err)
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Cursor Cloud Session state: %w", err)
	}
	data = append(data, '\n')
	return WriteFileAtomic(filepath.Join(s.dir, session.ID+".json"), data, 0o600, "Cursor Cloud Session state")
}

func (s CursorCloudSessionStore) List() ([]domain.CursorCloudSession, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.CursorCloudSession{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Cursor Cloud Session state directory: %w", err)
	}
	sessions := make([]domain.CursorCloudSession, 0, len(entries))
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

func (s CursorCloudSessionStore) Find(reference string) (domain.CursorCloudSession, error) {
	sessions, err := s.List()
	if err != nil {
		return domain.CursorCloudSession{}, err
	}
	matches := make([]domain.CursorCloudSession, 0, 1)
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
		return domain.CursorCloudSession{}, clierr.New(clierr.InvalidUsage, "Cursor Cloud Session reference %q is ambiguous", reference)
	}
	return domain.CursorCloudSession{}, clierr.New(clierr.NotFound, "Cursor Cloud Session %q does not exist", reference)
}

func (s CursorCloudSessionStore) Delete(id string) error {
	if err := ValidateResourceName(id); err != nil {
		return fmt.Errorf("invalid Cursor Cloud Session ID: %w", err)
	}
	if err := os.Remove(filepath.Join(s.dir, id+".json")); err != nil {
		return fmt.Errorf("delete Cursor Cloud Session state: %w", err)
	}
	return nil
}

func (s CursorCloudSessionStore) loadPath(path string) (domain.CursorCloudSession, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.CursorCloudSession{}, fmt.Errorf("read Cursor Cloud Session state: %w", err)
	}
	var session domain.CursorCloudSession
	if err := json.Unmarshal(data, &session); err != nil {
		return domain.CursorCloudSession{}, fmt.Errorf("decode Cursor Cloud Session state %q: %w", path, err)
	}
	if err := session.Validate(); err != nil {
		return domain.CursorCloudSession{}, fmt.Errorf("decode Cursor Cloud Session state %q: %w", path, err)
	}
	return session, nil
}
