package sharelink

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// memShares is the share store the routes act through, holding its rows in
// memory with the store's own meaning for each write.
type memShares struct {
	portaldomain.ShareStore
	rows    []portaldomain.Share
	listErr error
	saveErr error
}

func (m *memShares) list(match func(portaldomain.Share) bool) ([]portaldomain.Share, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var out []portaldomain.Share
	for _, s := range m.rows {
		if match(s) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *memShares) ListByAsset(_ context.Context, id string) ([]portaldomain.Share, error) {
	return m.list(func(s portaldomain.Share) bool { return s.AssetID == id })
}

func (m *memShares) ListByCollection(_ context.Context, id string) ([]portaldomain.Share, error) {
	return m.list(func(s portaldomain.Share) bool { return s.CollectionID == id })
}

func (m *memShares) Insert(_ context.Context, s *portaldomain.Share) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	s.CreatedAt = time.Now()
	m.rows = append(m.rows, *s)
	return nil
}

func (m *memShares) GetByID(_ context.Context, id string) (*portaldomain.Share, error) {
	for i := range m.rows {
		if m.rows[i].ID == id {
			s := m.rows[i]
			return &s, nil
		}
	}
	return nil, errors.New("not found")
}

func (m *memShares) find(id string) *portaldomain.Share {
	for i := range m.rows {
		if m.rows[i].ID == id {
			return &m.rows[i]
		}
	}
	return nil
}

func (m *memShares) Revoke(_ context.Context, id string) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.find(id).Revoked = true
	return nil
}

func (m *memShares) SetPermission(_ context.Context, id string, perm portaldomain.SharePermission) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.find(id).Permission = perm
	return nil
}

func (m *memShares) UpdateLink(_ context.Context, id string, u portaldomain.LinkUpdate) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	s := m.find(id)
	s.AccessMode, s.ExpiresAt, s.NoticeText, s.HideExpiration = u.AccessMode, u.ExpiresAt, u.NoticeText, u.HideExpiration
	return nil
}

type memAssets struct {
	portaldomain.AssetStore
	rows map[string]*portaldomain.Asset
}

func (m memAssets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	if a, ok := m.rows[id]; ok {
		return a, nil
	}
	return nil, errors.New("not found")
}

type memCollections struct {
	portaldomain.CollectionStore
	rows map[string]*portaldomain.Collection
}

func (m memCollections) Get(_ context.Context, id string) (*portaldomain.Collection, error) {
	if c, ok := m.rows[id]; ok {
		return c, nil
	}
	return nil, errors.New("not found")
}

type memPrompts struct {
	prompt.Store
	rows map[string]*prompt.Prompt
}

func (m memPrompts) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	if p, ok := m.rows[id]; ok {
		return p, nil
	}
	return nil, errors.New("not found")
}

var (
	owner    = &access.User{UserID: "u-owner", Email: "owner@example.com"}
	stranger = &access.User{UserID: "u-other", Email: "other@example.com"}
	now      = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
)

type fixture struct {
	shares  *memShares
	sharing bool
	mux     *http.ServeMux
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{shares: &memShares{}, sharing: true, mux: http.NewServeMux()}
	New(Config{
		Shares:        f.shares,
		Assets:        memAssets{rows: map[string]*portaldomain.Asset{"a1": {ID: "a1", OwnerID: owner.UserID, OwnerEmail: owner.Email}}},
		Collections:   memCollections{rows: map[string]*portaldomain.Collection{"c1": {ID: "c1", OwnerID: owner.UserID}}},
		Prompts:       memPrompts{rows: map[string]*prompt.Prompt{"p1": {ID: "p1", OwnerEmail: owner.Email}}},
		Access:        access.New(access.Config{}),
		Sharing:       func(*access.User) bool { return f.sharing },
		HasAccount:    func(_ context.Context, email string) bool { return email == "member@example.com" },
		PublicBaseURL: "https://platform.example.com",
		Now:           func() time.Time { return now },
	}).Register(f.mux)
	return f
}

// call sends one request as user and answers the status and the decoded body.
func (f *fixture) call(t *testing.T, user *access.User, method, path, body string) (code int, out map[string]any) {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if user != nil {
		req = req.WithContext(access.ContextWithUser(req.Context(), user))
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	out = map[string]any{}
	if rec.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	}
	return rec.Code, out
}

// shareOf is the share a link route answered with.
func shareOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	share, ok := body["share"].(map[string]any)
	require.True(t, ok, "no share in %v", body)
	return share
}

func (f *fixture) active() []portaldomain.Share {
	var out []portaldomain.Share
	for _, s := range f.shares.rows {
		if !s.Revoked {
			out = append(out, s)
		}
	}
	return out
}

