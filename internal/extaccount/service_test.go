package extaccount

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/browsersession"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// memStore keeps the Store contract in memory: one account per address,
// compared case-insensitively, and a link claimed once and only while live.
type memStore struct {
	mu       sync.Mutex
	accounts map[string]*Account
	links    map[string]*memLink
}

type memLink struct {
	account string
	expires time.Time
	created time.Time
	claimed bool
}

func newMemStore() *memStore {
	return &memStore{accounts: map[string]*Account{}, links: map[string]*memLink{}}
}

func (m *memStore) Insert(_ context.Context, a Account) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.accounts {
		if strings.EqualFold(b.Email, a.Email) {
			return ErrExists
		}
	}
	m.accounts[a.ID] = &a
	return nil
}

func (m *memStore) Get(_ context.Context, id string) (*Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return nil, ErrNotFound
	}
	c := *a
	return &c, nil
}

func (m *memStore) GetByEmail(_ context.Context, email string) (*Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if strings.EqualFold(a.Email, email) {
			c := *a
			return &c, nil
		}
	}
	return nil, ErrNotFound
}

func (m *memStore) List(context.Context) ([]Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Account, 0, len(m.accounts))
	for _, a := range m.accounts {
		out = append(out, *a)
	}
	return out, nil
}

func (m *memStore) edit(id string, fn func(*Account)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accounts[id]
	if !ok {
		return ErrNotFound
	}
	fn(a)
	return nil
}

func (m *memStore) SetExpiry(_ context.Context, id string, at *time.Time) error {
	return m.edit(id, func(a *Account) { a.ExpiresAt = at })
}

func (m *memStore) SetSuspended(_ context.Context, id string, at *time.Time) error {
	return m.edit(id, func(a *Account) { a.SuspendedAt = at })
}

func (m *memStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.accounts[id]; !ok {
		return ErrNotFound
	}
	delete(m.accounts, id)
	return nil
}

func (m *memStore) Convert(_ context.Context, email string, at time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, a := range m.accounts {
		if strings.EqualFold(a.Email, email) && a.ConvertedAt == nil {
			a.ConvertedAt = &at
			for h, l := range m.links {
				if l.account == a.ID {
					delete(m.links, h)
				}
			}
			return true, nil
		}
	}
	return false, nil
}

func (m *memStore) TouchSignIn(_ context.Context, id string, at time.Time) error {
	return m.edit(id, func(a *Account) { a.LastSignInAt = &at })
}

func (m *memStore) InsertLink(_ context.Context, hash, id string, expires time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.links[hash] = &memLink{account: id, expires: expires, created: time.Now()}
	return nil
}

func (m *memStore) ClaimLink(_ context.Context, hash string, now time.Time) (accountID string, claimed bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[hash]
	if !ok || l.claimed || !now.Before(l.expires) {
		return "", false, nil
	}
	l.claimed = true
	return l.account, true, nil
}

func (m *memStore) CountLinksSince(_ context.Context, id string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, l := range m.links {
		if l.account == id && !l.created.Before(since) {
			n++
		}
	}
	return n, nil
}

type sent struct {
	to, link   string
	invitation bool
	valid      time.Duration
}

type fixture struct {
	svc     *Service
	store   *memStore
	mail    []sent
	revoked []string
	now     time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "client", Roles: []string{"client"}, External: true}))
	require.NoError(t, reg.Register(&persona.Persona{Name: "analyst", Roles: []string{"analyst"}}))
	f := &fixture{store: newMemStore(), now: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	f.svc = New(Config{
		Store:    f.store,
		Personas: reg,
		KnownInternal: func(_ context.Context, email string) (bool, error) {
			return email == "staff@example.com", nil
		},
		Mail: func(_ context.Context, to, _, link string, invitation bool, valid time.Duration) error {
			f.mail = append(f.mail, sent{to: to, link: link, invitation: invitation, valid: valid})
			return nil
		},
		RevokeShares: func(_ context.Context, email string) error { f.revoked = append(f.revoked, email); return nil },
		BaseURL:      "https://portal.example.com/",
		Cookie:       browsersession.CookieConfig{Name: "s", Key: []byte("0123456789abcdef0123456789abcdef")},
		Now:          func() time.Time { return f.now },
	})
	return f
}

