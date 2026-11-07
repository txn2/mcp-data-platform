package peopleapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

type assets map[string]*portaldomain.Asset

func (a assets) Get(_ context.Context, id string) (*portaldomain.Asset, error) {
	if x, ok := a[id]; ok {
		return x, nil
	}
	return nil, errors.New("not found")
}

type versions struct {
	list []portaldomain.AssetVersion
	err  error
}

func (v versions) ListByAsset(context.Context, string, int, int) ([]portaldomain.AssetVersion, int, error) {
	return v.list, len(v.list), v.err
}

type shares struct {
	portaldomain.ShareStore
	list []portaldomain.Share
	err  error
}

func (s shares) ListByAsset(context.Context, string) ([]portaldomain.Share, error) {
	return s.list, s.err
}

func (shares) GetUserAssetPermissionViaCollection(context.Context, string, string, string) (portaldomain.SharePermission, error) {
	return "", nil
}

const (
	owner    = "owner@example.com"
	reader   = "reader@example.com"
	outsider = "outsider@example.com"
	partner  = "partner@client.example.org"
)

var tokens = []string{"tk_secret_persona_9137", "tk_secret_person_4410", "tk_secret_public_link_7781", "tk_revoked_0001"}

func fixture() (assets, versions, shares) {
	at := func(m int) time.Time { return time.Date(2026, 11, 1, 10, m, 0, 0, time.UTC) }
	past := time.Now().Add(-time.Hour)
	return assets{"a1": {ID: "a1", OwnerEmail: owner, OwnerID: "u-owner"}},
		versions{list: []portaldomain.AssetVersion{
			{Version: 1, CreatedBy: owner, CreatedAt: at(0)},
			{Version: 2, CreatedBy: "dana@example.com", CreatedAt: at(1)},
			{Version: 3, CreatedBy: "Dana@Example.com", CreatedAt: at(5)},
			{Version: 4, CreatedBy: "lee@example.com", CreatedAt: at(3)},
			{Version: 5, CreatedBy: "OWNER@example.com", CreatedAt: at(6)},
			{Version: 6, CreatedBy: "", CreatedAt: at(7)},
		}},
		shares{list: []portaldomain.Share{
			{Token: tokens[0], SharedWithPersona: "finance", Permission: portaldomain.PermissionViewer, CreatedBy: owner},
			{Token: tokens[1], SharedWithEmail: reader, Permission: portaldomain.PermissionEditor, CreatedBy: owner},
			{Token: tokens[1] + "x", SharedWithEmail: partner, Permission: portaldomain.PermissionViewer, CreatedBy: owner},
			{Token: tokens[2], AccessMode: "public", CreatedBy: owner, AccessCount: 14, Origin: portaldomain.OriginExplicit},
			{Token: tokens[3], SharedWithEmail: "gone@example.com", Revoked: true},
			{Token: "tk_expired", SharedWithEmail: "late@example.com", ExpiresAt: &past},
		}}
}

func serve(t *testing.T, user *access.User, cfg Config, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, cfg)
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, http.NoBody)
	if user != nil {
		r = r.WithContext(access.ContextWithUser(r.Context(), user))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func config() Config {
	a, v, s := fixture()
	return Config{
		Assets: a, Versions: v, Shares: s,
		Access: access.New(access.Config{
			Shares: s,
			PortalPermissions: func(roles []string) persona.PortalAccess {
				return persona.PortalAccess{External: len(roles) > 0 && roles[0] == "partner"}
			},
		}),
	}
}

var (
	ownerUser   = &access.User{UserID: "u-owner", Email: owner}
	readerUser  = &access.User{UserID: "u-reader", Email: reader}
	partnerUser = &access.User{UserID: "u-partner", Email: partner, Roles: []string{"partner"}}
)

// TestPeople is criteria 1 and 2: a reader of an asset another person owns
// sees the owner, and the people who wrote versions other than the owner are
// listed with their counts, newest first, whatever case their address was
// written in.
func TestPeople(t *testing.T) {
	w := serve(t, readerUser, config(), "/api/v1/portal/assets/a1/people")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var got People
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, owner, got.Owner.Email)
	require.Len(t, got.Contributors, 2)
	assert.Equal(t, "dana@example.com", got.Contributors[0].Email)
	assert.Equal(t, 2, got.Contributors[0].Versions)
	assert.Equal(t, "lee@example.com", got.Contributors[1].Email)
	assert.Equal(t, 1, got.Contributors[1].Versions)

	external := serve(t, partnerUser, config(), "/api/v1/portal/assets/a1/people")
	assert.Equal(t, http.StatusOK, external.Code, "an external reader sees the owner and contributors")
}

