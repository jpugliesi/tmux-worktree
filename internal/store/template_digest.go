package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// TemplateDigest identifies one exact Project Template and preparation format.
// encoding/json sorts string map keys, so the result does not depend on map
// insertion order.
func TemplateDigest(template domain.Template) (string, error) {
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
