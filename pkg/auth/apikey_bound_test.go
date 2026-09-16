package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePrincipals models the resolver contract: an address the platform can
// speak for resolves to a subject, an address it cannot resolves to nobody
// without an error, and a read can fail. It counts lookups so a test can tell
// whether the resolver was consulted at all.
type fakePrincipals struct {
	mu     sync.Mutex
	people map[string]BoundPrincipal
	err    error
	looks  int
}

func newFakePrincipals() *fakePrincipals {
	return &fakePrincipals{people: map[string]BoundPrincipal{}}
}

func (f *fakePrincipals) put(p BoundPrincipal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.people[p.Email] = p
}

func (f *fakePrincipals) lookups() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.looks
}

func (f *fakePrincipals) BoundPrincipal(_ context.Context, email string) (*BoundPrincipal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.looks++
	if f.err != nil {
		return nil, f.err
	}
	person, ok := f.people[email]
	if !ok {
		return nil, ErrNoBoundPrincipal
	}
	return &person, nil
}

// boundSetup returns an authenticator with a key store and a principal
// resolver attached, plus both fakes.
func boundSetup(t *testing.T) (*APIKeyAuthenticator, *fakeKeyStore, *fakePrincipals) {
	t.Helper()
	a, store := storeBacked(t)
	people := newFakePrincipals()
	a.SetPrincipalSource(people)
	return a, store, people
}

// TestBoundKeyAuthenticatesAsThePerson holds the point of the whole feature: a
// key issued against somebody's account presents their subject, not the key's.
func TestBoundKeyAuthenticatesAsThePerson(t *testing.T) {
	a, store, people := boundSetup(t)
	people.put(BoundPrincipal{Subject: "sub-42", Email: "analyst@example.com", Roles: []string{"analyst"}})

	value, row := storedKey(t, a, "chatgpt")
	row.UserEmail = "analyst@example.com"
	store.put(row)

	got, err := authenticate(a, value)
	if err != nil {
		t.Fatalf("authenticating a bound key: %v", err)
	}
	if got.UserID != "sub-42" {
		t.Errorf("UserID = %q, want sub-42 -- a bound key must not present a key identity", got.UserID)
	}
	if strings.Join(got.Roles, ",") != "analyst" {
		t.Errorf("Roles = %v, want [analyst]", got.Roles)
	}
}

// TestBoundKeyFollowsThePersonsCurrentRoles holds that a key with no roles of
// its own carries what the person holds now, not what they held when it was
// made: a role their provider has stopped granting stops reaching the key.
func TestBoundKeyFollowsThePersonsCurrentRoles(t *testing.T) {
	a, store, people := boundSetup(t)
	people.put(BoundPrincipal{Subject: "sub-42", Email: "analyst@example.com", Roles: []string{"analyst", "admin"}})

	value, row := storedKey(t, a, "chatgpt")
	row.UserEmail = "analyst@example.com"
	store.put(row)

	if got, err := authenticate(a, value); err != nil || strings.Join(got.Roles, ",") != "analyst,admin" {
		t.Fatalf("before the change: roles %v err %v", got, err)
	}

	// The provider stops granting admin; the next sign-in records that.
	people.put(BoundPrincipal{Subject: "sub-42", Email: "analyst@example.com", Roles: []string{"analyst"}})

	got, err := authenticate(a, value)
	if err != nil {
		t.Fatalf("authenticating after the role change: %v", err)
	}
	if strings.Join(got.Roles, ",") != "analyst" {
		t.Errorf("Roles = %v, want [analyst] -- the key kept a role the provider revoked", got.Roles)
	}
}

// TestBoundKeyRolesNarrowButDoNotFollow holds the administrator's override: a
// bound key carrying roles of its own uses exactly those, and is still the
// person.
func TestBoundKeyRolesNarrowButDoNotFollow(t *testing.T) {
	a, store, people := boundSetup(t)
	people.put(BoundPrincipal{Subject: "sub-42", Email: "analyst@example.com", Roles: []string{"analyst", "admin"}})

	value, row := storedKey(t, a, "narrowed", "viewer")
	row.UserEmail = "analyst@example.com"
	store.put(row)

	got, err := authenticate(a, value)
	if err != nil {
		t.Fatalf("authenticating a narrowed key: %v", err)
	}
	if got.UserID != "sub-42" {
		t.Errorf("UserID = %q, want sub-42", got.UserID)
	}
	if strings.Join(got.Roles, ",") != "viewer" {
		t.Errorf("Roles = %v, want [viewer] -- an override must not be widened by the person's roles", got.Roles)
	}
}

