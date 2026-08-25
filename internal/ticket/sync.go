package ticket

// SyncModeGit enables git synchronization of the Tickets home.
const SyncModeGit = "git"

// defaultSyncRemote is the git remote that sync uses when none is configured.
const defaultSyncRemote = "origin"

// SyncOptions is the resolved tickets-sync configuration. The zero value
// disables sync: the Service makes no git calls at all.
type SyncOptions struct {
	// Mode is "" or "off" for no sync, or "git".
	Mode string
	// Remote is the git remote name. The default is "origin".
	Remote string
}

// enabled reports whether git synchronization is on.
func (o SyncOptions) enabled() bool {
	return o.Mode == SyncModeGit
}

// remote returns the configured remote name or the default.
func (o SyncOptions) remote() string {
	if o.Remote == "" {
		return defaultSyncRemote
	}
	return o.Remote
}

// logf writes one best-effort sync warning when a logger is configured.
func (s *Service) logf(format string, a ...any) {
	if s.options.Logf == nil {
		return
	}
	s.options.Logf(format, a...)
}
