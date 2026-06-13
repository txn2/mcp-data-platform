package platform

import (
	"context"
	"sync"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

func TestInitUserStore(t *testing.T) {
	t.Run("nil database disables the directory", func(t *testing.T) {
		p := &Platform{}
		p.initUserStore()
		if p.UserStore() != nil {
			t.Error("expected nil user store without a database")
		}
		if p.userDirectory != nil {
			t.Error("expected nil directory without a database")
		}
	})

	t.Run("database enables the directory", func(t *testing.T) {
		db, _, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()

		p := &Platform{db: db}
		p.initUserStore()
		if p.UserStore() == nil {
			t.Error("expected a user store when a database is present")
		}
		if p.userDirectory == nil {
			t.Error("expected a directory when a database is present")
		}
	})
}

func TestDeriveUserName(t *testing.T) {
	t.Run("prefers given/family claims", func(t *testing.T) {
		info := &middleware.UserInfo{
			Name:   "Ignore Me",
			Claims: map[string]any{"given_name": "Marcus", "family_name": "Johnson"},
		}
		first, last := deriveUserName(info)
		if first != "Marcus" || last != "Johnson" {
			t.Errorf("got (%q,%q)", first, last)
		}
	})
	t.Run("falls back to full name", func(t *testing.T) {
		info := &middleware.UserInfo{Name: "Dana Lee"}
		first, last := deriveUserName(info)
		if first != "Dana" || last != "Lee" {
			t.Errorf("got (%q,%q)", first, last)
		}
	})
	t.Run("empty claims and name", func(t *testing.T) {
		first, last := deriveUserName(&middleware.UserInfo{})
		if first != "" || last != "" {
			t.Errorf("got (%q,%q)", first, last)
		}
	})
}

// fakeUserStore captures Observe calls for the wiring test.
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

// TestObserveAuthenticatedUser proves the full wiring: a UserInfo flowing
// through observeAuthenticatedUser is name-derived and lands in the directory
// store — but only for real-person auth types.
func TestObserveAuthenticatedUser(t *testing.T) {
	t.Run("records an OIDC user with derived name", func(t *testing.T) {
		fake := newFakeUserStore()
		p := &Platform{userDirectory: user.NewDirectory(fake)}

		p.observeAuthenticatedUser(&middleware.UserInfo{
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

	t.Run("ignores API key and anonymous auth", func(t *testing.T) {
		for _, at := range []string{"apikey", "noop", ""} {
			fake := newFakeUserStore()
			p := &Platform{userDirectory: user.NewDirectory(fake)}
			p.observeAuthenticatedUser(&middleware.UserInfo{
				Email: "ci@apikey.local", AuthType: at,
			})
			select {
			case <-fake.signal:
				t.Fatalf("auth type %q must not be recorded", at)
			case <-time.After(100 * time.Millisecond):
			}
		}
	})

	t.Run("nil directory is safe", func(_ *testing.T) {
		p := &Platform{}
		p.observeAuthenticatedUser(&middleware.UserInfo{AuthType: "oidc", Email: "a@b.io"})
	})
}

func TestObserveBrowserLogin(t *testing.T) {
	t.Run("records the browser-session user", func(t *testing.T) {
		fake := newFakeUserStore()
		p := &Platform{userDirectory: user.NewDirectory(fake)}

		p.observeBrowserLogin("Dana@Example.com", "Dana", "Lee")

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
		p := &Platform{}
		p.observeBrowserLogin("a@b.io", "A", "B")
	})
}
