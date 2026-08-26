// Package prstate reads the live state of pull requests from their forges.
// Ticket frontmatter carries only canonical PR URLs; this package fetches
// state on demand and caches it in the state directory. It never fails a
// command: every failure degrades to the unknown state with a hint.
package prstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/store"
)

type State string

const (
	StateOpen    State = "open"
	StateDraft   State = "draft"
	StateMerged  State = "merged"
	StateClosed  State = "closed"
	StateUnknown State = "unknown"
)

type Checks string

const (
	ChecksPass    Checks = "pass"
	ChecksFail    Checks = "fail"
	ChecksPending Checks = "pending"
	ChecksNone    Checks = "none"
	ChecksUnknown Checks = "unknown"
)

type ReviewDecision string

const (
	ReviewApproved         ReviewDecision = "approved"
	ReviewChangesRequested ReviewDecision = "changes_requested"
	ReviewRequired         ReviewDecision = "review_required"
	ReviewUnknown          ReviewDecision = "unknown"
)

// PRState is the live state of one pull request URL. It lives only in the
// state-directory cache, never in ticket files.
type PRState struct {
	URL            string         `json:"url"`
	Host           string         `json:"host"`
	State          State          `json:"state"`
	Checks         Checks         `json:"checks"`
	ReviewDecision ReviewDecision `json:"reviewDecision"`
	UpdatedAt      time.Time      `json:"updatedAt,omitempty"`
	FetchedAt      time.Time      `json:"fetchedAt"`
	// Hint says why the state is unknown.
	Hint string `json:"hint,omitempty"`
}

// Resolver fetches the state of one pull request URL for one forge host.
type Resolver interface {
	Host() string
	Fetch(ctx context.Context, url string) (PRState, error)
}

const (
	freshFor          = 120 * time.Second
	unknownFreshFor   = 30 * time.Second
	perFetchTimeout   = 5 * time.Second
	concurrentFetches = 4
)

// Service reads PR states through per-host resolvers with a TTL cache.
type Service struct {
	dir       string
	resolvers map[string]Resolver
	now       func() time.Time
}

// NewService builds the service. A nil resolvers slice installs the real
// gh and origin resolvers.
func NewService(stateDir string, resolvers []Resolver) *Service {
	if resolvers == nil {
		resolvers = []Resolver{NewGitHubResolver(), NewOriginResolver()}
	}
	byHost := make(map[string]Resolver, len(resolvers))
	for _, resolver := range resolvers {
		byHost[resolver.Host()] = resolver
	}
	return &Service{dir: filepath.Join(stateDir, "pr-state"), resolvers: byHost, now: time.Now}
}

// Get returns the state of one URL: cached when fresh, fetched otherwise.
// offline never execs; it returns the cached state at any age, or unknown.
func (s *Service) Get(ctx context.Context, prURL string, offline bool) PRState {
	cached, hasCached := s.readCache(prURL)
	if hasCached {
		age := s.now().Sub(cached.FetchedAt)
		limit := freshFor
		if cached.State == StateUnknown {
			limit = unknownFreshFor
		}
		if age < limit || offline {
			return cached
		}
	}
	if offline {
		return s.unknown(prURL, "offline: no cached PR state")
	}
	fetched := s.fetch(ctx, prURL)
	s.writeCache(fetched)
	return fetched
}

// GetAll fetches states concurrently with a bounded worker pool.
func (s *Service) GetAll(ctx context.Context, urls []string, offline bool) map[string]PRState {
	results := make(map[string]PRState, len(urls))
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, concurrentFetches)
	unique := map[string]bool{}
	for _, prURL := range urls {
		if unique[prURL] {
			continue
		}
		unique[prURL] = true
		wg.Add(1)
		go func(prURL string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			state := s.Get(ctx, prURL, offline)
			mu.Lock()
			results[prURL] = state
			mu.Unlock()
		}(prURL)
	}
	wg.Wait()
	return results
}

func (s *Service) fetch(ctx context.Context, prURL string) PRState {
	host := urlHost(prURL)
	resolver, found := s.resolvers[host]
	if !found {
		return s.unknown(prURL, fmt.Sprintf("no PR-state resolver for host %q", host))
	}
	fetchCtx, cancel := context.WithTimeout(ctx, perFetchTimeout)
	defer cancel()
	state, err := resolver.Fetch(fetchCtx, prURL)
	if err != nil {
		return s.unknown(prURL, err.Error())
	}
	state.URL = prURL
	state.Host = host
	state.FetchedAt = s.now()
	if state.Checks == "" {
		state.Checks = ChecksUnknown
	}
	if state.ReviewDecision == "" {
		state.ReviewDecision = ReviewUnknown
	}
	return state
}

func (s *Service) unknown(prURL, hint string) PRState {
	return PRState{
		URL: prURL, Host: urlHost(prURL), State: StateUnknown,
		Checks: ChecksUnknown, ReviewDecision: ReviewUnknown,
		FetchedAt: s.now(), Hint: hint,
	}
}

func urlHost(prURL string) string {
	parsed, err := url.Parse(prURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}

func (s *Service) cachePath(prURL string) string {
	digest := sha256.Sum256([]byte(prURL))
	return filepath.Join(s.dir, hex.EncodeToString(digest[:8])+".json")
}

func (s *Service) readCache(prURL string) (PRState, bool) {
	data, err := os.ReadFile(s.cachePath(prURL))
	if err != nil {
		return PRState{}, false
	}
	var state PRState
	if err := json.Unmarshal(data, &state); err != nil || state.URL != prURL {
		return PRState{}, false
	}
	return state, true
}

func (s *Service) writeCache(state PRState) {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return
	}
	data, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = store.WriteFileAtomic(s.cachePath(state.URL), data, 0o644, "PR state cache")
}

// runCommand is the exec seam shared by the resolvers. Tests replace it per
// resolver instance.
type runCommand func(ctx context.Context, name string, args ...string) ([]byte, error)

func realRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return output, nil
}
