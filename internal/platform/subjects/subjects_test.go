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
	mu sync.Mutex
	// pairs is every recorded pair; byPerson marks the ones the person
	// themselves recorded, which is all a bound credential may resolve through.
	pairs    map[string]string
	byPerson map[string]bool
	written  chan struct{}
	fail     error
}

func newFakeStore() *fakeStore {
	return &fakeStore{pairs: map[string]string{}, byPerson: map[string]bool{}, written: make(chan struct{}, 8)}
}

// person marks a pair as one the person themselves recorded, which is the
// state an address reaches by signing in.
func (f *fakeStore) person(address, subject string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pairs[address] = subject
	f.byPerson[address] = true
}

func (f *fakeStore) Record(_ context.Context, address, subject string, fromPerson bool) error {
	if f.fail != nil {
		return f.fail
	}
	f.mu.Lock()
	// A key's pair never overwrites a person's, as the real upsert refuses to.
	if fromPerson || !f.byPerson[address] {
		f.pairs[address] = subject
		f.byPerson[address] = fromPerson
	}
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

func (f *fakeStore) LookupPerson(_ context.Context, address string) (string, error) {
	if f.fail != nil {
		return "", f.fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.byPerson[address] {
		return "", nil
	}
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

// TestSubject_AnswersWithoutFolding is #1759: a caller only asking who somebody
// is must not move their files as a side effect, and a lookup that failed must
// not read as "nobody" -- a credential is refused on the difference.
func TestSubject_AnswersWithoutFolding(t *testing.T) {
	store := newFakeStore()
	store.person("jane@example.com", "sub-1")
	lib := newLibraryStore()
	lib.file("Jane@example.com")
	b := New(store)
	b.BindResources(resource.Deps{Store: lib, URIScheme: "mcp"})

	got, err := b.Subject(context.Background(), "Jane@example.com")
	require.NoError(t, err)
	assert.Equal(t, "sub-1", got)
	assert.Equal(t, "mcp://user/Jane@example.com/qa/rolling.csv", lib.uri(runMade),
		"asking who somebody is must not refile what they own")
}

func TestSubject_UnknownAndUnusableAddresses(t *testing.T) {
	store := newFakeStore()
	b := New(store)

	got, err := b.Subject(context.Background(), "jane@example.com")
	require.NoError(t, err)
	assert.Equal(t, "", got, "somebody the platform has not seen authenticate")

	got, err = b.Subject(context.Background(), "   ")
	require.NoError(t, err)
	assert.Equal(t, "", got)

	var nilBook *Book
	got, err = nilBook.Subject(context.Background(), "jane@example.com")
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestSubject_ALookupThatFailedIsReported(t *testing.T) {
	store := newFakeStore()
	store.fail = errors.New("database away")
	_, err := New(store).Subject(context.Background(), "jane@example.com")
	require.Error(t, err, "a failed read must not be answered as nobody")
}

// TestSubject_AKeyCannotDecideWhoSomebodyIs is #1759's sharpest refusal: a
// service key configured with a person's address must not become the subject
// that address authenticates as.
//
// Without this a key issued against that account would present the key's own
// `apikey:<name>` identity instead of the person's -- the exact inverse of what
// binding a key to an account is for -- and whatever it wrote would land in the
// key's library, readable by everyone holding that key.
func TestSubject_AKeyCannotDecideWhoSomebodyIs(t *testing.T) {
	store := newFakeStore()
	b := New(store)

	// Jane signs in; her own subject is recorded.
	b.Observe(&middleware.UserInfo{
		Email: "jane@example.com", UserID: "sub-jane", AuthType: middleware.AuthTypeOIDC,
	})
	<-store.written

	// A service key carrying her address authenticates. It is recorded (a run
	// acting for a key-only author still needs the pair, #1677) but it does not
	// take her address over.
	b.Observe(&middleware.UserInfo{
		Email: "jane@example.com", UserID: "apikey:ci", AuthType: middleware.AuthTypeAPIKey,
	})
	<-store.written

	got, err := b.Subject(context.Background(), "jane@example.com")
	require.NoError(t, err)
	assert.Equal(t, "sub-jane", got, "a service key overwrote the subject a person authenticates as")
}

// TestSubject_AKeyWrittenPairIsNotAPerson holds the other half: an address the
// platform has only ever seen a key authenticate at resolves to nobody, so a
// credential is refused rather than authenticating as a key identity.
func TestSubject_AKeyWrittenPairIsNotAPerson(t *testing.T) {
	store := newFakeStore()
	b := New(store)

	b.Observe(&middleware.UserInfo{
		Email: "jane@example.com", UserID: "apikey:ci", AuthType: middleware.AuthTypeAPIKey,
	})
	<-store.written

	got, err := b.Subject(context.Background(), "jane@example.com")
	require.NoError(t, err)
	assert.Equal(t, "", got, "a pair only a key wrote must not answer for the person")

	// The run path still reads it: a script whose author authenticates only by
	// key files where that author's sessions file (#1677).
	assert.Equal(t, "apikey:ci", b.ForRun(context.Background(), "jane@example.com"))
}

// TestSubject_ABoundKeyRecordsThePersonsOwnSubject holds that a key issued
// against an account is the person for this purpose: it presents their subject,
// so recording from it keeps the pair correct rather than corrupting it.
func TestSubject_ABoundKeyRecordsThePersonsOwnSubject(t *testing.T) {
	store := newFakeStore()
	b := New(store)

	b.Observe(&middleware.UserInfo{
		Email: "jane@example.com", UserID: "sub-jane", AuthType: middleware.AuthTypeAPIKey,
	})
	<-store.written

	got, err := b.Subject(context.Background(), "jane@example.com")
	require.NoError(t, err)
	assert.Equal(t, "sub-jane", got)
}
