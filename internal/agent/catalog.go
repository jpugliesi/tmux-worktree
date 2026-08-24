package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jpugliesi/tmux-worktree/internal/agentprovider"
	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
	tmuxclient "github.com/jpugliesi/tmux-worktree/internal/tmux"
	"github.com/jpugliesi/tmux-worktree/internal/transcript"
)

const (
	livePaneReferenceVersion   = "pane-v1-"
	transcriptReferenceVersion = "transcript-v1-"
	maxPanePreviewBytes        = 128 << 10
	maxPanePreviewLineBytes    = 4096
)

// IsLivePaneReference reports whether a reference has the versioned live-pane
// namespace. It lets callers avoid unrelated transcript scans.
func IsLivePaneReference(reference string) bool {
	return strings.HasPrefix(reference, livePaneReferenceVersion)
}

// CouldBeLivePaneReference reports whether a value is a full live-pane
// reference or a prefix of that namespace.
func CouldBeLivePaneReference(reference string) bool {
	return reference != "" && (IsLivePaneReference(reference) || strings.HasPrefix(livePaneReferenceVersion, reference))
}

// CatalogResult is a read-only view of registered and discovered Agent
// Sessions for one Workspace. A diagnostic from one discovery source does not
// remove valid entries from another source.
type CatalogResult struct {
	Entries     []CatalogEntry
	Complete    bool
	Diagnostics []string
}

// CatalogEntry is one picker entry. Reference is an Agent Session ID for a
// registered record or a versioned temporary reference for live pane evidence.
type CatalogEntry struct {
	Reference           string
	AgentSessionID      string
	ProviderSessionID   string
	Provider            string
	Label               string
	Status              string
	Registration        string
	Runtime             string
	RepositoryName      string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	LastActivity        time.Time
	CanPreview          bool
	CanSnapshot         bool
	CanResume           bool
	CanSend             bool
	CanFocus            bool
	registered          *domain.AgentSession
	transcriptCandidate *transcript.DiscoveredSession
	pane                *tmuxclient.PaneObservation
	process             *tmuxclient.ProcessObservation
}

// Preview is sanitized, bounded Agent Preview text. A livePane source is only
// the visible screen and is never an Agent Transcript or Transcript Snapshot.
type Preview struct {
	Reference      string
	Provider       string
	RepositoryName string
	Source         string
	Markdown       string
	UpdatedAt      time.Time
	Truncated      bool
}

