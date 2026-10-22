package extaccount

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// failingStore is memStore with one operation made to fail.
type failingStore struct {
	*memStore
	fail string
}

var errStore = errors.New("store down")

func (f *failingStore) Insert(ctx context.Context, a Account) error {
	if f.fail == "insert" {
		return errStore
	}
	return f.memStore.Insert(ctx, a)
}

func (f *failingStore) Get(ctx context.Context, id string) (*Account, error) {
	if f.fail == "get" {
		return nil, errStore
	}
	return f.memStore.Get(ctx, id)
}

func (f *failingStore) Delete(ctx context.Context, id string) error {
	if f.fail == "delete" {
		return errStore
	}
	return f.memStore.Delete(ctx, id)
}

func (f *failingStore) InsertLink(ctx context.Context, hash, id string, expires time.Time) error {
	if f.fail == "link" {
		return errStore
	}
	return f.memStore.InsertLink(ctx, hash, id, expires)
}

func (f *failingStore) CountLinksSince(ctx context.Context, id string, since time.Time) (int, error) {
	if f.fail == "count" {
		return 0, errStore
	}
	return f.memStore.CountLinksSince(ctx, id, since)
}

func (f *failingStore) ClaimLink(ctx context.Context, hash string, now time.Time) (accountID string, claimed bool, err error) {
	if f.fail == "claim" {
		return "", false, errStore
	}
	return f.memStore.ClaimLink(ctx, hash, now)
}

// failing rebuilds the fixture's service over a store that fails at one
// operation, keeping everything else.
func (f *fixture) failing(fail string, change func(*Config)) {
	cfg := f.svc.cfg
	cfg.Store = &failingStore{memStore: f.store, fail: fail}
	if change != nil {
		change(&cfg)
	}
	f.svc = New(cfg)
}

// A store or a check that fails stops the account being created; an
// invitation that cannot be sent leaves the account, as an administrator can
// ask for another.
func TestCreateWhenSomethingFails(t *testing.T) {
	ctx := context.Background()
	in := NewAccount{Email: "client@example.org", Persona: "client"}

	f := newFixture(t)
	f.failing("", func(c *Config) {
		c.KnownInternal = func(context.Context, string) (bool, error) { return false, errStore }
	})
	_, err := f.svc.Create(ctx, in, "admin")
	assert.ErrorIs(t, err, errStore)

	f = newFixture(t)
	f.failing("insert", nil)
	_, err = f.svc.Create(ctx, in, "admin")
	assert.ErrorIs(t, err, errStore)

	for name, change := range map[string]func(*Config){
		"the link is not stored": nil,
		"no mail delivery":       func(c *Config) { c.Mail = nil },
	} {
		f = newFixture(t)
		fail := "link"
		if change != nil {
			fail = ""
		}
		f.failing(fail, change)
		a, err := f.svc.Create(ctx, in, "admin")
		require.NoError(t, err, name)
		assert.NotEmpty(t, a.ID, name)
	}
}

// A sign-in link is sent only to an active account under its hourly limit,
// and nothing about the address is reported either way.
func TestRequestLinkSendsOnlyWhatItShould(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	_, err := f.svc.Create(ctx, NewAccount{Email: "client@example.org", Persona: "client"}, "admin")
	require.NoError(t, err)
	sentBefore := len(f.mail)

	f.svc.RequestLink(ctx, "nobody@example.org")
	assert.Len(t, f.mail, sentBefore, "no account, no mail")

	for range maxLinksPerHour {
		f.svc.RequestLink(ctx, " client@example.org ")
	}
	assert.Len(t, f.mail, maxLinksPerHour, "the invitation and four links reach the hourly limit")
	f.svc.RequestLink(ctx, "client@example.org")
	assert.Len(t, f.mail, maxLinksPerHour, "past the limit, nothing is sent")

	f = newFixture(t)
	a, err := f.svc.Create(ctx, NewAccount{Email: "client@example.org", Persona: "client"}, "admin")
	require.NoError(t, err)
	f.failing("count", nil)
	f.svc.RequestLink(ctx, "client@example.org")
	assert.Len(t, f.mail, 1, "a count that fails sends nothing")
	f.failing("link", nil)
	f.svc.RequestLink(ctx, "client@example.org")
	assert.Len(t, f.mail, 1, "a link that is not stored is not sent")

	f.failing("", nil)
	require.NoError(t, f.svc.Suspend(ctx, a.ID))
	f.svc.RequestLink(ctx, "client@example.org")
	assert.Len(t, f.mail, 1, "a suspended account is sent nothing")
}

// A link whose claim fails, whose account was suspended, or whose persona is
// no longer external opens no session.
func TestClaimRefusals(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a, err := f.svc.Create(ctx, NewAccount{Email: "client@example.org", Persona: "client"}, "admin")
	require.NoError(t, err)
	token := tokenOf(t, f.mail[0].link)

	f.failing("claim", nil)
	_, err = f.svc.Claim(ctx, httptest.NewRecorder(), token)
	assert.ErrorIs(t, err, errStore)

	f.failing("", nil)
	require.NoError(t, f.svc.Suspend(ctx, a.ID))
	_, err = f.svc.Claim(ctx, httptest.NewRecorder(), token)
	assert.ErrorIs(t, err, ErrLink)

	require.NoError(t, f.svc.Resume(ctx, a.ID))
	f.svc.RequestLink(ctx, "client@example.org")
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "client", Roles: []string{"client"}}))
	f.failing("", func(c *Config) { c.Personas = reg })
	_, err = f.svc.Claim(ctx, httptest.NewRecorder(), tokenOf(t, f.mail[len(f.mail)-1].link))
	assert.ErrorIs(t, err, ErrLink, "a persona no longer external")
}

