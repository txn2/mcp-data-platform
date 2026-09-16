package apikeyissue

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/txn2/mcp-data-platform/internal/platform/apikeystore"
	"github.com/txn2/mcp-data-platform/pkg/auth"
)

// fakeManager models the minting contract: a name already held is refused, and
// the in-memory copy is re-read after a write.
type fakeManager struct {
	value     string
	generated []auth.APIKey
	genErr    error
	syncs     int
	syncErr   error
}

func (f *fakeManager) GenerateKey(def auth.APIKey) (string, error) {
	f.generated = append(f.generated, def)
	if f.genErr != nil {
		return "", f.genErr
	}
	if f.value == "" {
		return "generated-value", nil
	}
	return f.value, nil
}

func (f *fakeManager) SyncHashedKeys(context.Context) error {
	f.syncs++
	return f.syncErr
}

// fakeStore models the key store: one row per name, insert-only.
type fakeStore struct {
	created []apikeystore.Definition
	err     error
}

func (f *fakeStore) Create(_ context.Context, def apikeystore.Definition) error {
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, def)
	return nil
}

// fakePrincipals resolves the accounts a test says the platform knows.
type fakePrincipals struct {
	people map[string]auth.BoundPrincipal
	err    error
}

func (f *fakePrincipals) BoundPrincipal(_ context.Context, email string) (*auth.BoundPrincipal, error) {
	if f.err != nil {
		return nil, f.err
	}
	person, ok := f.people[email]
	if !ok {
		return nil, auth.ErrNoBoundPrincipal
	}
	return &person, nil
}

// newIssuer builds an issuer over fresh fakes, knowing the people given.
func newIssuer(people ...auth.BoundPrincipal) (*Issuer, *fakeManager, *fakeStore) {
	manager := &fakeManager{}
	store := &fakeStore{}
	known := map[string]auth.BoundPrincipal{}
	for _, p := range people {
		known[p.Email] = p
	}
	var announced int
	return &Issuer{
		Manager:    manager,
		Store:      store,
		Principals: &fakePrincipals{people: known},
		Announce:   func() { announced++ },
	}, manager, store
}

