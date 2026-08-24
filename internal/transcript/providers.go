package transcript

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/agentprovider"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// providerDescriptor keeps the knowledge about one provider in one place:
// where the provider stores its transcript files, how a session starts
// again, how twt reads one linked transcript, and how twt discovers the
// sessions of a Workspace.
type providerDescriptor struct {
	root func(s *Service) string
	read func(s *Service, sessionID string, workspace domain.Workspace) (Transcript, error)
	// linkedCandidate identifies a file that can hold one of the exact linked
	// session IDs. Discovery keeps these files outside its newest-file window.
	linkedCandidate func(path string, sessionIDs map[string]bool) bool
	// transcriptName selects the JSON Lines files that belong to a session.
	// A nil value accepts every .jsonl file.
	transcriptName func(name string) bool
	// discover reads the session ID and the repository name of one provider
	// file. A file that twt cannot verify against the Workspace returns ok
	// false. Discovery must not read the transcript body.
	discover func(path string, workspace domain.Workspace) (sessionID, repositoryName string, ok bool)
}

// providers is the one table of providers that support verifiable linked
// transcripts. agentprovider stays the authority for provider names and
// static capabilities.
var providers = map[string]providerDescriptor{
	"codex": {
		root: (*Service).codexRoot,
		read: (*Service).readCodex,
		linkedCandidate: func(path string, sessionIDs map[string]bool) bool {
			name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
			for sessionID := range sessionIDs {
				if strings.HasSuffix(name, sessionID) {
					return true
				}
			}
			return false
		},
		discover: discoverCodex,
	},
	"claude": {
		root: (*Service).claudeRoot,
		read: (*Service).readClaude,
		linkedCandidate: func(path string, sessionIDs map[string]bool) bool {
			return sessionIDs[strings.TrimSuffix(filepath.Base(path), ".jsonl")]
		},
		discover: discoverClaude,
	},
	"grok": {
		root: (*Service).grokRoot,
		read: (*Service).readGrok,
		linkedCandidate: func(path string, sessionIDs map[string]bool) bool {
			return filepath.Base(path) == "chat_history.jsonl" && sessionIDs[filepath.Base(filepath.Dir(path))]
		},
		transcriptName: func(name string) bool { return name == "chat_history.jsonl" },
		discover:       discoverGrok,
	},
}

// providerNames returns transcript provider names in registry order.
func providerNames() []string {
	names := make([]string, 0, len(providers))
	for _, name := range agentprovider.Names() {
		if _, ok := providers[name]; ok {
			names = append(names, name)
		}
	}
	return names
}

// SupportsProvider reports whether the provider supports verifiable linked
// transcripts.
func SupportsProvider(provider string) bool {
	descriptor, known := agentprovider.Lookup(provider)
	_, implemented := providers[provider]
	return known && descriptor.SupportsTranscript() && implemented
}

// ResumeCommand returns the command that starts a discovered provider session
// again. An unsupported provider returns no command.
func ResumeCommand(provider, sessionID string) []string {
	descriptor, ok := agentprovider.Lookup(provider)
	if !ok || !SupportsProvider(provider) {
		return nil
	}
	return descriptor.ResumeCommand(sessionID)
}

// ValidateProviders checks that registry transcript capabilities and the
// provider transcript Adapters agree.
func ValidateProviders() error {
	for _, name := range agentprovider.Names() {
		descriptor, _ := agentprovider.Lookup(name)
		_, implemented := providers[name]
		if descriptor.SupportsTranscript() != implemented {
			return fmt.Errorf("provider %q transcript registry and Adapter disagree", name)
		}
		if implemented && providers[name].linkedCandidate == nil {
			return fmt.Errorf("provider %q has no linked transcript candidate resolver", name)
		}
	}
	return nil
}