// TestShareSummary is criteria 3 and 4 and the security criterion: an
// internal reader gets the active personas, people and links; an external
// reader, a signed-out request and a person the asset is not shared with are
// refused; and no share token appears in the response in any encoding.
func TestShareSummary(t *testing.T) {
	w := serve(t, readerUser, config(), "/api/v1/portal/assets/a1/share-summary")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var got ShareSummary
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, 4, got.Total, "revoked and expired shares are left out")
	assert.Equal(t, []personaShare{{Persona: "finance", Permission: portaldomain.PermissionViewer}}, got.Personas)
	require.Len(t, got.People, 2)
	assert.Equal(t, partner, got.People[0].Email)
	require.Len(t, got.Links, 1)
	assert.Equal(t, 14, got.Links[0].Opened)
	assert.Equal(t, owner, got.Links[0].CreatedBy)

	body := w.Body.String()
	for _, tok := range append(tokens, "tk_expired") {
		for _, form := range []string{
			tok, url.QueryEscape(tok), base64.StdEncoding.EncodeToString([]byte(tok)),
			base64.URLEncoding.EncodeToString([]byte(tok)), strings.Trim(mustJSON(tok), `"`),
		} {
			assert.NotContains(t, body, form, "no share token reaches the summary")
		}
	}
	assert.NotContains(t, body, "token")
	assert.NotContains(t, body, "/share/")

	assert.Equal(t, http.StatusForbidden, serve(t, partnerUser, config(), "/api/v1/portal/assets/a1/share-summary").Code)
	assert.Equal(t, http.StatusUnauthorized, serve(t, nil, config(), "/api/v1/portal/assets/a1/share-summary").Code)
	stranger := &access.User{UserID: "u-x", Email: outsider}
	assert.Equal(t, http.StatusForbidden, serve(t, stranger, config(), "/api/v1/portal/assets/a1/share-summary").Code)
	assert.Equal(t, http.StatusForbidden, serve(t, stranger, config(), "/api/v1/portal/assets/a1/people").Code)
	assert.Equal(t, http.StatusNotFound, serve(t, ownerUser, config(), "/api/v1/portal/assets/none/people").Code)
}

func TestFailuresAndMounting(t *testing.T) {
	cfg := config()
	cfg.Versions = versions{err: errors.New("down")}
	assert.Equal(t, http.StatusInternalServerError, serve(t, ownerUser, cfg, "/api/v1/portal/assets/a1/people").Code)
	cfg = config()
	cfg.Shares = shares{err: errors.New("down")}
	cfg.Access = access.New(access.Config{})
	assert.Equal(t, http.StatusInternalServerError, serve(t, ownerUser, cfg, "/api/v1/portal/assets/a1/share-summary").Code)

	cfg = config()
	cfg.Versions = nil
	w := serve(t, ownerUser, cfg, "/api/v1/portal/assets/a1/people")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"contributors":[]`, "an empty list is [], never null")

	cfg.Shares = nil
	assert.Equal(t, http.StatusNotFound, serve(t, ownerUser, cfg, "/api/v1/portal/assets/a1/share-summary").Code, "no share store, no route")
	assert.Equal(t, http.StatusNotFound, serve(t, ownerUser, Config{}, "/api/v1/portal/assets/a1/people").Code, "nothing without stores")
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
