package userdir

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/internal/platform/subjects"
	"github.com/txn2/mcp-data-platform/pkg/auth"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
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
		h.ObserveBrowserLogin("a@b.io", "A", "B", "sub-a", nil)                         // no panic
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
	mu        sync.Mutex
	last      [3]string
	lastRoles []string
	signal    chan struct{}
}

func newFakeUserStore() *fakeUserStore {
	return &fakeUserStore{signal: make(chan struct{}, 8)}
}

func (f *fakeUserStore) Observe(_ context.Context, email, first, last string, roles []string) error {
	f.mu.Lock()
	f.last = [3]string{email, first, last}
	f.lastRoles = roles
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

		h.ObserveBrowserLogin("Dana@Example.com", "Dana", "Lee", "sub-dana", []string{"analyst"})

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
		(&Handle{}).ObserveBrowserLogin("a@b.io", "A", "B", "sub-a", nil)
	})
}

// fakeSubjectStore records the pair the book writes.
type fakeSubjectStore struct {
	mu sync.Mutex
	// pairs is every recorded pair; byPerson holds only the ones the person
	// themselves recorded, which is what a bound key resolves through.
	pairs    map[string]string
	byPerson map[string]bool
	signal   chan struct{}
}

func (f *fakeSubjectStore) Record(_ context.Context, address, subject string, fromPerson bool) error {
	f.mu.Lock()
	// A key's pair never overwrites a person's, as the real store's upsert
	// refuses to.
	if fromPerson || !f.byPerson[address] {
		f.pairs[address] = subject
		if f.byPerson == nil {
			f.byPerson = map[string]bool{}
		}
		f.byPerson[address] = fromPerson
	}
	f.mu.Unlock()
	f.signal <- struct{}{}
	return nil
}

func (f *fakeSubjectStore) Lookup(_ context.Context, address string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pairs[address], nil
}

func (f *fakeSubjectStore) LookupPerson(_ context.Context, address string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.byPerson[address] {
		return "", nil
	}
	return f.pairs[address], nil
}

// TestObserveAuthenticated_RecordsTheSubjectForAnAPIKeyToo is #1677: the pair a
// managed-script run resolves its author's library through is recorded for
// every principal a person authenticates as, API keys included, while the
// directory stays people-only.
func TestObserveAuthenticated_RecordsTheSubjectForAnAPIKeyToo(t *testing.T) {
	users := newFakeUserStore()
	pairs := newFakeSubjects(map[string]string{})
	h := handleWith(users)
	h.subjects = subjects.New(pairs)

	h.ObserveAuthenticated(&middleware.UserInfo{
		Email: "admin@example.com", UserID: "apikey:admin", AuthType: middleware.AuthTypeAPIKey,
	})
	select {
	case <-pairs.signal:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the pair to be recorded")
	}
	select {
	case <-users.signal:
		t.Fatal("an API key is nobody to share with and must not enter the directory")
	case <-time.After(100 * time.Millisecond):
	}
	if got := h.Subjects().ForRun(context.Background(), "admin@example.com"); got != "apikey:admin" {
		t.Fatalf("ForRun = %q", got)
	}
}

func TestSubjects_NilSafety(t *testing.T) {
	var h *Handle
	if h.Subjects() != nil {
		t.Error("nil handle Subjects() should be nil")
	}
	h.BindResourceFold(resource.Deps{}) // no panic
	handleWith(newFakeUserStore()).BindResourceFold(resource.Deps{})

	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if New(db).Subjects() == nil {
		t.Error("Subjects() should be non-nil with a database")
	}
}

// getUserStore answers Get from a fixed set of rows, and can fail, which is
// what a bound key is refused on.
type getUserStore struct {
	fakeUserStore
	rows map[string]user.User
	err  error
}

func (g *getUserStore) Get(_ context.Context, email string) (*user.User, error) {
	if g.err != nil {
		return nil, g.err
	}
	row, ok := g.rows[email]
	if !ok {
		return nil, user.ErrNotFound
	}
	return &row, nil
}

// lookupErrStore fails every subject lookup.
type lookupErrStore struct{}

