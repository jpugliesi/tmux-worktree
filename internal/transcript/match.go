package transcript

import "time"

const liveSessionStartSkew = 5 * time.Second

// MatchLiveSession picks the provider session that belongs to one live pane.
// A start time match wins. One unused session for that repository or
// directory is next. When allowNewest is set, the newest leftover session
// for that place is used. A used session ID is skipped so two live panes do
// not share one session.
func MatchLiveSession(sessions []DiscoveredSession, provider, repository, directory string, started time.Time, used map[string]bool, allowNewest bool) (DiscoveredSession, bool) {
	if provider == "" || !SupportsProvider(provider) {
		return DiscoveredSession{}, false
	}
	directory = CanonicalDirectory(directory)
	if repository == "" && directory == "" {
		return DiscoveredSession{}, false
	}
	candidates := make([]DiscoveredSession, 0, len(sessions))
	for _, session := range sessions {
		if session.Provider != provider || session.SessionID == "" {
			continue
		}
		if !sameLivePlace(session, repository, directory) {
			continue
		}
		if used[session.Provider+"\x00"+session.SessionID] {
			continue
		}
		candidates = append(candidates, session)
	}
	if len(candidates) == 0 {
		return DiscoveredSession{}, false
	}
	if !started.IsZero() {
		matches := make([]DiscoveredSession, 0, len(candidates))
		for _, session := range candidates {
			if session.StartedAt.IsZero() {
				continue
			}
			delta := session.StartedAt.Sub(started)
			if delta < 0 {
				delta = -delta
			}
			if delta <= liveSessionStartSkew {
				matches = append(matches, session)
			}
		}
		if len(matches) == 1 {
			return matches[0], true
		}
	}
	if len(candidates) == 1 {
		return candidates[0], true
	}
	if !allowNewest {
		return DiscoveredSession{}, false
	}
	newest := candidates[0]
	for _, session := range candidates[1:] {
		if session.LastActivity.After(newest.LastActivity) {
			newest = session
		}
	}
	return newest, true
}

func sameLivePlace(session DiscoveredSession, repository, directory string) bool {
	if repository != "" && session.RepositoryName == repository {
		return true
	}
	sessionDirectory := CanonicalDirectory(session.Directory)
	return directory != "" && sessionDirectory != "" && sessionDirectory == directory
}
