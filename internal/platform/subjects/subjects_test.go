package subjects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// fakeStore records pairs in memory and signals each write.
type fakeStore struct {
	mu      sync.Mutex
	pairs   map[string]string
	written chan struct{}
	fail    error
}

func newFakeStore() *fakeStore {
	return &fakeStore{pairs: map[string]string{}, written: make(chan struct{}, 8)}
}

func (f *fakeStore) Record(_ context.Context, address, subject string) error {
	if f.fail != nil {
		return f.fail
	}
	f.mu.Lock()
	f.pairs[address] = subject
	f.mu.Unlock()
	f.written <- struct{}{}
	return nil
}

func (f *fakeStore) Lookup(_ context.Context, address string) (string, error) {
	if f.fail != nil {
		return "", f.fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pairs[address], nil
}

// libraryStore is the slice of resource.Store a fold touches: a listing by
// library, a lookup by address, and the move. Every other method is the
// embedded nil interface, so a fold that reached for one would panic here.
type libraryStore struct {
	resource.Store
	mu    sync.Mutex
	rows  map[string]*resource.Resource
	moved chan struct{}
}

func newLibraryStore() *libraryStore {
	return &libraryStore{rows: map[string]*resource.Resource{}, moved: make(chan struct{}, 8)}
}

// runMade is the one row every fold test starts from: a file a run filed by
// address before the platform knew the subject.
const runMade = "run-made"

func (s *libraryStore) file(scopeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[runMade] = &resource.Resource{
		ID: runMade, Scope: resource.ScopeUser, ScopeID: scopeID, Path: "qa", Filename: "rolling.csv",
		URI: resource.BuildURI("mcp", resource.ScopeUser, scopeID, "qa", "rolling.csv"),
	}
}

func (s *libraryStore) List(_ context.Context, f resource.Filter) ([]resource.Resource, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []resource.Resource
	for _, r := range s.rows {
		for _, sf := range f.Scopes {
			if sf.Scope == r.Scope && sf.ScopeID == r.ScopeID {
				out = append(out, *r)
			}
		}
	}
	return out, len(out), nil
}

func (s *libraryStore) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rows {
		if r.URI == uri {
			return r, nil
		}
	}
	return nil, fmt.Errorf("not found: %w", sql.ErrNoRows)
}

func (s *libraryStore) Move(_ context.Context, moves []resource.Move) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range moves {
		r := s.rows[m.ID]
		r.Scope, r.ScopeID, r.Path, r.URI = m.Scope, m.ScopeID, m.Path, m.URI
	}
	s.moved <- struct{}{}
	return nil
}

func (s *libraryStore) uri(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rows[id].URI
}

func person(authType, email, sub string) *middleware.UserInfo {
	return &middleware.UserInfo{AuthType: authType, Email: email, UserID: sub}
}

func awaitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected %s", what)
	}
}

func TestObserve_RecordsThePairForEveryKindOfPerson(t *testing.T) {
	for _, at := range []string{middleware.AuthTypeOIDC, middleware.AuthTypeOAuth, middleware.AuthTypeAPIKey} {
		t.Run(at, func(t *testing.T) {
			store := newFakeStore()
			b := New(store)
			b.Observe(person(at, " Jane.Doe@Example.com ", "sub-"+at))
			awaitSignal(t, store.written, "a recorded pair")
			store.mu.Lock()
			defer store.mu.Unlock()
			assert.Equal(t, "sub-"+at, store.pairs["jane.doe@example.com"],
				"keyed by the normalized address, as the users directory keys the same person")
		})
	}
}