func (lookupErrStore) Record(context.Context, string, string, bool) error { return nil }

func (lookupErrStore) Lookup(context.Context, string) (string, error) {
	return "", errors.New("database unavailable")
}

func (lookupErrStore) LookupPerson(context.Context, string) (string, error) {
	return "", errors.New("database unavailable")
}

// TestBoundPrincipal is #1759: what a key issued against somebody's account
// resolves to, and every way it resolves to nobody.
func TestBoundPrincipal(t *testing.T) {
	newHandle := func(rows map[string]user.User, pairs map[string]string) *Handle {
		h := handleWith(&getUserStore{rows: rows})
		h.subjects = subjects.New(newFakeSubjects(pairs))
		return h
	}
	known := map[string]user.User{
		"dana@example.com": {Email: "dana@example.com", Roles: []string{"dp_analyst"}},
	}
	seen := map[string]string{"dana@example.com": "sub-dana"}

	t.Run("resolves the subject and the roles last recorded", func(t *testing.T) {
		// The address is normalized on the way in, as the directory keys it.
		person, err := newHandle(known, seen).BoundPrincipal(context.Background(), "Dana@Example.com")
		if err != nil {
			t.Fatalf("BoundPrincipal: %v", err)
		}
		if person == nil {
			t.Fatal("resolved nobody for a person the platform has seen sign in")
		}
		if person.Subject != "sub-dana" {
			t.Errorf("Subject = %q, want sub-dana", person.Subject)
		}
		if len(person.Roles) != 1 || person.Roles[0] != "dp_analyst" {
			t.Errorf("Roles = %v, want [dp_analyst]", person.Roles)
		}
	})

	t.Run("resolves nobody the platform cannot speak for", func(t *testing.T) {
		cases := map[string]*Handle{
			// Removing somebody from the directory is how their keys stop working.
			"no directory row":  newHandle(map[string]user.User{}, seen),
			"never signed in":   newHandle(known, map[string]string{}),
			"not an address":    newHandle(known, seen),
			"no handle at all":  nil,
			"no store attached": {},
		}
		addresses := map[string]string{"not an address": "not-an-email"}
		for name, h := range cases {
			address, ok := addresses[name]
			if !ok {
				address = "dana@example.com"
			}
			person, err := h.BoundPrincipal(context.Background(), address)
			if !errors.Is(err, auth.ErrNoBoundPrincipal) {
				t.Errorf("%s: err = %v, want ErrNoBoundPrincipal", name, err)
			}
			if person != nil {
				t.Errorf("%s: resolved %+v, want nobody", name, person)
			}
		}
	})

	t.Run("a read that fails is its own error, never nobody", func(t *testing.T) {
		directoryDown := handleWith(&getUserStore{err: errors.New("database unavailable")})
		directoryDown.subjects = subjects.New(newFakeSubjects(seen))
		_, err := directoryDown.BoundPrincipal(context.Background(), "dana@example.com")
		if err == nil {
			t.Error("a failed directory read resolved without an error")
		}
		if errors.Is(err, auth.ErrNoBoundPrincipal) {
			t.Error("a failed read was reported as there being nobody, which a caller would act on")
		}

		subjectsDown := handleWith(&getUserStore{rows: known})
		subjectsDown.subjects = subjects.New(lookupErrStore{})
		_, err = subjectsDown.BoundPrincipal(context.Background(), "dana@example.com")
		if err == nil {
			t.Error("a failed subject read resolved without an error")
		}
		if errors.Is(err, auth.ErrNoBoundPrincipal) {
			t.Error("a failed read was reported as there being nobody, which a caller would act on")
		}
	})
}

// newFakeSubjects builds a store holding pairs the people themselves recorded,
// which is the state a bound key resolves through.
func newFakeSubjects(pairs map[string]string) *fakeSubjectStore {
	byPerson := make(map[string]bool, len(pairs))
	for address := range pairs {
		byPerson[address] = true
	}
	return &fakeSubjectStore{pairs: pairs, byPerson: byPerson, signal: make(chan struct{}, 8)}
}
