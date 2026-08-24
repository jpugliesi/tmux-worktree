package transcript

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// maxDiscoverFiles limits the provider files that twt inspects for one
// discovery. twt inspects the newest files first.
const maxDiscoverFiles = 256

// DiscoveredSession is one provider session that belongs to a Workspace. Path
// stays inside twt. The JSON interface never shows a provider file path.
type DiscoveredSession struct {
	Provider       string
	SessionID      string
	RepositoryName string
	LastActivity   time.Time
	Path           string
}

// DiscoverOptions selects and filters the discovered provider sessions.
type DiscoverOptions struct {
	// Provider limits the result to one provider. An empty value reads all
	// providers that support verifiable linked transcripts.
	Provider string
	// Linked holds the Agent Sessions of the Workspace. Discover does not
	// return a provider session that one of these records uses.
	Linked []domain.AgentSession
	// IncludeLinked returns verified linked sessions too. It also keeps their
	// exact candidate files outside the bounded newest-file window.
	IncludeLinked bool
	// Since drops each provider session with an older last activity time.
	Since time.Time
}

// Discover finds the provider sessions that ran inside a repository of the
// Workspace. The result is sorted from the newest last activity to the oldest.
func (s *Service) Discover(workspace domain.Workspace, options DiscoverOptions) ([]DiscoveredSession, error) {
	names := []string{}
	for _, provider := range providerNames() {
		if options.Provider != "" && options.Provider != provider {
			continue
		}
		names = append(names, provider)
	}
	foundByProvider := make([][]DiscoveredSession, len(names))
	errorsByProvider := make([]error, len(names))
	linkedByProvider := linkedSessionIDs(options.Linked)
	var wait sync.WaitGroup
	for index, provider := range names {
		wait.Add(1)
		go func() {
			defer wait.Done()
			var linked map[string]bool
			if options.IncludeLinked {
				linked = linkedByProvider[provider]
			}
			foundByProvider[index], errorsByProvider[index] = s.discoverProvider(provider, workspace, options.Since, linked)
		}()
	}
	wait.Wait()
	sessions := []DiscoveredSession{}
	diagnostics := []error{}
	for index := range names {
		if errorsByProvider[index] != nil {
			diagnostics = append(diagnostics, fmt.Errorf("%s transcript discovery: %w", names[index], errorsByProvider[index]))
			continue
		}
		sessions = append(sessions, foundByProvider[index]...)
	}
	linked := linkedSessions(options.Linked)
	result := make([]DiscoveredSession, 0, len(sessions))
	for _, session := range sessions {
		if !options.IncludeLinked && linked[session.Provider+"\x00"+session.SessionID] {
			continue
		}
		result = append(result, session)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].LastActivity.Equal(result[j].LastActivity) {
			return result[i].SessionID < result[j].SessionID
		}
		return result[i].LastActivity.After(result[j].LastActivity)
	})
	return result, errors.Join(diagnostics...)
}

func (s *Service) discoverProvider(provider string, workspace domain.Workspace, since time.Time, linked map[string]bool) ([]DiscoveredSession, error) {
	descriptor := providers[provider]
	var linkedCandidate func(string) bool
	if len(linked) > 0 {
		linkedCandidate = func(path string) bool { return descriptor.linkedCandidate(path, linked) }
	}
	files, err := newestTranscriptFiles(descriptor.root(s), since, descriptor.transcriptName, linkedCandidate)
	if err != nil {
		return nil, err
	}
	sessions := []DiscoveredSession{}
	for _, file := range files {
		session, ok := readDiscovered(provider, descriptor, file, workspace)
		if !ok {
			continue
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

// readDiscovered verifies one provider file against the Workspace. A file that
// twt cannot verify is not an error: discovery drops it. Discovery reads
// session metadata only.
func readDiscovered(provider string, descriptor providerDescriptor, file transcriptFile, workspace domain.Workspace) (DiscoveredSession, bool) {
	sessionID, repositoryName, ok := descriptor.discover(file.path, workspace)
	if !ok || sessionID == "" || repositoryName == "" {
		return DiscoveredSession{}, false
	}
	return DiscoveredSession{
		Provider: provider, SessionID: sessionID, RepositoryName: repositoryName,
		LastActivity: file.modTime, Path: file.path,
	}, true
}

type transcriptFile struct {
	path    string
	modTime time.Time
}

// newestTranscriptFiles lists the regular JSON Lines files under root, from
// the newest to the oldest, with a limit on general discovery files. Exact
// linked candidates stay outside that window, with a separate bound. A set
// since time drops older files unless they are exact linked candidates.
func newestTranscriptFiles(root string, since time.Time, include func(string) bool, linkedCandidate func(string) bool) ([]transcriptFile, error) {
	files := []transcriptFile{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			return nil
		}
		if include != nil && !include(entry.Name()) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		linked := linkedCandidate != nil && linkedCandidate(path)
		if !since.IsZero() && !info.ModTime().After(since) && !linked {
			return nil
		}
		files = append(files, transcriptFile{path: path, modTime: info.ModTime()})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.After(files[j].modTime) })
	if len(files) > maxDiscoverFiles {
		newest := append([]transcriptFile(nil), files[:maxDiscoverFiles]...)
		linkedExtras := 0
		for _, file := range files[maxDiscoverFiles:] {
			if linkedCandidate == nil || !linkedCandidate(file.path) {
				continue
			}
			linkedExtras++
			if linkedExtras > maxDiscoverFiles {
				return nil, clierr.New(clierr.PreconditionFailed, "too many linked transcript candidates outside the discovery window")
			}
			newest = append(newest, file)
		}
		files = newest
	}
	return files, nil
}

func linkedSessionIDs(agents []domain.AgentSession) map[string]map[string]bool {
	result := map[string]map[string]bool{}
	for _, agent := range agents {
		if agent.ProviderSessionID == "" {
			continue
		}
		if result[agent.Provider] == nil {
			result[agent.Provider] = map[string]bool{}
		}
		result[agent.Provider][agent.ProviderSessionID] = true
	}
	return result
}

func linkedSessions(agents []domain.AgentSession) map[string]bool {
	linked := map[string]bool{}
	for _, agent := range agents {
		if agent.ProviderSessionID != "" {
			linked[agent.Provider+"\x00"+agent.ProviderSessionID] = true
		}
	}
	return linked
}
