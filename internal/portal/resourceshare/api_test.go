package resourceshare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// memShares is the store as the routes use it, kept in memory.
type memShares struct {
	rows []portaldomain.Share
	err  error
}

func (m *memShares) Insert(_ context.Context, s *portaldomain.Share) error {
	if m.err != nil {
		return m.err
	}
	m.rows = append(m.rows, *s)
	return nil
}

func (m *memShares) List(_ context.Context, id string) ([]portaldomain.Share, error) {
	out := []portaldomain.Share{}
	for _, s := range m.rows {
		if s.ResourceID == id && !s.Revoked {
			out = append(out, s)
		}
	}
	return out, m.err
}

func (*memShares) matches(s portaldomain.Share, r Recipient) bool {
	return !s.Revoked && ((r.Email != "" && strings.EqualFold(s.SharedWithEmail, r.Email)) ||
		(r.Persona != "" && s.SharedWithPersona == r.Persona))
}

func (m *memShares) Permission(_ context.Context, id string, r Recipient) (string, error) {
	best := ""
	for _, s := range m.rows {
		if s.ResourceID == id && m.matches(s, r) && (best == "" || s.Permission == portaldomain.PermissionEditor) {
			best = string(s.Permission)
		}
	}
	return best, m.err
}

func (m *memShares) SharedWith(_ context.Context, r Recipient) ([]Ref, error) {
	out := []Ref{}
	for _, s := range m.rows {
		if m.matches(s, r) {
			out = append(out, Ref{ResourceID: s.ResourceID, ShareID: s.ID, SharedBy: s.CreatedBy, Permission: string(s.Permission)})
		}
	}
	return out, m.err
}

func (m *memShares) Revoke(_ context.Context, id, shareID string) error {
	for i := range m.rows {
		if m.rows[i].ID == shareID && m.rows[i].ResourceID == id && !m.rows[i].Revoked {
			m.rows[i].Revoked = true
			return nil
		}
	}
	return ErrNotFound
}

func (m *memShares) SetPermission(_ context.Context, id, shareID string, perm portaldomain.SharePermission) error {
	for i := range m.rows {
		if m.rows[i].ID == shareID && m.rows[i].ResourceID == id && !m.rows[i].Revoked {
			m.rows[i].Permission = perm
			return nil
		}
	}
	return ErrNotFound
}

type memResources map[string]*resource.Resource

func (m memResources) Get(_ context.Context, id string) (*resource.Resource, error) {
	if r, ok := m[id]; ok {
		return r, nil
	}
	return nil, errors.New("not found")
}

// people are the callers, by the address a test request names.
var people = map[string]struct {
	claims resource.Claims
	caller Caller
}{
	"owner@example.com":  {resource.Claims{Sub: "owner", Email: "owner@example.com", Roles: []string{"staff"}}, Caller{Persona: "staff", Sharing: true}},
	"quiet@example.com":  {resource.Claims{Sub: "quiet", Email: "quiet@example.com", Roles: []string{"quiet"}}, Caller{Persona: "quiet"}},
	"reader@example.com": {resource.Claims{Sub: "reader", Email: "reader@example.com", Roles: []string{"sales"}}, Caller{Persona: "sales", Sharing: true}},
	"vendor@example.com": {resource.Claims{Sub: "ext:1", Email: "vendor@example.com", Roles: []string{"vendor"}}, Caller{
		Persona: "vendors", External: true,
	}},
	"admin@example.com": {resource.Claims{Sub: "admin", Email: "admin@example.com", IsAdmin: true}, Caller{Persona: "admin", Sharing: true}},
}

type fixture struct {
	shares   *memShares
	svc      *Service
	mux      *http.ServeMux
	notified []string
}

