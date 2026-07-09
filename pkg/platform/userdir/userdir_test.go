package userdir

import (
	"context"
	"sync"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

func TestNew(t *testing.T) {
	t.Run("nil database returns a nil handle", func(t *testing.T) {
		h := New(nil)
		if h != nil {
			t.Fatalf("New(nil) = %v, want nil", h)
		}
		// Every accessor and observer must be nil-safe on the nil handle.
		if h.Store() != nil {
			t.Error("nil handle Store() should be nil")
		}
		if h.Directory() != nil {
			t.Error("nil handle Directory() should be nil")
		}
		h.ObserveAuthenticated(&middleware.UserInfo{AuthType: "oidc", Email: "a@b.io"}) // no panic
		h.ObserveBrowserLogin("a@b.io", "A", "B")                                       // no panic
	})

	t.Run("database builds store and directory", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()

		h := New(db)
		if h == nil {
			t.Fatal("New(db) = nil, want a handle")
		}
		if h.Store() == nil {
			t.Error("Store() should be non-nil with a database")
		}
		if h.Directory() == nil {
			t.Error("Directory() should be non-nil with a database")
		}
	})
}

// fakeUserStore captures Observe calls for the wiring tests.
type fakeUserStore struct {
	mu     sync.Mutex
	last   [3]string
	signal chan struct{}
}

func newFakeUserStore() *fakeUserStore {
	return &fakeUserStore{signal: make(chan struct{}, 8)}
}

func (f *fakeUserStore) Observe(_ context.Context, email, first, last string) error {
	f.mu.Lock()
	f.last = [3]string{email, first, last}
	f.mu.Unlock()
	f.signal <- struct{}{}
	return nil
}

func (*fakeUserStore) Insert(context.Context, user.User) error         { return nil }
func (*fakeUserStore) Get(context.Context, string) (*user.User, error) { return nil, user.ErrNotFound }
func (*fakeUserStore) List(context.Context, user.Filter) ([]user.User, int, error) {
	return nil, 0, nil
}
func (*fakeUserStore) Update(context.Context, string, user.Update) error { return nil }
func (*fakeUserStore) Delete(context.Context, string) error              { return nil }

// handleWith builds a Handle over a directory backed by store, bypassing New so
// the Observe behavior can be exercised without a real database.
func handleWith(store user.Store) *Handle {
	return &Handle{store: store, directory: user.NewDirectory(store)}
}

// TestObserveAuthenticated proves the full wiring: a UserInfo flowing through
// ObserveAuthenticated is name-derived and lands in the directory store — but
// only for real-person auth types.
func TestObserveAuthenticated(t *testing.T) {
	t.Run("records an OIDC user with derived name", func(t *testing.T) {
		fake := newFakeUserStore()
		h := handleWith(fake)

		h.ObserveAuthenticated(&middleware.UserInfo{
			Email:    "Marcus.Johnson@Example.com",
			AuthType: "oidc",
			Claims:   map[string]any{"given_name": "Marcus", "family_name": "Johnson"},
		})

		select {
		case <-fake.signal:
		case <-time.After(2 * time.Second):
			t.Fatal("expected a directory write")
		}
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if fake.last != [3]string{"marcus.johnson@example.com", "Marcus", "Johnson"} {
			t.Errorf("unexpected write: %v", fake.last)
		}
	})

	t.Run("records an OAuth user", func(t *testing.T) {
		fake := newFakeUserStore()
		h := handleWith(fake)

		h.ObserveAuthenticated(&middleware.UserInfo{
			Email: "Dana@Example.com", AuthType: "oauth", Name: "Dana Lee",
		})

		select {
		case <-fake.signal:
		case <-time.After(2 * time.Second):
			t.Fatal("expected a directory write")
		}
	})

	t.Run("ignores API key and anonymous auth", func(t *testing.T) {
		for _, at := range []string{"apikey", "noop", ""} {
			fake := newFakeUserStore()
			h := handleWith(fake)
			h.ObserveAuthenticated(&middleware.UserInfo{
				Email: "ci@apikey.local", AuthType: at,
			})
			select {
			case <-fake.signal:
				t.Fatalf("auth type %q must not be recorded", at)
			case <-time.After(100 * time.Millisecond):
			}
		}
	})

	t.Run("nil info and nil directory are safe", func(_ *testing.T) {
		handleWith(newFakeUserStore()).ObserveAuthenticated(nil)
		(&Handle{}).ObserveAuthenticated(&middleware.UserInfo{AuthType: "oidc", Email: "a@b.io"})
	})
}

func TestObserveBrowserLogin(t *testing.T) {
	t.Run("records the browser-session user", func(t *testing.T) {
		fake := newFakeUserStore()
		h := handleWith(fake)

		h.ObserveBrowserLogin("Dana@Example.com", "Dana", "Lee")

		select {
		case <-fake.signal:
		case <-time.After(2 * time.Second):
			t.Fatal("expected a directory write")
		}
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if fake.last != [3]string{"dana@example.com", "Dana", "Lee"} {
			t.Errorf("unexpected write: %v", fake.last)
		}
	})

	t.Run("nil directory is safe", func(_ *testing.T) {
		(&Handle{}).ObserveBrowserLogin("a@b.io", "A", "B")
	})
}
