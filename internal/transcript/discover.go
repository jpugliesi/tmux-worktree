package transcript

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

// maxDiscoverFiles limits the provider files that twt reads for one
// discovery. twt reads the newest files first.
const maxDiscoverFiles = 256

// DiscoveredSession is one provider session that belongs to a Project. Path
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
	// Linked holds the Agent Sessions of the Project. Discover does not
	// return a provider session that one of these records uses.
	Linked []domain.AgentSession
	// Since drops each provider session with an older last activity time.
	Since time.Time
}

// Discover finds the provider sessions that ran inside a repository of the
// Project. The result is sorted from the newest last activity to the oldest.
func (s *Service) Discover(project domain.Project, options DiscoverOptions) ([]DiscoveredSession, error) {
	sessions := []DiscoveredSession{}
	for _, provider := range providerNames() {
		if options.Provider != "" && options.Provider != provider {
			continue
		}
		found, err := s.discoverProvider(provider, project)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, found...)
	}
	linked := linkedSessions(options.Linked)
	result := make([]DiscoveredSession, 0, len(sessions))
	for _, session := range sessions {
		if linked[session.Provider+"\x00"+session.SessionID] {
			continue
		}
		if !options.Since.IsZero() && !session.LastActivity.After(options.Since) {
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
	return result, nil
}

func (s *Service) discoverProvider(provider string, project domain.Project) ([]DiscoveredSession, error) {
	descriptor := providers[provider]
	files, err := newestTranscriptFiles(descriptor.root(s))
	if err != nil {
		return nil, err
	}
	sessions := []DiscoveredSession{}
	for _, file := range files {
		session, ok := readDiscovered(provider, descriptor, file, project)
		if !ok {
			continue
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

// readDiscovered reads one provider file. A file that twt cannot verify
// against the Project is not an error: discovery drops it.
func readDiscovered(provider string, descriptor providerDescriptor, file transcriptFile, project domain.Project) (DiscoveredSession, bool) {
	lines, info, err := readJSONLines(file.path)
	if err != nil {
		return DiscoveredSession{}, false
	}
	sessionID, repositoryName, ok := descriptor.discover(file.path, lines, project)
	if !ok || sessionID == "" || repositoryName == "" {
		return DiscoveredSession{}, false
	}
	return DiscoveredSession{
		Provider: provider, SessionID: sessionID, RepositoryName: repositoryName,
		LastActivity: info.ModTime(), Path: file.path,
	}, true
}

type transcriptFile struct {
	path    string
	modTime time.Time
}

// newestTranscriptFiles lists the regular JSON Lines files under root, from
// the newest to the oldest, with a limit on the number of files.
func newestTranscriptFiles(root string) ([]transcriptFile, error) {
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
		info, err := entry.Info()
		if err != nil {
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
		files = files[:maxDiscoverFiles]
	}
	return files, nil
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