func newFixture() *fixture {
	f := &fixture{shares: &memShares{}}
	res := memResources{
		"r1":     {ID: "r1", Scope: resource.ScopeUser, ScopeID: "owner", UploaderSub: "owner", DisplayName: "extract.csv"},
		"upload": {ID: "upload", Scope: resource.ScopeUser, ScopeID: "ext:1", UploaderSub: "ext:1", DisplayName: "delivery.csv"},
	}
	f.svc = New(Deps{
		Shares: f.shares, Resources: res,
		PersonaExists: func(name string) bool { return name != "nobody" },
		Claims: func(r *http.Request) (*resource.Claims, error) {
			p, ok := people[r.Header.Get("X-As")]
			if !ok {
				return nil, errors.New("signed out")
			}
			c := p.claims
			c.Shared = f.svc.Lookup(r.Context(), &c, p.caller)
			return &c, nil
		},
		Caller: func(roles []string) Caller {
			for _, p := range people {
				if len(roles) > 0 && len(p.claims.Roles) > 0 && p.claims.Roles[0] == roles[0] {
					return p.caller
				}
			}
			return Caller{}
		},
		Notify: func(_ context.Context, s *portaldomain.Share, id, title, msg string) {
			f.notified = append(f.notified, s.SharedWithEmail+":"+id+":"+title+":"+msg)
		},
		Members: func(_ context.Context, name string) []string {
			if name == "marketing" {
				return []string{"m1@example.com", "m2@example.com"}
			}
			return nil
		},
	})
	f.mux = http.NewServeMux()
	f.svc.Register(f.mux)
	return f
}

func (f *fixture) do(as, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	r.Header.Set("X-As", as)
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}