// Catalog lists stored Agent Sessions, verified provider transcripts, and
// strongly identified provider processes in live Workspace panes. It never
// writes state, claims a pane, or saves a transcript link.
func (s *Service) Catalog(workspace domain.Workspace) (CatalogResult, error) {
	registered, err := s.store.List(workspace.ID)
	if err != nil {
		return CatalogResult{}, err
	}
	result := CatalogResult{Complete: true}

	var panes []tmuxclient.PaneObservation
	panes, err = s.tmux.ObserveWorkspace(workspace)
	if err != nil && workspace.Status != domain.WorkspaceArchived {
		result.Complete = false
		result.Diagnostics = append(result.Diagnostics, err.Error())
	}
	panesByAgent := map[string]tmuxclient.PaneObservation{}
	for _, pane := range panes {
		if pane.AgentID != "" {
			panesByAgent[pane.AgentID] = pane
		}
	}

	registeredByTranscript := map[string]int{}
	for index := range registered {
		agent := registered[index]
		pane, paneFound := panesByAgent[agent.ID]
		process, processFound := runtimeProviderProcess(pane, agent)
		_, runtimeBound := processBinding(agent)
		live := processFound
		if !runtimeBound {
			live = paneFound && !pane.Dead && agent.PaneStart != "" && pane.StartCommand == agent.PaneStart
		}
		entry := CatalogEntry{
			Reference: agent.ID, AgentSessionID: agent.ID, ProviderSessionID: agent.ProviderSessionID,
			Provider: agent.Provider, Label: agent.Label, Status: "stopped", Registration: "registered",
			Runtime: "stopped", CreatedAt: agent.CreatedAt, UpdatedAt: agent.UpdatedAt,
			LastActivity: agent.UpdatedAt, CanResume: workspace.Status == domain.WorkspaceActive && (live || len(agent.ResumeCommand) > 0),
			CanSend: live, CanFocus: live, registered: &agent,
		}
		if live {
			entry.Status, entry.Runtime = "live", "live"
		}
		if paneFound && live {
			if !runtimeBound {
				process, processFound = observedProviderProcess(pane, agent.Provider)
			}
			if processFound {
				entry.CanPreview, entry.CanFocus = true, true
				if runtimeBound {
					entry.CanSend = sameCommand(pane.CurrentCommand, agent.PaneCommand)
				}
				entry.Status, entry.Runtime = "live", "live"
				paneCopy, processCopy := pane, process
				entry.pane, entry.process = &paneCopy, &processCopy
			}
		}
		result.Entries = append(result.Entries, entry)
		if agent.ProviderSessionID != "" && transcript.SupportsProvider(agent.Provider) {
			registeredByTranscript[agent.Provider+"\x00"+agent.ProviderSessionID] = len(result.Entries) - 1
		}
	}

	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		result.Complete = false
		result.Diagnostics = append(result.Diagnostics, fmt.Sprintf("find home directory: %v", homeErr))
	} else {
		found, discoverErr := transcript.New(home, s.stateDir).Discover(workspace, transcript.DiscoverOptions{
			Linked: registered, IncludeLinked: true,
		})
		if discoverErr != nil {
			result.Complete = false
			result.Diagnostics = append(result.Diagnostics, discoverErr.Error())
		}
		for index := range found {
			session := found[index]
			if registeredIndex, linked := registeredByTranscript[session.Provider+"\x00"+session.SessionID]; linked {
				entry := &result.Entries[registeredIndex]
				entry.CanPreview, entry.CanSnapshot = true, true
				entry.RepositoryName = session.RepositoryName
				continue
			}
			sessionCopy := session
			result.Entries = append(result.Entries, CatalogEntry{
				Reference: TranscriptReference(session.Provider, session.SessionID), ProviderSessionID: session.SessionID,
				Provider: session.Provider, Label: session.Provider, Status: "discovered", Registration: "discovered", Runtime: "stopped",
				RepositoryName: session.RepositoryName, LastActivity: session.LastActivity,
				CanPreview: true, CanSnapshot: true, CanResume: workspace.Status == domain.WorkspaceActive,
				transcriptCandidate: &sessionCopy,
			})
		}
	}

	result.Entries = append(result.Entries, s.livePaneEntries(workspace, panes)...)
	return result, nil
}

func (s *Service) livePaneEntries(workspace domain.Workspace, panes []tmuxclient.PaneObservation) []CatalogEntry {
	entries := []CatalogEntry{}
	for index := range panes {
		pane := panes[index]
		if workspace.Status != domain.WorkspaceActive || pane.Dead || pane.AgentID != "" || pane.RootStarted == "" {
			continue
		}
		process, provider, ok := oneObservedProviderProcess(pane)
		if !ok {
			continue
		}
		paneCopy, processCopy := pane, process
		activity := parseProcessStart(process.Started)
		if activity.IsZero() {
			activity = s.now()
		}
		entries = append(entries, CatalogEntry{
			Reference: livePaneReference(workspace.ID, pane, process, provider),
			Provider:  provider, Label: provider, Status: "discovered", Registration: "discovered", Runtime: "live",
			RepositoryName: workspaceRepository(workspace, pane.CurrentPath), LastActivity: activity,
			CanPreview: true, CanResume: true, CanSend: true, CanFocus: true,
			pane: &paneCopy, process: &processCopy,
		})
	}
	return entries
}

// Preview reads one Catalog entry without adopting it. A verified Agent
// Transcript wins. A strongly observed live pane is the fallback.
func (s *Service) Preview(workspace domain.Workspace, reference string) (Preview, error) {
	if IsLivePaneReference(reference) {
		entry, found, err := s.findLivePaneCandidate(workspace, reference)
		if err != nil {
			return Preview{}, err
		}
		if !found {
			return Preview{}, clierr.New(clierr.NotFound, "Agent Session %q does not exist", reference)
		}
		return s.previewEntry(workspace, entry, reference)
	}
	catalog, err := s.Catalog(workspace)
	if err != nil {
		return Preview{}, err
	}
	entry, err := findCatalogEntry(catalog.Entries, reference)
	if err != nil {
		return Preview{}, err
	}
	return s.previewEntry(workspace, entry, reference)
}

