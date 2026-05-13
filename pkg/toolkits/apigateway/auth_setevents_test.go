package apigateway

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
)

// TestOAuth2AuthSetAuthEventsRaceFree exercises both setters with
// the sync.RWMutex in place. Coverage target: SetAuthEvents +
// snapshot path that Apply / refresh / handleRevoked use.
func TestOAuth2AuthSetAuthEventsRaceFree(t *testing.T) {
	t.Parallel()
	a := newOAuth2AuthorizationCodeAuth(Config{})
	store := newFakeTokenStore()
	writer := authevents.NewWriter(authevents.NewMemoryStore(), nil)

	a.SetTokenStore(store)
	a.SetAuthEvents(writer)

	gotStore, gotEvents := a.snapshot()
	if gotStore == nil {
		t.Errorf("snapshot store should not be nil")
	}
	if gotEvents != writer {
		t.Errorf("snapshot events mismatch")
	}
	// Reset to nil — verify nil-safe path through snapshot.
	a.SetTokenStore(nil)
	a.SetAuthEvents(nil)
	s2, e2 := a.snapshot()
	if s2 != nil || e2 != nil {
		t.Errorf("snapshot after nil-set should be (nil, nil); got (%v, %v)", s2, e2)
	}
}

// TestToolkitSetAuthEventsPropagates exercises the Toolkit-level
// SetAuthEvents loop that threads the writer into already-
// materialized authorization_code authenticators.
func TestToolkitSetAuthEventsPropagates(t *testing.T) {
	t.Parallel()
	tk := New("test")
	writer := authevents.NewWriter(authevents.NewMemoryStore(), nil)
	tk.SetAuthEvents(writer)
	if tk.authEvents != writer {
		t.Error("SetAuthEvents should set the toolkit's writer field")
	}
}

type fakeTokenStore struct{}

func newFakeTokenStore() *fakeTokenStore { return &fakeTokenStore{} }

func (*fakeTokenStore) Get(_ context.Context, _ string) (*PersistedToken, error) {
	return nil, ErrTokenNotFound
}

func (*fakeTokenStore) Set(_ context.Context, _ PersistedToken) error {
	return nil
}

func (*fakeTokenStore) Delete(_ context.Context, _ string) error {
	return nil
}