func TestObserve_RecordsNothingForWhatIsNotAPerson(t *testing.T) {
	tests := []struct {
		name string
		info *middleware.UserInfo
	}{
		{"a script run", person(middleware.AuthTypeScript, "author@example.com", "script:daily")},
		{"an anonymous session", person(middleware.AuthTypeAnonymous, "anon@example.com", "anonymous")},
		{"auth disabled", person(middleware.AuthTypeNoop, "noop@example.com", "noop")},
		{"no address", person(middleware.AuthTypeOIDC, "", "sub-1")},
		{"no subject", person(middleware.AuthTypeOIDC, "jane@example.com", "")},
		{"nil", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			New(store).Observe(tc.info)
			select {
			case <-store.written:
				t.Fatal("recorded a pair it must not")
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
	t.Run("a nil book", func(_ *testing.T) {
		var b *Book
		b.Observe(person(middleware.AuthTypeOIDC, "jane@example.com", "sub-1"))
		b.BindResources(resource.Deps{})
	})
}

func TestObserve_IsThrottledPerPair(t *testing.T) {
	store := newFakeStore()
	b := New(store)
	b.Observe(person(middleware.AuthTypeOIDC, "jane@example.com", "sub-1"))
	awaitSignal(t, store.written, "the first write")
	b.Observe(person(middleware.AuthTypeOIDC, "jane@example.com", "sub-1"))
	select {
	case <-store.written:
		t.Fatal("the same pair was written twice inside the window")
	case <-time.After(50 * time.Millisecond):
	}
	// A different subject for the same address is a different pair: an
	// identity-provider migration is followed on the next authentication.
	b.Observe(person(middleware.AuthTypeOIDC, "jane@example.com", "sub-2"))
	awaitSignal(t, store.written, "a write for the new subject")
	store.mu.Lock()
	defer store.mu.Unlock()
	assert.Equal(t, "sub-2", store.pairs["jane@example.com"])
}

func TestObserve_PrunesTheThrottleWhenItGrows(t *testing.T) {
	b := New(newFakeStore())
	b.ttl = time.Nanosecond
	for i := range maxSeenEntries + 1 {
		b.shouldWrite(fmt.Sprintf("k-%d", i))
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	assert.LessOrEqual(t, len(b.seen), maxSeenEntries, "expired entries are pruned once the map fills")
}

func TestObserve_FoldsTheAddressKeyedLibraryOnceThePairIsKnown(t *testing.T) {
	store := newFakeStore()
	lib := newLibraryStore()
	lib.file("jane@example.com")
	b := New(store)
	b.BindResources(resource.Deps{Store: lib, URIScheme: "mcp"})

	b.Observe(person(middleware.AuthTypeOIDC, "jane@example.com", "sub-1"))
	awaitSignal(t, store.written, "the recorded pair")
	awaitSignal(t, lib.moved, "the fold")
	assert.Equal(t, "mcp://user/sub-1/qa/rolling.csv", lib.uri(runMade))
}

func TestObserve_ARecordThatFailedFoldsNothing(t *testing.T) {
	store := newFakeStore()
	store.fail = errors.New("database away")
	lib := newLibraryStore()
	lib.file("jane@example.com")
	b := New(store)
	b.BindResources(resource.Deps{Store: lib, URIScheme: "mcp"})
	b.Observe(person(middleware.AuthTypeOIDC, "jane@example.com", "sub-1"))
	select {
	case <-lib.moved:
		t.Fatal("folded on a pair that was not recorded")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestForRun_AnswersTheSubjectAndFoldsFirst(t *testing.T) {
	store := newFakeStore()
	store.pairs["jane@example.com"] = "sub-1"
	lib := newLibraryStore()
	lib.file("Jane@example.com")
	b := New(store)
	b.BindResources(resource.Deps{Store: lib, URIScheme: "mcp"})

	// The address is looked up normalized and folded verbatim: the rows a run
	// filed are keyed by the version author exactly as recorded.
	got := b.ForRun(context.Background(), "Jane@example.com")
	assert.Equal(t, "sub-1", got)
	assert.Equal(t, "mcp://user/sub-1/qa/rolling.csv", lib.uri(runMade))
}

func TestForRun_AnUnseenAuthorStaysKeyedByAddress(t *testing.T) {
	store := newFakeStore()
	lib := newLibraryStore()
	lib.file("jane@example.com")
	b := New(store)
	b.BindResources(resource.Deps{Store: lib, URIScheme: "mcp"})

	assert.Equal(t, "", b.ForRun(context.Background(), "jane@example.com"))
	assert.Equal(t, "mcp://user/jane@example.com/qa/rolling.csv", lib.uri(runMade), "nothing to fold into")
	assert.Equal(t, "", b.ForRun(context.Background(), "   "))
}

func TestForRun_ALookupThatFailedIsUnknown(t *testing.T) {
	store := newFakeStore()
	store.fail = errors.New("database away")
	assert.Equal(t, "", New(store).ForRun(context.Background(), "jane@example.com"))
	var nilBook *Book
	assert.Equal(t, "", nilBook.ForRun(context.Background(), "jane@example.com"))
}

func TestForRun_FoldsNothingWithoutALibraryBound(t *testing.T) {
	store := newFakeStore()
	store.pairs["jane@example.com"] = "sub-1"
	b := New(store)
	b.BindResources(resource.Deps{}) // no store: unbound
	assert.Equal(t, "sub-1", b.ForRun(context.Background(), "jane@example.com"))
}

func TestFoldInto_ReportsAFoldThatFailedAndGoesOn(t *testing.T) {
	store := newFakeStore()
	store.pairs["jane@example.com"] = "sub-1"
	b := New(store)
	b.BindResources(resource.Deps{Store: brokenLibrary{}, URIScheme: "mcp"})
	assert.Equal(t, "sub-1", b.ForRun(context.Background(), "jane@example.com"),
		"the subject is still the answer; the fold is retried on the next occasion")
}

func TestNew_NeedsAStore(t *testing.T) {
	require.Nil(t, New(nil))
}

type brokenLibrary struct{ resource.Store }

func (brokenLibrary) List(context.Context, resource.Filter) ([]resource.Resource, int, error) {
	return nil, 0, errors.New("postgres is unreachable")
}