// Removing an account that cannot be read, or whose row cannot be deleted,
// fails; one whose shares cannot be revoked says so.
func TestRemoveWhenSomethingFails(t *testing.T) {
	ctx := context.Background()
	for _, fail := range []string{"get", "delete"} {
		f := newFixture(t)
		a, err := f.svc.Create(ctx, NewAccount{Email: "client@example.org", Persona: "client"}, "admin")
		require.NoError(t, err)
		f.failing(fail, nil)
		assert.ErrorIs(t, f.svc.Remove(ctx, a.ID), errStore, fail)
	}
	f := newFixture(t)
	a, err := f.svc.Create(ctx, NewAccount{Email: "client@example.org", Persona: "client"}, "admin")
	require.NoError(t, err)
	f.failing("", func(c *Config) { c.RevokeShares = func(context.Context, string) error { return errStore } })
	assert.ErrorIs(t, f.svc.Remove(ctx, a.ID), errStore)
}

// A session is admitted with its persona's roles while the account is
// active; one whose account cannot be read, or whose persona is gone, is not.
func TestAdmitWhenSomethingFails(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a, err := f.svc.Create(ctx, NewAccount{Email: "client@example.org", Persona: "client"}, "admin")
	require.NoError(t, err)

	roles, ok := f.svc.Admit(ctx, "someone-else", []string{"analyst"})
	assert.True(t, ok, "not an external session")
	assert.Equal(t, []string{"analyst"}, roles)

	f.failing("get", nil)
	_, ok = f.svc.Admit(ctx, a.UserID(), nil)
	assert.False(t, ok)

	f.failing("", func(c *Config) { c.Personas = nil })
	_, ok = f.svc.Admit(ctx, a.UserID(), nil)
	assert.False(t, ok, "no persona registry, no external persona")
}

// Before this replica's registry has heard of a persona, the persona store
// decides whether an account may be created under it.
func TestCreatableFromThePersonaStore(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	stored := map[string]*persona.Persona{
		"new-client": {Name: "new-client", Roles: []string{"new_client"}, External: true},
		"new-staff":  {Name: "new-staff", Roles: []string{"new_staff"}},
	}
	f.failing("", func(c *Config) {
		c.StoredPersona = func(_ context.Context, name string) (*persona.Persona, bool) {
			p, ok := stored[name]
			return p, ok
		}
	})
	_, err := f.svc.Create(ctx, NewAccount{Email: "a@example.org", Persona: "new-client"}, "admin")
	require.NoError(t, err)
	_, err = f.svc.Create(ctx, NewAccount{Email: "b@example.org", Persona: "new-staff"}, "admin")
	assert.ErrorIs(t, err, ErrPersona, "a stored persona that is not external")
	_, err = f.svc.Create(ctx, NewAccount{Email: "c@example.org", Persona: "analyst"}, "admin")
	assert.ErrorIs(t, err, ErrPersona, "a persona the registry knows is decided by the registry")
}

func (f *failingStore) Convert(ctx context.Context, email string, at time.Time) (bool, error) {
	if f.fail == "convert" {
		return false, errStore
	}
	return f.memStore.Convert(ctx, email, at)
}

// The word the admin list shows for each state an account can be in.
func TestStatus(t *testing.T) {
	now := time.Now()
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	for want, a := range map[string]Account{
		"active":    {ExpiresAt: &future},
		"expired":   {ExpiresAt: &past},
		"suspended": {SuspendedAt: &past},
		"converted": {ConvertedAt: &past, SuspendedAt: &past},
	} {
		assert.Equal(t, want, a.Status(now))
	}
}

// A refusal of the input names its reason and is ErrInvalid.
func TestInvalidNamesItsReason(t *testing.T) {
	err := invalid("name is longer than %d characters", 3)
	assert.ErrorIs(t, err, ErrInvalid)
	assert.Equal(t, "invalid external account: name is longer than 3 characters", err.Error())
}

// A service built with nothing optional reads the default settings and the
// clock, lists what its store holds, and refuses an expiry in the past.
func TestServiceDefaults(t *testing.T) {
	ctx := context.Background()
	svc := New(Config{Store: newMemStore()})
	assert.Equal(t, Defaults(), svc.settings(ctx))
	accounts, err := svc.List(ctx)
	require.NoError(t, err)
	assert.Empty(t, accounts)
	past := time.Now().Add(-time.Hour)
	assert.ErrorIs(t, svc.Extend(ctx, "a1", &past), ErrInvalid)
}

// A conversion with no address, or one the store cannot make, leaves the
// sign-in alone.
func TestConvertIsBestEffort(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a, err := f.svc.Create(ctx, NewAccount{Email: "client@example.org", Persona: "client"}, "admin")
	require.NoError(t, err)
	f.svc.ConvertOnIdentityProviderSignIn(ctx, "")
	f.failing("convert", nil)
	f.svc.ConvertOnIdentityProviderSignIn(ctx, "client@example.org")
	got, err := f.store.Get(ctx, a.ID)
	require.NoError(t, err)
	assert.Nil(t, got.ConvertedAt)
}