// TestIssueStoresAServiceKey holds the unbound path: the key carries the roles
// and address it was asked for, and no account.
func TestIssueStoresAServiceKey(t *testing.T) {
	issuer, manager, store := newIssuer()

	issued, err := issuer.Issue(context.Background(), Request{
		Name:      "etl",
		Email:     "etl@example.com",
		Roles:     []string{"service"},
		CreatedBy: "admin@example.com",
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if issued.Person != nil {
		t.Errorf("a key bound to nobody resolved to %+v", issued.Person)
	}
	if len(store.created) != 1 {
		t.Fatalf("stored %d keys, want 1", len(store.created))
	}
	row := store.created[0]
	if row.UserEmail != "" {
		t.Errorf("UserEmail = %q, want empty on a service key", row.UserEmail)
	}
	if row.Email != "etl@example.com" {
		t.Errorf("Email = %q, want etl@example.com", row.Email)
	}
	// The stored row holds a hash, never the value.
	if row.KeyHash == issued.Key {
		t.Fatal("the store holds the key value rather than its hash")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(row.KeyHash), []byte(issued.Key)); err != nil {
		t.Errorf("the stored hash does not match the key handed back: %v", err)
	}
	if manager.syncs != 1 {
		t.Errorf("synced %d times after a write, want 1", manager.syncs)
	}
}

// TestIssueBindsToAnAccount holds that a bound key carries the person's address
// rather than any the requester supplied, so a listing cannot say one thing
// while the session presents another.
func TestIssueBindsToAnAccount(t *testing.T) {
	person := auth.BoundPrincipal{Subject: "sub-42", Email: "analyst@example.com", Roles: []string{"analyst"}}
	issuer, _, store := newIssuer(person)

	issued, err := issuer.Issue(context.Background(), Request{
		Name:      "chatgpt",
		Email:     "somebody.else@example.com",
		UserEmail: "analyst@example.com",
		CreatedBy: "analyst@example.com",
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if issued.Person == nil || issued.Person.Subject != "sub-42" {
		t.Fatalf("resolved person = %+v, want sub-42", issued.Person)
	}
	row := store.created[0]
	if row.UserEmail != "analyst@example.com" {
		t.Errorf("UserEmail = %q, want analyst@example.com", row.UserEmail)
	}
	if row.Email != "analyst@example.com" {
		t.Errorf("Email = %q -- a bound key must carry its person's address", row.Email)
	}
	if len(row.Roles) != 0 {
		t.Errorf("Roles = %v, want none: a key with no override follows its person", row.Roles)
	}
}

// TestIssueKeepsAnOverride holds that roles named on a bound request are stored
// verbatim, which is how an administrator narrows a key.
func TestIssueKeepsAnOverride(t *testing.T) {
	person := auth.BoundPrincipal{Subject: "sub-42", Email: "analyst@example.com", Roles: []string{"analyst", "admin"}}
	issuer, _, store := newIssuer(person)

	if _, err := issuer.Issue(context.Background(), Request{
		Name:      "narrowed",
		UserEmail: "analyst@example.com",
		Roles:     []string{"viewer"},
	}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if got := store.created[0].Roles; len(got) != 1 || got[0] != "viewer" {
		t.Errorf("Roles = %v, want [viewer]", got)
	}
}

// TestIssueRefusesAnUnknownAccount holds that nothing is minted or stored for
// an account the platform cannot speak for.
func TestIssueRefusesAnUnknownAccount(t *testing.T) {
	issuer, manager, store := newIssuer()

	_, err := issuer.Issue(context.Background(), Request{Name: "k", UserEmail: "nobody@example.com"})
	if !errors.Is(err, ErrUnknownPerson) {
		t.Fatalf("err = %v, want ErrUnknownPerson", err)
	}
	if len(manager.generated) != 0 {
		t.Errorf("a key was minted for an account that does not resolve")
	}
	if len(store.created) != 0 {
		t.Errorf("a key was stored for an account that does not resolve")
	}
}

// TestIssueRefusesWhenTheResolverCannotAnswer holds that a read failure is not
// read as "nobody": a key is refused, not issued unresolvable.
func TestIssueRefusesWhenTheResolverCannotAnswer(t *testing.T) {
	issuer, _, store := newIssuer()
	issuer.Principals = &fakePrincipals{err: errors.New("database unavailable")}

	if _, err := issuer.Issue(context.Background(), Request{Name: "k", UserEmail: "analyst@example.com"}); err == nil {
		t.Fatal("a key was issued while the resolver could not answer")
	}
	if len(store.created) != 0 {
		t.Error("a key was stored while the resolver could not answer")
	}
}

// TestIssueRefusesABindingWithNoResolver holds that a deployment that cannot
// resolve accounts refuses to bind rather than issuing a key that will not
// authenticate.
func TestIssueRefusesABindingWithNoResolver(t *testing.T) {
	issuer, _, _ := newIssuer()
	issuer.Principals = nil

	if _, err := issuer.Issue(context.Background(), Request{Name: "k", UserEmail: "analyst@example.com"}); !errors.Is(err, ErrUnknownPerson) {
		t.Fatalf("err = %v, want ErrUnknownPerson", err)
	}
}

// TestIssueReportsATakenName holds that a name already held is answered as that
// rather than as a storage failure, from either the mint or the store.
func TestIssueReportsATakenName(t *testing.T) {
	t.Run("the inventory already holds the name", func(t *testing.T) {
		issuer, manager, store := newIssuer()
		manager.genErr = fmt.Errorf("api key name %q is already held: %w", "etl", auth.ErrKeyNameTaken)

		if _, err := issuer.Issue(context.Background(), Request{Name: "etl", Roles: []string{"service"}}); !errors.Is(err, ErrNameTaken) {
			t.Fatalf("err = %v, want ErrNameTaken", err)
		}
		if len(store.created) != 0 {
			t.Error("a key was stored under a name already held")
		}
	})

	t.Run("another replica stored the name first", func(t *testing.T) {
		issuer, _, store := newIssuer()
		store.err = apikeystore.ErrExists

		if _, err := issuer.Issue(context.Background(), Request{Name: "etl", Roles: []string{"service"}}); !errors.Is(err, ErrNameTaken) {
			t.Fatalf("err = %v, want ErrNameTaken", err)
		}
	})
}

// TestIssueDoesNotBlameTheNameForAnEntropyFailure holds that minting failing
// for a reason that is not the requester's doing is not reported to them as the
// name they chose being taken, which would send them off renaming a key that
// would fail under any name.
func TestIssueDoesNotBlameTheNameForAnEntropyFailure(t *testing.T) {
	issuer, manager, _ := newIssuer()
	manager.genErr = errors.New("generating random key: entropy source unavailable")

	_, err := issuer.Issue(context.Background(), Request{Name: "etl", Roles: []string{"service"}})
	if err == nil {
		t.Fatal("a key was issued while minting failed")
	}
	if errors.Is(err, ErrNameTaken) {
		t.Errorf("err = %v, which reports an entropy failure as a name conflict", err)
	}
}

// TestIssueRefusesWithNowhereToStore holds that a deployment with no key store
// refuses rather than minting a value that would authenticate nowhere.
func TestIssueRefusesWithNowhereToStore(t *testing.T) {
	t.Run("no store", func(t *testing.T) {
		issuer, _, _ := newIssuer()
		issuer.Store = nil
		if _, err := issuer.Issue(context.Background(), Request{Name: "k", Roles: []string{"service"}}); !errors.Is(err, ErrNoStore) {
			t.Fatalf("err = %v, want ErrNoStore", err)
		}
	})

	t.Run("no manager", func(t *testing.T) {
		issuer, _, _ := newIssuer()
		issuer.Manager = nil
		if _, err := issuer.Issue(context.Background(), Request{Name: "k", Roles: []string{"service"}}); !errors.Is(err, ErrNoStore) {
			t.Fatalf("err = %v, want ErrNoStore", err)
		}
	})

	t.Run("no issuer at all", func(t *testing.T) {
		var issuer *Issuer
		if _, err := issuer.Issue(context.Background(), Request{Name: "k"}); !errors.Is(err, ErrNoStore) {
			t.Fatalf("err = %v, want ErrNoStore", err)
		}
	})
}

// TestIssueStoresTheExpiry holds that a lifetime reaches the stored row, so an
// expired key is refused by the authenticator that reads it.
func TestIssueStoresTheExpiry(t *testing.T) {
	issuer, _, store := newIssuer()
	ends := time.Now().Add(24 * time.Hour)

	if _, err := issuer.Issue(context.Background(), Request{
		Name:      "temporary",
		Roles:     []string{"service"},
		ExpiresAt: &ends,
	}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if store.created[0].ExpiresAt == nil || !store.created[0].ExpiresAt.Equal(ends) {
		t.Errorf("ExpiresAt = %v, want %v", store.created[0].ExpiresAt, ends)
	}
}

// TestIssueSucceedsWhenTheCopyCannotBeRefreshed holds that a key is live once
// it is stored: a failed re-read of the in-memory copy does not fail the write,
// because that copy is not what the key authenticates from (#1715).
func TestIssueSucceedsWhenTheCopyCannotBeRefreshed(t *testing.T) {
	issuer, manager, store := newIssuer()
	manager.syncErr = errors.New("store unreachable")

	if _, err := issuer.Issue(context.Background(), Request{Name: "k", Roles: []string{"service"}}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(store.created) != 1 {
		t.Error("the key was not stored")
	}
}

// TestIssueRefusesToSquatTheSelfIssuedNamespace holds that a key bound to
// nobody cannot wear the name the platform composes for a key somebody issued
// for themselves. Such a key would sit in that namespace without the ownership
// that makes it theirs: its apparent owner would not see it in their own list,
// could not revoke it, and would be told the name they chose is already taken.
func TestIssueRefusesToSquatTheSelfIssuedNamespace(t *testing.T) {
	issuer, manager, store := newIssuer()

	_, err := issuer.Issue(context.Background(), Request{
		Name:  SelfIssuedPrefix + "alice@example.com:laptop",
		Roles: []string{"service"},
	})
	if !errors.Is(err, ErrReservedName) {
		t.Fatalf("err = %v, want ErrReservedName", err)
	}
	if len(manager.generated) != 0 || len(store.created) != 0 {
		t.Error("a key was minted or stored in the reserved namespace")
	}
}

// TestIssueAllowsTheNamespaceForABoundKey holds the other side: the namespace
// is reserved for keys that belong to somebody, so one that does may use it.
// This is the path the portal route itself takes.
func TestIssueAllowsTheNamespaceForABoundKey(t *testing.T) {
	person := auth.BoundPrincipal{Subject: "sub-1", Email: "alice@example.com"}
	issuer, _, store := newIssuer(person)

	if _, err := issuer.Issue(context.Background(), Request{
		Name:      SelfIssuedPrefix + "alice@example.com:laptop",
		UserEmail: "alice@example.com",
	}); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if len(store.created) != 1 {
		t.Error("the key was not stored")
	}
}