func tokenOf(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	require.NoError(t, err)
	require.Equal(t, ClaimPath, u.Path)
	return u.Query().Get("token")
}

func TestCreateRefusesWhatCannotBeAnExternalAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, c := range []struct {
		in   NewAccount
		want error
	}{
		{NewAccount{Email: "not an address", Persona: "client"}, ErrInvalid},
		{NewAccount{Email: "a@example.org"}, ErrInvalid},
		{NewAccount{Email: "a@example.org", Persona: "analyst"}, ErrPersona},
		{NewAccount{Email: "a@example.org", Persona: "nobody"}, ErrPersona},
		{NewAccount{Email: "staff@example.com", Persona: "client"}, ErrInternal},
		{NewAccount{Email: "a@example.org", Persona: "client", ExpiresAt: new(f.now.Add(-time.Hour))}, ErrInvalid},
	} {
		_, err := f.svc.Create(ctx, c.in, "admin@example.com")
		assert.ErrorIs(t, err, c.want, "%+v", c.in)
	}
	_, err := f.svc.Create(ctx, NewAccount{Email: "a@example.org", Persona: "client"}, "admin")
	require.NoError(t, err)
	_, err = f.svc.Create(ctx, NewAccount{Email: "A@example.org", Persona: "client"}, "admin")
	assert.ErrorIs(t, err, ErrExists)
}

func TestAnInvitationSignsInOnceAndTheSessionFollowsTheAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a, err := f.svc.Create(ctx, NewAccount{Email: "client@example.org", Name: "Client", Persona: "client"}, "admin")
	require.NoError(t, err)
	require.Len(t, f.mail, 1)
	assert.True(t, f.mail[0].invitation)
	assert.Equal(t, 7*24*time.Hour, f.mail[0].valid)
	assert.True(t, strings.HasPrefix(f.mail[0].link, "https://portal.example.com"+ClaimPath+"?token="))

	w := httptest.NewRecorder()
	claimed, err := f.svc.Claim(ctx, w, tokenOf(t, f.mail[0].link))
	require.NoError(t, err)
	assert.Equal(t, a.ID, claimed.ID)
	cookie := w.Result().Cookies()[0]
	claims, err := browsersession.VerifySession(cookie.Value, []byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	assert.Equal(t, UserIDPrefix+a.ID, claims.UserID)
	assert.Equal(t, []string{"client"}, claims.Roles)
	assert.Equal(t, 12*60*60, cookie.MaxAge, "the session lasts the session setting")

	_, err = f.svc.Claim(ctx, httptest.NewRecorder(), tokenOf(t, f.mail[0].link))
	assert.ErrorIs(t, err, ErrLink, "a link works once")

	roles, ok := f.svc.Admit(ctx, a.UserID(), nil)
	assert.True(t, ok)
	assert.Equal(t, []string{"client"}, roles)
	require.NoError(t, f.svc.Suspend(ctx, a.ID))
	_, ok = f.svc.Admit(ctx, a.UserID(), nil)
	assert.False(t, ok, "a suspended account's session is refused")
	require.NoError(t, f.svc.Resume(ctx, a.ID))
	require.NoError(t, f.svc.Extend(ctx, a.ID, new(f.now.Add(time.Hour))))
	f.now = f.now.Add(2 * time.Hour)
	_, ok = f.svc.Admit(ctx, a.UserID(), nil)
	assert.False(t, ok, "an expired account's session is refused")

	roles, ok = f.svc.Admit(ctx, "kc-subject", []string{"analyst"})
	assert.True(t, ok, "a session that is not an external account's is not judged")
	assert.Equal(t, []string{"analyst"}, roles)
}