func (s *Service) previewEntry(workspace domain.Workspace, entry CatalogEntry, reference string) (Preview, error) {
	if entry.ProviderSessionID != "" && transcript.SupportsProvider(entry.Provider) {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return Preview{}, fmt.Errorf("find home directory: %w", homeErr)
		}
		value, readErr := transcript.New(home, s.stateDir).Read(entry.Provider, entry.ProviderSessionID, workspace)
		if readErr == nil {
			return Preview{
				Reference: reference, Provider: value.Provider, RepositoryName: value.RepositoryName,
				Source: "transcript", Markdown: value.Markdown, UpdatedAt: value.UpdatedAt,
			}, nil
		}
		if entry.pane == nil {
			return Preview{}, readErr
		}
	}
	if entry.pane == nil || entry.process == nil {
		return Preview{}, clierr.New(clierr.PreconditionFailed, "Agent Session %q has no available Agent Preview", reference)
	}
	freshPane, _, ok := s.freshProviderProcess(workspace, entry)
	if !ok {
		return Preview{}, clierr.New(clierr.PreconditionFailed, "Agent Session %q is no longer live", reference)
	}
	text, err := s.tmux.CaptureVisible(freshPane.ID, workspace.ID)
	if err != nil {
		return Preview{}, err
	}
	if _, _, ok := s.freshProviderProcess(workspace, entry); !ok {
		return Preview{}, clierr.New(clierr.PreconditionFailed, "Agent Session %q stopped during preview", reference)
	}
	text, truncated := boundedPanePreview(transcript.SanitizeUntrusted(text))
	return Preview{
		Reference: reference, Provider: entry.Provider, RepositoryName: entry.RepositoryName,
		Source: "livePane", Markdown: "_Live pane preview. This is not the full Agent Transcript._\n\n```text\n" + text + "\n```\n",
		UpdatedAt: s.now(), Truncated: truncated,
	}, nil
}

func (s *Service) freshProviderProcess(workspace domain.Workspace, entry CatalogEntry) (tmuxclient.PaneObservation, tmuxclient.ProcessObservation, bool) {
	panes, err := s.tmux.ObserveWorkspace(workspace)
	if err != nil || entry.pane == nil || entry.process == nil {
		return tmuxclient.PaneObservation{}, tmuxclient.ProcessObservation{}, false
	}
	for _, pane := range panes {
		if pane.ID != entry.pane.ID {
			continue
		}
		for _, process := range pane.Foreground {
			if !sameObservedProcess(process, *entry.process) {
				continue
			}
			identified := agentprovider.IdentifyProcess(agentprovider.Process{
				Command: process.Command, Executable: process.Executable, Args: process.Args,
			})
			return pane, process, identified == entry.Provider
		}
	}
	return tmuxclient.PaneObservation{}, tmuxclient.ProcessObservation{}, false
}

func sameObservedProcess(first, second tmuxclient.ProcessObservation) bool {
	return first.ID == second.ID && first.Started == second.Started && sameCommand(first.Command, second.Command) &&
		first.Executable == second.Executable && slices.Equal(first.Args, second.Args)
}

func observedProviderProcess(pane tmuxclient.PaneObservation, provider string) (tmuxclient.ProcessObservation, bool) {
	var found tmuxclient.ProcessObservation
	matches := 0
	for _, process := range pane.Foreground {
		identified := agentprovider.IdentifyProcess(agentprovider.Process{
			Command: process.Command, Executable: process.Executable, Args: process.Args,
		})
		if identified == provider {
			found, matches = process, matches+1
		}
	}
	return found, matches == 1
}

func runtimeProviderProcess(pane tmuxclient.PaneObservation, agent domain.AgentSession) (tmuxclient.ProcessObservation, bool) {
	binding, ok := processBinding(agent)
	if !ok || pane.ID == "" || pane.ID != agent.TmuxPane ||
		pane.RootProcessID != binding.PaneRootID || pane.RootStarted != binding.PaneRootStarted {
		return tmuxclient.ProcessObservation{}, false
	}
	for _, process := range pane.Foreground {
		if process.ID != binding.ID || process.Started != binding.Started || !sameCommand(process.Command, binding.Command) ||
			tmuxclient.ProcessEvidence(process) != binding.Evidence {
			continue
		}
		identified := agentprovider.IdentifyProcess(agentprovider.Process{
			Command: process.Command, Executable: process.Executable, Args: process.Args,
		})
		return process, identified == agent.Provider
	}
	return tmuxclient.ProcessObservation{}, false
}