// TestBoundKeyIsRefusedWhenTheAccountCannotBeResolved holds that every way of
// failing to resolve the account refuses the key rather than falling back to a
// key identity. Falling back would hand the caller a second principal at the
// moment the platform lost track of the first.
func TestBoundKeyIsRefusedWhenTheAccountCannotBeResolved(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(*APIKeyAuthenticator, *fakePrincipals)
	}{
		{
			name:    "the platform has no record of the person",
			arrange: func(_ *APIKeyAuthenticator, _ *fakePrincipals) {},
		},
		{
			name: "the record has no subject to present",
			arrange: func(_ *APIKeyAuthenticator, people *fakePrincipals) {
				people.put(BoundPrincipal{Email: "analyst@example.com", Roles: []string{"analyst"}})
			},
		},
		{
			name: "the resolver could not answer",
			arrange: func(_ *APIKeyAuthenticator, people *fakePrincipals) {
				people.err = errors.New("database unavailable")
			},
		},
		{
			name: "this deployment resolves no accounts",
			arrange: func(a *APIKeyAuthenticator, _ *fakePrincipals) {
				a.SetPrincipalSource(nil)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, store, people := boundSetup(t)
			value, row := storedKey(t, a, "chatgpt")
			row.UserEmail = "analyst@example.com"
			store.put(row)
			tt.arrange(a, people)

			got, err := authenticate(a, value)
			if err == nil {
				t.Fatalf("a bound key authenticated as %q when its account could not be resolved", got.UserID)
			}
		})
	}
}

// TestServiceKeyIsUnchangedByTheBindingPath holds that a key bound to nobody is
// the standalone identity it has always been, and that no resolver is consulted
// for it.
func TestServiceKeyIsUnchangedByTheBindingPath(t *testing.T) {
	a, store, people := boundSetup(t)
	value, row := storedKey(t, a, "etl", "service")
	store.put(row)

	got, err := authenticate(a, value)
	if err != nil {
		t.Fatalf("authenticating a service key: %v", err)
	}
	if got.UserID != "apikey:etl" {
		t.Errorf("UserID = %q, want apikey:etl", got.UserID)
	}
	if people.lookups() != 0 {
		t.Errorf("the resolver was consulted %d times for a key bound to nobody", people.lookups())
	}
}

// TestBoundKeyExpiryIsCheckedBeforeTheAccount holds that an expired key is
// refused as expired, without a lookup: the account is irrelevant to a key that
// has ended.
func TestBoundKeyExpiryIsCheckedBeforeTheAccount(t *testing.T) {
	a, store, people := boundSetup(t)
	people.put(BoundPrincipal{Subject: "sub-42", Email: "analyst@example.com"})

	past := time.Now().Add(-time.Hour)
	value, row := storedKey(t, a, "stale")
	row.UserEmail = "analyst@example.com"
	row.ExpiresAt = &past
	store.put(row)

	if _, err := authenticate(a, value); err == nil {
		t.Fatal("an expired bound key authenticated")
	} else if !strings.Contains(err.Error(), "expired") {
		t.Errorf("refusal = %q, want it to name the expiry", err)
	}
	if people.lookups() != 0 {
		t.Errorf("the resolver was consulted %d times for an expired key", people.lookups())
	}
}

// TestPrincipalsReportsTheAttachedResolver holds that the route which issues a
// bound key can read the very resolver the authenticator will use, so the check
// at creation and the behavior at authentication cannot drift apart.
func TestPrincipalsReportsTheAttachedResolver(t *testing.T) {
	var nilAuthenticator *APIKeyAuthenticator
	if nilAuthenticator.Principals() != nil {
		t.Error("a nil authenticator reported a resolver")
	}

	a := NewAPIKeyAuthenticator(APIKeyConfig{})
	if a.Principals() != nil {
		t.Error("an authenticator with no resolver attached reported one")
	}

	people := newFakePrincipals()
	a.SetPrincipalSource(people)
	if a.Principals() != people {
		t.Error("Principals did not report the attached resolver")
	}
}

// TestListKeysCarriesTheAccount holds that a key listing says whose a key is,
// which is how an administrator sees and revokes a key somebody issued for
// themselves.
func TestListKeysCarriesTheAccount(t *testing.T) {
	a, store, _ := boundSetup(t)
	_, row := storedKey(t, a, "user:analyst@example.com:chatgpt")
	row.UserEmail = "analyst@example.com"
	store.put(row)
	if err := a.SyncHashedKeys(context.Background()); err != nil {
		t.Fatalf("SyncHashedKeys: %v", err)
	}

	var found bool
	for _, k := range a.ListKeys() {
		if k.Name == "user:analyst@example.com:chatgpt" {
			found = true
			if k.UserEmail != "analyst@example.com" {
				t.Errorf("UserEmail = %q, want analyst@example.com", k.UserEmail)
			}
		}
	}
	if !found {
		t.Fatal("the listing did not hold the key")
	}
}