// A person shares a file they uploaded by address; the recipient finds it
// shared with them and reads it as viewer; a third person reaches nothing
// (#2258).
func TestShareAFileWithAPerson(t *testing.T) {
	f := newFixture()
	w := f.do("owner@example.com", http.MethodPost, "/api/v1/resources/r1/shares", `{"shared_with_email":"reader@example.com","message":"for the review"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Equal(t, []string{"reader@example.com:r1:extract.csv:for the review"}, f.notified)

	w = f.do("reader@example.com", http.MethodGet, "/api/v1/resources/shared-with-me", "")
	require.Equal(t, http.StatusOK, w.Code)
	var list SharedList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(t, list.Resources, 1)
	assert.Equal(t, "r1", list.Resources[0].Resource.ID)
	assert.Equal(t, "viewer", list.Resources[0].Permission)

	w = f.do("quiet@example.com", http.MethodGet, "/api/v1/resources/r1/shares", "")
	assert.Equal(t, http.StatusNotFound, w.Code, "a third person does not learn the file exists")

	w = f.do("owner@example.com", http.MethodGet, "/api/v1/resources/r1/shares", "")
	require.Equal(t, http.StatusOK, w.Code)
	var shares ShareList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &shares))
	require.Len(t, shares.Shares, 1)
	w = f.do("reader@example.com", http.MethodGet, "/api/v1/resources/r1/shares", "")
	assert.Equal(t, http.StatusForbidden, w.Code, "a viewer does not manage the shares")

	w = f.do("owner@example.com", http.MethodDelete, "/api/v1/resources/r1/shares/"+shares.Shares[0].ID, "")
	assert.Equal(t, http.StatusNoContent, w.Code)
	w = f.do("owner@example.com", http.MethodDelete, "/api/v1/resources/r1/shares/"+shares.Shares[0].ID, "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	w = f.do("reader@example.com", http.MethodGet, "/api/v1/resources/shared-with-me", "")
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	assert.Empty(t, list.Resources)
}

// Who may share, and with whom.
func TestShareRefusals(t *testing.T) {
	f := newFixture()
	cases := []struct {
		as, body string
		want     int
	}{
		{"quiet@example.com", `{"shared_with_email":"x@example.com"}`, http.StatusNotFound},
		{"owner@example.com", `{}`, http.StatusBadRequest},
		{"owner@example.com", `{"shared_with_persona":"finance"}`, http.StatusCreated},
		{"owner@example.com", `{"shared_with_persona":"nobody"}`, http.StatusBadRequest},
		{"owner@example.com", `{"shared_with_email":"not an address"}`, http.StatusBadRequest},
		{"owner@example.com", `{"shared_with_email":"x@example.com","bogus":1}`, http.StatusBadRequest},
		{"owner@example.com", `{"shared_with_email":"x@example.com","message":"<b>x</b>"}`, http.StatusBadRequest},
		{"admin@example.com", `{"shared_with_persona":"finance","permission":"editor"}`, http.StatusCreated},
		{"owner@example.com", `{"shared_with_persona":"staff","notify":false}`, http.StatusCreated},
	}
	for _, c := range cases {
		w := f.do(c.as, http.MethodPost, "/api/v1/resources/r1/shares", c.body)
		assert.Equal(t, c.want, w.Code, "%s %s: %s", c.as, c.body, w.Body.String())
	}
	assert.Equal(t, http.StatusUnauthorized, f.do("", http.MethodGet, "/api/v1/resources/shared-with-me", "").Code)

	// A persona that withholds sharing is refused, owning the file or not.
	people["quiet@example.com"] = struct {
		claims resource.Claims
		caller Caller
	}{resource.Claims{Sub: "owner", Email: "quiet@example.com", Roles: []string{"quiet"}}, Caller{Persona: "quiet"}}
	defer func() {
		people["quiet@example.com"] = struct {
			claims resource.Claims
			caller Caller
		}{resource.Claims{Sub: "quiet", Email: "quiet@example.com", Roles: []string{"quiet"}}, Caller{Persona: "quiet"}}
	}()
	w := f.do("quiet@example.com", http.MethodPost, "/api/v1/resources/r1/shares", `{"shared_with_email":"x@example.com"}`)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// An editor share is held to viewer for a person of an external persona, who
// shares nothing, their own upload included (#2290).
func TestExternalPeople(t *testing.T) {
	f := newFixture()
	w := f.do("owner@example.com", http.MethodPost, "/api/v1/resources/r1/shares", `{"shared_with_email":"vendor@example.com","permission":"editor"}`)
	require.Equal(t, http.StatusCreated, w.Code)
	w = f.do("vendor@example.com", http.MethodGet, "/api/v1/resources/shared-with-me", "")
	var list SharedList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(t, list.Resources, 1)
	assert.Equal(t, "viewer", list.Resources[0].Permission)
	c := people["vendor@example.com"]
	assert.Equal(t, "viewer", f.svc.Lookup(context.Background(), &c.claims, c.caller)("r1"))

	for _, body := range []string{`{"shared_with_email":"owner@example.com"}`, `{"shared_with_persona":"vendors"}`} {
		w = f.do("vendor@example.com", http.MethodPost, "/api/v1/resources/upload/shares", body)
		assert.Equal(t, http.StatusForbidden, w.Code, body)
	}
}

func TestLookupFailureGrantsNothing(t *testing.T) {
	f := newFixture()
	f.shares.err = errors.New("down")
	c := people["reader@example.com"]
	assert.Empty(t, f.svc.Lookup(context.Background(), &c.claims, c.caller)("r1"))
	w := f.do("owner@example.com", http.MethodGet, "/api/v1/resources/r1/shares", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	w = f.do("reader@example.com", http.MethodGet, "/api/v1/resources/shared-with-me", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// A file posted to a channel is opened by a link share any signed-in person of
// an internal persona reaches, recorded as posted where it went; a person who
// may not share the file makes none.
func TestLinkShare(t *testing.T) {
	f := newFixture()
	res := &resource.Resource{ID: "r1", Scope: resource.ScopeUser, ScopeID: "owner", UploaderSub: "owner"}
	owner := people["owner@example.com"].claims

	share, status, msg := f.svc.LinkShare(context.Background(), &owner, res, "#launch")
	require.NotNil(t, share, "%d %s", status, msg)
	assert.Equal(t, "r1", share.ResourceID)
	assert.Equal(t, portaldomain.OriginChannelPost, share.Origin)
	assert.Equal(t, "#launch", share.PostedTo)
	assert.Equal(t, "authenticated", string(share.AccessMode))
	assert.Empty(t, share.SharedWithEmail)
	require.Len(t, f.shares.rows, 1)

	for _, as := range []string{"quiet@example.com", "reader@example.com", "vendor@example.com"} {
		c := people[as].claims
		share, status, _ = f.svc.LinkShare(context.Background(), &c, res, "#launch")
		assert.Nil(t, share, as)
		assert.Equal(t, http.StatusForbidden, status, as)
	}

	f.shares.err = errors.New("database down")
	share, status, _ = f.svc.LinkShare(context.Background(), &owner, res, "#launch")
	assert.Nil(t, share)
	assert.Equal(t, http.StatusInternalServerError, status)
}