// Choosing a mode creates the item's one link, a second choice changes that
// same link in place with its token kept, and Only people added removes it.
func TestGeneralLinkLifecycle(t *testing.T) {
	f := newFixture(t)

	code, body := f.call(t, owner, http.MethodPut, "/api/v1/portal/assets/a1/link",
		`{"access_mode":"public","expires_in":"24h"}`)
	require.Equal(t, http.StatusCreated, code, body)
	share := shareOf(t, body)
	token, _ := share["token"].(string)
	assert.Equal(t, "public", share["access_mode"])
	assert.Equal(t, "https://platform.example.com/portal/view/"+token, body["share_url"])
	require.Len(t, f.active(), 1)
	// A new link's lifetime is BuildShare's, on the wall clock.
	assert.WithinDuration(t, time.Now().Add(24*time.Hour), *f.active()[0].ExpiresAt, time.Minute)

	code, body = f.call(t, owner, http.MethodPut, "/api/v1/portal/assets/a1/link", `{"access_mode":"authenticated"}`)
	require.Equal(t, http.StatusOK, code, body)
	share = shareOf(t, body)
	assert.Equal(t, token, share["token"], "the link handed out keeps working under the new rule")
	assert.Equal(t, "authenticated", share["access_mode"])
	require.Len(t, f.active(), 1, "choosing a mode never makes a second link")
	assert.Nil(t, f.active()[0].ExpiresAt, "a link for signed-in people is revoke-only")

	banner := "Board only"
	code, _ = f.call(t, owner, http.MethodPut, "/api/v1/portal/assets/a1/link",
		`{"notice_text":"`+banner+`","hide_expiration":true}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, banner, f.active()[0].NoticeText)
	assert.Equal(t, "authenticated", string(f.active()[0].AccessMode), "an omitted mode keeps the link's")

	code, _ = f.call(t, owner, http.MethodDelete, "/api/v1/portal/assets/a1/link", "")
	require.Equal(t, http.StatusNoContent, code)
	assert.Empty(t, f.active())

	code, _ = f.call(t, owner, http.MethodDelete, "/api/v1/portal/assets/a1/link", "")
	assert.Equal(t, http.StatusNoContent, code, "an item with no link is left as it is")
}

// The link the dialog changes is the newest general one: a posted link and a
// grant to a person are left alone.
func TestGeneralLinkLeavesOtherSharesAlone(t *testing.T) {
	f := newFixture(t)
	f.shares.rows = []portaldomain.Share{
		{ID: "post", AssetID: "a1", Token: "t-post", AccessMode: "authenticated", Origin: portaldomain.OriginChannelPost, PostedTo: "#general", CreatedAt: now},
		{ID: "grant", AssetID: "a1", Token: "t-grant", SharedWithEmail: "member@example.com", AccessMode: "restricted", CreatedAt: now},
		{ID: "old", AssetID: "a1", Token: "t-old", AccessMode: "authenticated", CreatedAt: now.Add(-time.Hour)},
		{ID: "new", AssetID: "a1", Token: "t-new", AccessMode: "authenticated", CreatedAt: now.Add(-time.Minute)},
	}
	code, body := f.call(t, owner, http.MethodPut, "/api/v1/portal/assets/a1/link", `{"access_mode":"public","expires_in":"1h"}`)
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "t-new", shareOf(t, body)["token"])
	assert.Equal(t, "authenticated", string(f.shares.find("old").AccessMode), "an older link is listed apart, unchanged")
	assert.Equal(t, "authenticated", string(f.shares.find("post").AccessMode))
}

func TestCollectionLink(t *testing.T) {
	f := newFixture(t)
	code, body := f.call(t, owner, http.MethodPut, "/api/v1/portal/collections/c1/link", `{"access_mode":"authenticated"}`)
	require.Equal(t, http.StatusCreated, code, body)
	assert.Equal(t, "c1", f.active()[0].CollectionID)
	code, _ = f.call(t, owner, http.MethodDelete, "/api/v1/portal/collections/c1/link", "")
	assert.Equal(t, http.StatusNoContent, code)
	assert.Empty(t, f.active())
}

func TestLinkRefusals(t *testing.T) {
	cases := []struct {
		name   string
		user   *access.User
		path   string
		body   string
		before func(*fixture)
		status int
	}{
		{name: "anonymous", path: "/api/v1/portal/assets/a1/link", body: `{}`, status: http.StatusUnauthorized},
		{name: "no such asset", user: owner, path: "/api/v1/portal/assets/nope/link", body: `{}`, status: http.StatusNotFound},
		{name: "no such collection", user: owner, path: "/api/v1/portal/collections/nope/link", body: `{}`, status: http.StatusNotFound},
		{name: "not the owner", user: stranger, path: "/api/v1/portal/assets/a1/link", body: `{"access_mode":"authenticated"}`, status: http.StatusForbidden},
		{name: "not the collection owner", user: stranger, path: "/api/v1/portal/collections/c1/link", body: `{}`, status: http.StatusForbidden},
		{
			name: "persona may not share", user: owner, path: "/api/v1/portal/assets/a1/link", body: `{"access_mode":"authenticated"}`,
			before: func(f *fixture) { f.sharing = false }, status: http.StatusForbidden,
		},
		{name: "not JSON", user: owner, path: "/api/v1/portal/assets/a1/link", body: `{`, status: http.StatusBadRequest},
		{name: "restricted", user: owner, path: "/api/v1/portal/assets/a1/link", body: `{"access_mode":"restricted"}`, status: http.StatusBadRequest},
		{name: "public with no expiry", user: owner, path: "/api/v1/portal/assets/a1/link", body: `{"access_mode":"public"}`, status: http.StatusBadRequest},
		{
			name: "expiry on a signed-in link", user: owner, path: "/api/v1/portal/assets/a1/link", body: `{"access_mode":"authenticated","expires_in":"1h"}`,
			before: func(f *fixture) {
				f.shares.rows = []portaldomain.Share{{ID: "l", AssetID: "a1", AccessMode: "authenticated", CreatedAt: now}}
			},
			status: http.StatusBadRequest,
		},
		{
			name: "store unreadable", user: owner, path: "/api/v1/portal/assets/a1/link", body: `{"access_mode":"authenticated"}`,
			before: func(f *fixture) { f.shares.listErr = errors.New("down") }, status: http.StatusInternalServerError,
		},
		{
			name: "store refuses the insert", user: owner, path: "/api/v1/portal/assets/a1/link", body: `{"access_mode":"authenticated"}`,
			before: func(f *fixture) { f.shares.saveErr = errors.New("down") }, status: http.StatusInternalServerError,
		},
		{
			name: "store refuses the update", user: owner, path: "/api/v1/portal/assets/a1/link", body: `{"access_mode":"authenticated"}`,
			before: func(f *fixture) {
				f.shares.rows = []portaldomain.Share{{ID: "l", AssetID: "a1", AccessMode: "authenticated", CreatedAt: now}}
				f.shares.saveErr = errors.New("down")
			},
			status: http.StatusInternalServerError,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.before != nil {
				tc.before(f)
			}
			code, _ := f.call(t, tc.user, http.MethodPut, tc.path, tc.body)
			assert.Equal(t, tc.status, code)
		})
	}
}

// Removing a link takes no sharing right: a persona that may not share may
// still stop sharing.
func TestDeleteLinkNeedsNoSharingRight(t *testing.T) {
	f := newFixture(t)
	f.sharing = false
	f.shares.rows = []portaldomain.Share{{ID: "l", AssetID: "a1", AccessMode: "authenticated", CreatedAt: now}}
	code, _ := f.call(t, owner, http.MethodDelete, "/api/v1/portal/assets/a1/link", "")
	assert.Equal(t, http.StatusNoContent, code)
	assert.True(t, f.shares.find("l").Revoked)

	f.shares.rows[0].Revoked = false
	f.shares.saveErr = errors.New("down")
	code, _ = f.call(t, owner, http.MethodDelete, "/api/v1/portal/assets/a1/link", "")
	assert.Equal(t, http.StatusInternalServerError, code)

	f.shares.listErr = errors.New("down")
	code, _ = f.call(t, owner, http.MethodDelete, "/api/v1/portal/assets/a1/link", "")
	assert.Equal(t, http.StatusInternalServerError, code)
}

// A grant's permission changes in place, for each kind of item; a link has
// none, and an address with no account can only view.
func TestPatchSharePermission(t *testing.T) {
	grants := []portaldomain.Share{
		{ID: "asset-member", AssetID: "a1", SharedWithEmail: "member@example.com", Permission: "viewer"},
		{ID: "coll-group", CollectionID: "c1", SharedWithPersona: "finance", Permission: "viewer"},
		{ID: "prompt-member", PromptID: "p1", SharedWithEmail: "member@example.com", Permission: "viewer"},
	}
	for _, g := range grants {
		t.Run(g.ID, func(t *testing.T) {
			f := newFixture(t)
			f.shares.rows = []portaldomain.Share{g}
			code, body := f.call(t, owner, http.MethodPatch, "/api/v1/portal/shares/"+g.ID, `{"permission":"editor"}`)
			require.Equal(t, http.StatusOK, code, body)
			assert.Equal(t, "editor", body["permission"])
			assert.Equal(t, portaldomain.PermissionEditor, f.shares.find(g.ID).Permission)

			code, _ = f.call(t, stranger, http.MethodPatch, "/api/v1/portal/shares/"+g.ID, `{"permission":"viewer"}`)
			assert.Equal(t, http.StatusForbidden, code)
		})
	}
}

func TestPatchShareRefusals(t *testing.T) {
	rows := []portaldomain.Share{
		{ID: "guest", AssetID: "a1", SharedWithEmail: "guest@example.org", Permission: "viewer"},
		{ID: "link", AssetID: "a1", AccessMode: "authenticated"},
		{ID: "gone", AssetID: "a1", SharedWithEmail: "member@example.com", Revoked: true},
		{ID: "orphan-asset", AssetID: "nope", SharedWithEmail: "member@example.com"},
		{ID: "orphan-coll", CollectionID: "nope", SharedWithEmail: "member@example.com"},
		{ID: "orphan-prompt", PromptID: "nope", SharedWithEmail: "member@example.com"},
		{ID: "ok", AssetID: "a1", SharedWithEmail: "member@example.com"},
	}
	cases := []struct {
		name, id, body string
		user           *access.User
		storeErr       bool
		status         int
	}{
		{name: "anonymous", id: "ok", body: `{"permission":"editor"}`, status: http.StatusUnauthorized},
		{name: "not JSON", id: "ok", body: `{`, user: owner, status: http.StatusBadRequest},
		{name: "no such share", id: "missing", body: `{"permission":"editor"}`, user: owner, status: http.StatusNotFound},
		{name: "revoked", id: "gone", body: `{"permission":"editor"}`, user: owner, status: http.StatusNotFound},
		{name: "asset gone", id: "orphan-asset", body: `{"permission":"editor"}`, user: owner, status: http.StatusNotFound},
		{name: "collection gone", id: "orphan-coll", body: `{"permission":"editor"}`, user: owner, status: http.StatusNotFound},
		{name: "prompt gone", id: "orphan-prompt", body: `{"permission":"editor"}`, user: owner, status: http.StatusNotFound},
		{name: "unknown permission", id: "ok", body: `{"permission":"owner"}`, user: owner, status: http.StatusBadRequest},
		{name: "a link", id: "link", body: `{"permission":"editor"}`, user: owner, status: http.StatusBadRequest},
		{name: "a guest as editor", id: "guest", body: `{"permission":"editor"}`, user: owner, status: http.StatusBadRequest},
		{name: "store refuses", id: "ok", body: `{"permission":"editor"}`, user: owner, storeErr: true, status: http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.shares.rows = append([]portaldomain.Share(nil), rows...)
			if tc.storeErr {
				f.shares.saveErr = errors.New("down")
			}
			code, body := f.call(t, tc.user, http.MethodPatch, "/api/v1/portal/shares/"+tc.id, tc.body)
			assert.Equal(t, tc.status, code, body)
		})
	}

	f := newFixture(t)
	f.shares.rows = append([]portaldomain.Share(nil), rows...)
	code, _ := f.call(t, owner, http.MethodPatch, "/api/v1/portal/shares/guest", `{"permission":"viewer"}`)
	assert.Equal(t, http.StatusOK, code, "a guest may still be made a viewer")
}

// With no collection store the collection link routes are not mounted, and
// with no prompt store a prompt share is not found.
func TestRegisterWithoutStores(t *testing.T) {
	mux := http.NewServeMux()
	New(Config{}).Register(mux)
	_, pattern := mux.Handler(httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/portal/shares/x", http.NoBody))
	assert.Empty(t, pattern, "nothing is mounted without a share store")

	shares := &memShares{rows: []portaldomain.Share{{ID: "p", PromptID: "p1", SharedWithEmail: "member@example.com"}}}
	mux = http.NewServeMux()
	New(Config{Shares: shares, Access: access.New(access.Config{})}).Register(mux)
	_, pattern = mux.Handler(httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/portal/collections/c1/link", http.NoBody))
	assert.Empty(t, pattern)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/portal/shares/p", strings.NewReader(`{"permission":"editor"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(access.ContextWithUser(req.Context(), owner)))
	assert.Equal(t, http.StatusNotFound, rec.Code)

	shares.rows = []portaldomain.Share{{ID: "c", CollectionID: "c1", SharedWithEmail: "member@example.com"}}
	req = httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/portal/shares/c", strings.NewReader(`{"permission":"editor"}`))
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(access.ContextWithUser(req.Context(), owner)))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
