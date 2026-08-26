package prstate

import (
	"context"
	"errors"
	"testing"
	"time"
)

func fixedNow() time.Time { return time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC) }

type fakeResolver struct {
	host   string
	state  PRState
	err    error
	calls  int
}

func (f *fakeResolver) Host() string { return f.host }
func (f *fakeResolver) Fetch(context.Context, string) (PRState, error) {
	f.calls++
	return f.state, f.err
}

func newTestService(t *testing.T, resolver Resolver) *Service {
	t.Helper()
	service := NewService(t.TempDir(), []Resolver{resolver})
	service.now = fixedNow
	return service
}

func TestGetFetchesCachesAndRespectsTTL(t *testing.T) {
	resolver := &fakeResolver{host: "github.com", state: PRState{State: StateOpen, Checks: ChecksPass}}
	service := newTestService(t, resolver)
	url := "https://github.com/acme/api/pull/7"

	first := service.Get(context.Background(), url, false)
	if first.State != StateOpen || first.Host != "github.com" || resolver.calls != 1 {
		t.Fatalf("first = %+v calls %d", first, resolver.calls)
	}
	// Fresh cache: no second exec.
	if second := service.Get(context.Background(), url, false); second.State != StateOpen || resolver.calls != 1 {
		t.Fatalf("cache miss on fresh state: calls %d", resolver.calls)
	}
	// Expired cache refetches.
	service.now = func() time.Time { return fixedNow().Add(freshFor + time.Second) }
	if _, _ = service.Get(context.Background(), url, false), 0; resolver.calls != 2 {
		t.Fatalf("expired cache did not refetch: calls %d", resolver.calls)
	}
}

func TestOfflineUsesStaleCacheAndNeverExecs(t *testing.T) {
	resolver := &fakeResolver{host: "github.com", state: PRState{State: StateMerged}}
	service := newTestService(t, resolver)
	url := "https://github.com/acme/api/pull/7"
	service.Get(context.Background(), url, false)
	service.now = func() time.Time { return fixedNow().Add(24 * time.Hour) }
	stale := service.Get(context.Background(), url, true)
	if stale.State != StateMerged || resolver.calls != 1 {
		t.Fatalf("offline = %+v calls %d", stale, resolver.calls)
	}
	// Offline with no cache: unknown with a hint, no exec.
	cold := service.Get(context.Background(), "https://github.com/acme/api/pull/8", true)
	if cold.State != StateUnknown || cold.Hint == "" || resolver.calls != 1 {
		t.Fatalf("offline cold = %+v calls %d", cold, resolver.calls)
	}
}

func TestFailuresDegradeToUnknownWithNegativeTTL(t *testing.T) {
	resolver := &fakeResolver{host: "github.com", err: errors.New("gh exploded")}
	service := newTestService(t, resolver)
	url := "https://github.com/acme/api/pull/7"
	state := service.Get(context.Background(), url, false)
	if state.State != StateUnknown || state.Hint != "gh exploded" {
		t.Fatalf("failure state = %+v", state)
	}
	// Within the negative TTL the unknown is served from cache.
	service.now = func() time.Time { return fixedNow().Add(unknownFreshFor - time.Second) }
	service.Get(context.Background(), url, false)
	if resolver.calls != 1 {
		t.Fatalf("negative TTL refetched: calls %d", resolver.calls)
	}
	// After it, a refetch happens.
	service.now = func() time.Time { return fixedNow().Add(unknownFreshFor + time.Second) }
	service.Get(context.Background(), url, false)
	if resolver.calls != 2 {
		t.Fatalf("unknown never refetched: calls %d", resolver.calls)
	}
}

func TestUnknownHostAndGetAll(t *testing.T) {
	resolver := &fakeResolver{host: "github.com", state: PRState{State: StateOpen}}
	service := newTestService(t, resolver)
	odd := service.Get(context.Background(), "https://forge.example/pr/1", false)
	if odd.State != StateUnknown || odd.Hint == "" {
		t.Fatalf("unknown host = %+v", odd)
	}
	all := service.GetAll(context.Background(), []string{
		"https://github.com/acme/api/pull/1",
		"https://github.com/acme/api/pull/2",
		"https://github.com/acme/api/pull/1",
	}, false)
	if len(all) != 2 || resolver.calls != 2 {
		t.Fatalf("GetAll = %d results, %d calls", len(all), resolver.calls)
	}
}
