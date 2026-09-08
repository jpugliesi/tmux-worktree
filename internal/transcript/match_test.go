package transcript

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMatchLiveSessionPrefersTheStartTime(t *testing.T) {
	started := time.Date(2026, 9, 8, 16, 45, 0, 0, time.UTC)
	older := DiscoveredSession{Provider: "codex", SessionID: "old", RepositoryName: "app", StartedAt: started.Add(-time.Hour), LastActivity: started}
	live := DiscoveredSession{Provider: "codex", SessionID: "live", RepositoryName: "app", StartedAt: started, LastActivity: started.Add(time.Minute)}
	got, ok := MatchLiveSession([]DiscoveredSession{older, live}, "codex", "app", "", started, nil, false)
	if !ok || got.SessionID != "live" {
		t.Fatalf("MatchLiveSession() = %+v, ok=%v", got, ok)
	}
}

func TestMatchLiveSessionUsesTheSessionDirectory(t *testing.T) {
	session := DiscoveredSession{Provider: "codex", SessionID: "dir", Directory: "/work/app", LastActivity: time.Now().UTC()}
	got, ok := MatchLiveSession([]DiscoveredSession{session}, "codex", "", "/work/app", time.Time{}, nil, false)
	if !ok || got.SessionID != "dir" {
		t.Fatalf("MatchLiveSession() by directory = %+v, ok=%v", got, ok)
	}
}

func TestMatchLiveSessionUsesASymbolicLinkDirectory(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Fatal(err)
	}
	session := DiscoveredSession{Provider: "codex", SessionID: "link", Directory: realDir, LastActivity: time.Now().UTC()}
	got, ok := MatchLiveSession([]DiscoveredSession{session}, "codex", "", alias, time.Time{}, nil, false)
	if !ok || got.SessionID != "link" {
		t.Fatalf("MatchLiveSession() by symlink directory = %+v, ok=%v", got, ok)
	}
}

func TestMatchLiveSessionUsesTheOnlyRepositorySession(t *testing.T) {
	session := DiscoveredSession{Provider: "codex", SessionID: "only", RepositoryName: "app", LastActivity: time.Now().UTC()}
	got, ok := MatchLiveSession([]DiscoveredSession{session}, "codex", "app", "", time.Time{}, nil, false)
	if !ok || got.SessionID != "only" {
		t.Fatalf("MatchLiveSession() = %+v, ok=%v", got, ok)
	}
}

func TestMatchLiveSessionKeepsAmbiguousSessionsUnbound(t *testing.T) {
	now := time.Now().UTC()
	first := DiscoveredSession{Provider: "codex", SessionID: "one", RepositoryName: "app", LastActivity: now}
	second := DiscoveredSession{Provider: "codex", SessionID: "two", RepositoryName: "app", LastActivity: now.Add(time.Minute)}
	if _, ok := MatchLiveSession([]DiscoveredSession{first, second}, "codex", "app", "", time.Time{}, nil, false); ok {
		t.Fatal("MatchLiveSession() bound an ambiguous session")
	}
	got, ok := MatchLiveSession([]DiscoveredSession{first, second}, "codex", "app", "", time.Time{}, nil, true)
	if !ok || got.SessionID != "two" {
		t.Fatalf("MatchLiveSession(allowNewest) = %+v, ok=%v", got, ok)
	}
}
