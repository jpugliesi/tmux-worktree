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

// TemplateStatus describes one current Project Template. Unreadable marks a
// Project Template that twt cannot load or digest.
type TemplateStatus struct {
	Digests    DigestSet
	Unreadable bool
}

// TemplateCatalog maps each current Project Template name to its status.
type TemplateCatalog map[string]TemplateStatus

// TemplateDisposition tells what to do with one prepared digest.
type TemplateDisposition int

const (
	// TemplateCurrent: the digest matches the current Project Template.
	TemplateCurrent TemplateDisposition = iota
	// TemplateKeep: twt cannot read the Project Template, so it cannot know
	// if the digest is obsolete. Keep the Prepared Environment.
	TemplateKeep
	// TemplateObsolete: the Project Template no longer exists, or the digest
	// no longer matches it.
	TemplateObsolete
)

// Disposition answers whether a Prepared Environment digest for templateName
// is current, must be kept, or is obsolete.
func (c TemplateCatalog) Disposition(templateName, digest string) TemplateDisposition {
	status, found := c[templateName]
	if !found {
		return TemplateObsolete
	}
	if status.Unreadable {
		return TemplateKeep
	}
	if status.Digests.Matches(digest) {
		return TemplateCurrent
	}
	return TemplateObsolete
}

// LoadTemplateCatalog reads each Project Template and returns its digests.
// The second return value holds one warning for each Project Template that
// twt cannot load; the catalog marks those entries as Unreadable.
func LoadTemplateCatalog(configDir string) (TemplateCatalog, []string, error) {
	templates := NewTemplateStore(configDir)
	names, err := templates.List()
	if err != nil {
		return nil, nil, err
	}
	catalog := make(TemplateCatalog, len(names))
	var warnings []string
	for _, name := range names {
		var digests DigestSet
		template, err := templates.Load(name)
		if err == nil {
			digests, err = Digests(template)
		}
		if err != nil {
			catalog[name] = TemplateStatus{Unreadable: true}
			warnings = append(warnings, fmt.Sprintf("Project Template %q is not valid. twt kept its Prepared Environments.", name))
			continue
		}
		catalog[name] = TemplateStatus{Digests: digests}
	}
	return catalog, warnings, nil
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
// format. Installations that twt prepared before the digest split use it.
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
