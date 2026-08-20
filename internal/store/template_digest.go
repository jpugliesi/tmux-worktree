package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// DigestSet holds the digests that can identify one Prepared Environment. A
// record can carry the environment digest or the older whole-template digest.
type DigestSet struct {
	Environment string
	Legacy      string
}

// Matches reports whether digest is one of the digests in this set.
func (d DigestSet) Matches(digest string) bool {
	if digest == "" {
		return false
	}
	return digest == d.Environment || digest == d.Legacy
}

// Digests returns the environment digest and the legacy whole-template digest.
func Digests(template domain.Template) (DigestSet, error) {
	environment, err := EnvironmentDigest(template)
	if err != nil {
		return DigestSet{}, err
	}
	legacy, err := LegacyTemplateDigest(template)
	if err != nil {
		return DigestSet{}, err
	}
	return DigestSet{Environment: environment, Legacy: legacy}, nil
}

// environmentDigestPayload holds only the Project Template values that change
// the physical worktrees of a Prepared Environment. A change to the Project
// Template name, a window name, the Project initialization, or the pool depth
// does not change this payload, so a prepared set stays usable.
type environmentDigestPayload struct {
	FormatVersion int                           `json:"formatVersion"`
	Repositories  []environmentDigestRepository `json:"repositories"`
}

type environmentDigestRepository struct {
	Name          string                       `json:"name"`
	CloneURL      string                       `json:"cloneUrl"`
	CloneDepth    int                          `json:"cloneDepth"`
	DefaultBranch string                       `json:"defaultBranch"`
	Remotes       map[string]string            `json:"remotes"`
	Initialize    *environmentDigestInitialize `json:"initialize"`
}

type environmentDigestInitialize struct {
	Command          []string `json:"command"`
	WorkingDirectory string   `json:"workingDirectory"`
}

// EnvironmentDigest identifies the physical worktree set of one Project
// Template revision and preparation format. encoding/json sorts string map
// keys, so the result does not depend on map insertion order.
func EnvironmentDigest(template domain.Template) (string, error) {
	payload := environmentDigestPayload{
		FormatVersion: domain.PreparationFormatVersion,
		Repositories:  make([]environmentDigestRepository, 0, len(template.Repositories)),
	}
	for _, repository := range template.Repositories {
		entry := environmentDigestRepository{
			Name:          repository.Name,
			CloneURL:      repository.Clone.URL,
			CloneDepth:    repository.Clone.Depth,
			DefaultBranch: repository.DefaultBranch,
			Remotes:       repository.Remotes,
		}
		if repository.Initialize != nil {
			entry.Initialize = &environmentDigestInitialize{
				Command:          repository.Initialize.Command,
				WorkingDirectory: repository.Initialize.WorkingDirectory,
			}
		}
		payload.Repositories = append(payload.Repositories, entry)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode Prepared Environment digest: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// LegacyTemplateDigest identifies one exact Project Template and preparation
// format. Installations that twt2 prepared before the digest split use it.
func LegacyTemplateDigest(template domain.Template) (string, error) {
	payload := struct {
		FormatVersion int             `json:"formatVersion"`
		Template      domain.Template `json:"template"`
	}{
		FormatVersion: domain.PreparationFormatVersion,
		Template:      template,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode Project Template digest: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