func TestARequestedLinkGoesOnlyToAnActiveAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a, err := f.svc.Create(ctx, NewAccount{Email: "client@example.org", Persona: "client"}, "admin")
	require.NoError(t, err)
	f.mail = nil

	f.svc.RequestLink(ctx, "nobody@example.org")
	assert.Empty(t, f.mail, "an address with no account is sent nothing")
	f.svc.RequestLink(ctx, " CLIENT@example.org ")
	require.Len(t, f.mail, 1)
	assert.False(t, f.mail[0].invitation)
	assert.Equal(t, 15*time.Minute, f.mail[0].valid)

	f.now = f.now.Add(16 * time.Minute)
	_, err = f.svc.Claim(ctx, httptest.NewRecorder(), tokenOf(t, f.mail[0].link))
	assert.ErrorIs(t, err, ErrLink, "an expired link is dead")

	for range 10 {
		f.svc.RequestLink(ctx, "client@example.org")
	}
	assert.LessOrEqual(t, len(f.mail), maxLinksPerHour, "a mailbox is sent a few links an hour")

	require.NoError(t, f.svc.Suspend(ctx, a.ID))
	f.mail = nil
	f.svc.RequestLink(ctx, "client@example.org")
	assert.Empty(t, f.mail, "a suspended account is sent nothing")
}

func TestRemovingAnAccountRevokesItsShares(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a, err := f.svc.Create(ctx, NewAccount{Email: "client@example.org", Persona: "client"}, "admin")
	require.NoError(t, err)
	assert.True(t, f.svc.IsExternal(ctx, "client@example.org"))
	require.NoError(t, f.svc.Remove(ctx, a.ID))
	assert.Equal(t, []string{"client@example.org"}, f.revoked)
	_, ok := f.svc.Admit(ctx, a.UserID(), nil)
	assert.False(t, ok)
	assert.ErrorIs(t, f.svc.Remove(ctx, a.ID), ErrNotFound)
}

func TestAnIdentityProviderSignInConvertsTheAccount(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a, err := f.svc.Create(ctx, NewAccount{Email: "client@example.org", Persona: "client"}, "admin")
	require.NoError(t, err)
	invite := f.mail[0].link

	f.svc.ConvertOnIdentityProviderSignIn(ctx, "Client@Example.org")
	got, err := f.svc.Get(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, "converted", got.Status(f.now))
	_, ok := f.svc.Admit(ctx, a.UserID(), nil)
	assert.False(t, ok, "the external session ends")
	_, err = f.svc.Claim(ctx, httptest.NewRecorder(), tokenOf(t, invite))
	assert.ErrorIs(t, err, ErrLink, "its links die")
	assert.False(t, f.svc.IsExternal(ctx, "client@example.org"), "the address is no longer an external account's")
	f.mail = nil
	f.svc.RequestLink(ctx, "client@example.org")
	assert.Empty(t, f.mail, "and asks for no more links")
}

func TestSettingsValidate(t *testing.T) {
	require.NoError(t, Defaults().Validate())
	assert.Error(t, Settings{SessionHours: 0, InviteHours: 1, LinkMinutes: 1}.Validate())
	assert.Error(t, Settings{SessionHours: 1, InviteHours: 1, LinkMinutes: 2000}.Validate())
	assert.Equal(t, "expired", Account{ExpiresAt: new(time.Unix(0, 0))}.Status(time.Now()))
	_, ok := AccountID("ext:")
	assert.False(t, ok)
	assert.True(t, errors.Is(ErrLink, ErrLink))
}

// An account can be created under a persona another replica saved before
// this one's registry hears of it (#2092).
func TestCreateFindsAPersonaTheRegistryHasNotHeardOf(t *testing.T) {
	f := newFixture(t)
	f.svc.cfg.StoredPersona = func(_ context.Context, name string) (*persona.Persona, bool) {
		switch name {
		case "fresh":
			return &persona.Persona{Name: "fresh", Roles: []string{"fresh"}, External: true}, true
		case "internal":
			return &persona.Persona{Name: "internal", Roles: []string{"internal"}}, true
		}
		return nil, false
	}
	_, err := f.svc.Create(context.Background(), NewAccount{Email: "n@example.org", Persona: "fresh"}, "admin")
	require.NoError(t, err)
	_, err = f.svc.Create(context.Background(), NewAccount{Email: "m@example.org", Persona: "internal"}, "admin")
	assert.ErrorIs(t, err, ErrPersona)
	_, err = f.svc.Create(context.Background(), NewAccount{Email: "o@example.org", Persona: "analyst"}, "admin")
	assert.ErrorIs(t, err, ErrPersona, "the registry's answer stands for a persona it knows")
}
