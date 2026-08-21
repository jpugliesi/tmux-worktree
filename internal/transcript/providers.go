package transcript

import (
	"sort"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// providerDescriptor keeps the knowledge about one provider in one place:
// where the provider stores its transcript files, how a session starts
// again, how twt reads one linked transcript, and how twt discovers the
// sessions of a Project.
type providerDescriptor struct {
	root          func(s *Service) string
	resumeCommand func(sessionID string) []string
	read          func(s *Service, sessionID string, project domain.Project) (Transcript, error)
	// transcriptName selects the JSON Lines files that belong to a session.
	// A nil value accepts every .jsonl file.
	transcriptName func(name string) bool
	// discover reads the session ID and the repository name of one provider
	// file. A file that twt cannot verify against the Project returns ok
	// false. Discovery must not read the transcript body.
	discover func(path string, project domain.Project) (sessionID, repositoryName string, ok bool)
}

// providers is the one table of providers that support verifiable linked
// transcripts. domain.AgentProviders stays the authority for provider names.
var providers = map[string]providerDescriptor{
	"codex": {
		root:          (*Service).codexRoot,
		resumeCommand: func(sessionID string) []string { return []string{"codex", "resume", sessionID} },
		read:          (*Service).readCodex,
		discover:      discoverCodex,
	},
	"claude": {
		root:          (*Service).claudeRoot,
		resumeCommand: func(sessionID string) []string { return []string{"claude", "--resume", sessionID} },
		read:          (*Service).readClaude,
		discover:      discoverClaude,
	},
	"grok": {
		root:           (*Service).grokRoot,
		resumeCommand:  func(sessionID string) []string { return []string{"grok", "--resume", sessionID} },
		read:           (*Service).readGrok,
		transcriptName: func(name string) bool { return name == "chat_history.jsonl" },
		discover:       discoverGrok,
	},
}

// providerNames returns the supported provider names in sorted order.
func providerNames() []string {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SupportsProvider reports whether the provider supports verifiable linked
// transcripts.
func SupportsProvider(provider string) bool {
	_, ok := providers[provider]
	return ok
}

// ResumeCommand returns the command that starts a discovered provider session
// again. An unsupported provider returns no command.
func ResumeCommand(provider, sessionID string) []string {
	descriptor, ok := providers[provider]
	if !ok {
		return nil
	}
	return descriptor.resumeCommand(sessionID)
}