func sameCommand(first, second string) bool {
	return strings.EqualFold(filepath.Base(strings.TrimSpace(first)), filepath.Base(strings.TrimSpace(second)))
}

func oneObservedProviderProcess(pane tmuxclient.PaneObservation) (tmuxclient.ProcessObservation, string, bool) {
	var found tmuxclient.ProcessObservation
	provider := ""
	matches := 0
	for _, process := range pane.Foreground {
		identified := agentprovider.IdentifyProcess(agentprovider.Process{
			Command: process.Command, Executable: process.Executable, Args: process.Args,
		})
		if identified != "" {
			found, provider, matches = process, identified, matches+1
		}
	}
	return found, provider, matches == 1
}

func livePaneReference(workspaceID string, pane tmuxclient.PaneObservation, process tmuxclient.ProcessObservation, provider string) string {
	evidence := strings.Join([]string{workspaceID, pane.ID, provider, fmt.Sprint(process.ID), process.Started}, "\x00")
	digest := sha256.Sum256([]byte(evidence))
	return livePaneReferenceVersion + hex.EncodeToString(digest[:12])
}

// TranscriptReference returns the provider-qualified, versioned public
// reference for one unregistered transcript candidate.
func TranscriptReference(provider, sessionID string) string {
	evidence := provider + "\x00" + sessionID
	digest := sha256.Sum256([]byte(evidence))
	return transcriptReferenceVersion + hex.EncodeToString(digest[:12])
}

func findCatalogEntry(entries []CatalogEntry, reference string) (CatalogEntry, error) {
	exact := []CatalogEntry{}
	matches := []CatalogEntry{}
	for _, entry := range entries {
		if entry.Reference == reference || entry.AgentSessionID == reference ||
			entry.ProviderSessionID == reference || (entry.registered != nil && entry.registered.RuntimeReference == reference) {
			exact = append(exact, entry)
			continue
		}
		if strings.HasPrefix(entry.Reference, reference) ||
			strings.HasPrefix(entry.ProviderSessionID, reference) ||
			(entry.registered != nil && strings.HasPrefix(entry.registered.RuntimeReference, reference)) {
			matches = append(matches, entry)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	if len(exact) > 1 {
		return CatalogEntry{}, clierr.New(clierr.InvalidUsage, "Agent Session reference %q is ambiguous", reference)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return CatalogEntry{}, clierr.New(clierr.InvalidUsage, "Agent Session reference %q is ambiguous", reference)
	}
	return CatalogEntry{}, clierr.New(clierr.NotFound, "Agent Session %q does not exist", reference)
}

func parseProcessStart(value string) time.Time {
	parsed, _ := time.ParseInLocation("Mon Jan 2 15:04:05 2006", value, time.Local)
	return parsed.UTC()
}

func workspaceRepository(workspace domain.Workspace, directory string) string {
	cleanDirectory, err := filepath.Abs(directory)
	if err != nil {
		return ""
	}
	for _, repository := range workspace.Repositories {
		root, rootErr := filepath.Abs(repository.Path)
		if rootErr != nil {
			continue
		}
		relative, relativeErr := filepath.Rel(root, cleanDirectory)
		if relativeErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return repository.Name
		}
	}
	return ""
}

func boundedPanePreview(text string) (string, bool) {
	truncated := false
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		if len(line) > maxPanePreviewLineBytes {
			lines[index] = validUTF8Prefix(line, maxPanePreviewLineBytes-len("…")) + "…"
			truncated = true
		}
	}
	text = strings.Join(lines, "\n")
	if len(text) > maxPanePreviewBytes {
		text = validUTF8Suffix(text, maxPanePreviewBytes)
		truncated = true
	}
	return strings.TrimSpace(text), truncated
}

func validUTF8Prefix(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.ValidString(text[:end]) {
		end--
	}
	return text[:end]
}

func validUTF8Suffix(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	start := len(text) - limit
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:]
}
