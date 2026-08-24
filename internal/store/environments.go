package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

type EnvironmentStore struct {
	dir string
}

func NewEnvironmentStore(stateDir string) EnvironmentStore {
	return EnvironmentStore{dir: filepath.Join(stateDir, "environments")}
}

func (s EnvironmentStore) Save(environment domain.PreparedEnvironment) error {
	if err := validateEnvironment(environment); err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create Prepared Environment state directory: %w", err)
	}
	path := filepath.Join(s.dir, environment.ID+".json")
	existing, err := s.loadPath(path)
	if err == nil && existing.ClaimReservation != nil && !reflect.DeepEqual(existing.ClaimReservation, environment.ClaimReservation) {
		return fmt.Errorf("Prepared Environment %q claim reservation cannot change", environment.ID)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect Prepared Environment %q before save: %w", environment.ID, err)
	}
	data, err := json.MarshalIndent(environment, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Prepared Environment %q: %w", environment.ID, err)
	}
	data = append(data, '\n')
	return WriteFileAtomic(path, data, 0o600, "Prepared Environment state")
}

func (s EnvironmentStore) List() ([]domain.PreparedEnvironment, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.PreparedEnvironment{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Prepared Environment state directory: %w", err)
	}
	environments := make([]domain.PreparedEnvironment, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		environment, err := s.loadPath(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		environments = append(environments, environment)
	}
	sort.Slice(environments, func(i, j int) bool {
		if environments[i].CreatedAt.Equal(environments[j].CreatedAt) {
			return environments[i].ID < environments[j].ID
		}
		return environments[i].CreatedAt.Before(environments[j].CreatedAt)
	})
	return environments, nil
}

func (s EnvironmentStore) Find(id string) (domain.PreparedEnvironment, error) {
	if err := ValidateResourceName(id); err != nil {
		return domain.PreparedEnvironment{}, fmt.Errorf("invalid Prepared Environment ID: %w", err)
	}
	environment, err := s.loadPath(filepath.Join(s.dir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return domain.PreparedEnvironment{}, fmt.Errorf("Prepared Environment %q does not exist: %w", id, os.ErrNotExist)
	}
	return environment, err
}

func (s EnvironmentStore) Delete(id string) error {
	if err := ValidateResourceName(id); err != nil {
		return fmt.Errorf("invalid Prepared Environment ID: %w", err)
	}
	if err := os.Remove(filepath.Join(s.dir, id+".json")); errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("Prepared Environment %q does not exist: %w", id, os.ErrNotExist)
	} else if err != nil {
		return fmt.Errorf("delete Prepared Environment %q: %w", id, err)
	}
	if err := syncDirectory(s.dir); err != nil {
		return fmt.Errorf("sync Prepared Environment state directory: %w", err)
	}
	return nil
}

func (s EnvironmentStore) loadPath(path string) (domain.PreparedEnvironment, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return domain.PreparedEnvironment{}, fmt.Errorf("read Prepared Environment state %q: %w", path, err)
	}
	var environment domain.PreparedEnvironment
	var header struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return domain.PreparedEnvironment{}, fmt.Errorf("decode Prepared Environment state %q: %w", path, err)
	}
	if header.Version != domain.PreparedEnvironmentVersion {
		return domain.PreparedEnvironment{}, fmt.Errorf("Prepared Environment state %q uses unsupported version %d", path, header.Version)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&environment); err != nil {
		return domain.PreparedEnvironment{}, fmt.Errorf("decode Prepared Environment state %q: %w", path, err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return domain.PreparedEnvironment{}, fmt.Errorf("decode Prepared Environment state %q: %w", path, err)
	}
	normalizeLegacySetupSteps(environment.Steps)
	if environment.ClaimReservation != nil {
		normalizeLegacySetupSteps(environment.ClaimReservation.Workspace.Steps)
	}
	if environment.ID != filenameID(path) {
		return domain.PreparedEnvironment{}, fmt.Errorf("Prepared Environment state file %q contains ID %q", path, environment.ID)
	}
	if err := validateEnvironment(environment); err != nil {
		return domain.PreparedEnvironment{}, err
	}
	return environment, nil
}

func validateEnvironment(environment domain.PreparedEnvironment) error {
	if err := ValidateResourceName(environment.ID); err != nil {
		return fmt.Errorf("invalid Prepared Environment ID: %w", err)
	}
	if err := environment.Validate(); err != nil {
		return err
	}
	digests, err := Digests(environment.TemplateSnapshot)
	if err != nil {
		return fmt.Errorf("digest Workspace Template for Prepared Environment %q: %w", environment.ID, err)
	}
	if !digests.Matches(environment.TemplateDigest) {
		return fmt.Errorf("Prepared Environment %q has Workspace Template digest %q; expected %q", environment.ID, environment.TemplateDigest, digests.Environment)
	}
	return nil
}

func filenameID(path string) string {
	name := filepath.Base(path)
	return name[:len(name)-len(filepath.Ext(name))]
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not supported")
		}
		return err
	}
	return nil
}
