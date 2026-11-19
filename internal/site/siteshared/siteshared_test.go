package siteshared

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

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// fakeStore holds sites and which (site, environment) each email or persona
// was granted.
type fakeStore struct {
	sites     map[string]*site.Site
	byEmail   map[string]map[string]bool // email -> "site/env"
	byPersona map[string]map[string]bool
	domains   map[string][]site.Domain
	err       error
	getErr    error
	domErr    error
	accessErr error
}

func (f *fakeStore) GrantedSiteIDs(_ context.Context, email, persona string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	seen := map[string]bool{}
	var out []string
	for _, set := range []map[string]bool{f.byEmail[email], f.byPersona[persona]} {
		for key := range set {
			id, _, _ := strings.Cut(key, "/")
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out, nil
}

func (f *fakeStore) Get(_ context.Context, id string) (*site.Site, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	st, ok := f.sites[id]
	if !ok {
		return nil, site.ErrNotFound
	}
	return st, nil
}

func (f *fakeStore) SiteDomains(_ context.Context, id string) ([]site.Domain, error) {
	return f.domains[id], f.domErr
}

func (f *fakeStore) SiteAccess(_ context.Context, id, env, email, persona string) (*site.Access, error) {
	if f.accessErr != nil {
		return nil, f.accessErr
	}
	key := id + "/" + env
	return &site.Access{OwnerEmail: "owner@example.com", InternalView: true, ByEmail: f.byEmail[email][key], ByPersona: f.byPersona[persona][key]}, nil
}

func newFake() *fakeStore {
	return &fakeStore{
		sites: map[string]*site.Site{
			"s1": {ID: "s1", Name: "zeta-site", Environments: []site.Environment{{Name: "dev"}, {Name: "staging", Status: "offline", StatusMessage: "Back Monday"}, {Name: "prod"}}},
			"s2": {ID: "s2", Name: "alpha-site", Environments: []site.Environment{{Name: "dev"}, {Name: "prod"}}},
		},
		byEmail:   map[string]map[string]bool{"pat@client.example": {"s1/dev": true, "s1/staging": true, "gone/dev": true}},
		byPersona: map[string]map[string]bool{"partners": {"s2/prod": true}},
		domains:   map[string][]site.Domain{"s2": {{Hostname: "www.alpha.example.com", Environment: "prod"}}},
	}
}

var at = site.Addressing{PagesDomain: "pages.example.net", Scheme: "https"}

// #2718: only the environments a grant opens for the person are listed, by
// site name, each with its address and whether it is online; a removed site
// adds nothing.
func TestShared_ListsOnlyGrantedEnvironments(t *testing.T) {
	got, err := Shared(context.Background(), newFake(), at, sitesvc.Actor{Email: "pat@client.example", Persona: "partners"})
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, Environment{Site: "alpha-site", Environment: "prod", Address: at.Address(newFake().sites["s2"], "prod", newFake().domains["s2"]), Online: true}, got[0])
	assert.Equal(t, "zeta-site", got[1].Site)
	assert.Equal(t, "dev", got[1].Environment)
	assert.True(t, got[1].Online)
	assert.Equal(t, Environment{Site: "zeta-site", Environment: "staging", Address: got[2].Address, Online: false, Message: "Back Monday"}, got[2])
	names := map[string]bool{}
	for _, e := range got {
		names[e.Site+"/"+e.Environment] = true
	}
	assert.False(t, names["zeta-site/prod"], "an environment no grant names is never listed")
	assert.False(t, names["alpha-site/dev"], "nor one beside a granted one")
}

// An email grant counts only once the email is verified, as at the site's
// gateway; internal view and ownership open nothing here.
func TestShared_UnverifiedEmailAndOtherRoutesOpenNothing(t *testing.T) {
	got, err := Shared(context.Background(), newFake(), at, sitesvc.Actor{Email: "pat@client.example", EmailUnverified: true})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.NotNil(t, got, "an empty list is [], never null")

	got, err = Shared(context.Background(), newFake(), at, sitesvc.Actor{Email: "owner@example.com", Persona: "staff"})
	require.NoError(t, err)
	assert.Empty(t, got, "the owner and internal view are the Sites area's, not grants")
}

// Every store failure is reported.
func TestShared_Failures(t *testing.T) {
	actor := sitesvc.Actor{Email: "pat@client.example", Persona: "partners"}
	for name, set := range map[string]func(*fakeStore){
		"granted": func(f *fakeStore) { f.err = errors.New("down") },
		"get":     func(f *fakeStore) { f.getErr = errors.New("down") },
		"domains": func(f *fakeStore) { f.domErr = errors.New("down") },
		"access":  func(f *fakeStore) { f.accessErr = errors.New("down") },
	} {
		f := newFake()
		set(f)
		_, err := Shared(context.Background(), f, at, actor)
		assert.Error(t, err, name)
	}
}

// The route answers the list as JSON, nothing when the caller is not
// resolved, and a 500 that repeats no store error.
func TestHandler(t *testing.T) {
	addressing := func() site.Addressing { return at }
	signedIn := func(http.ResponseWriter, *http.Request) (sitesvc.Actor, bool) {
		return sitesvc.Actor{Email: "pat@client.example"}, true
	}
	rec := httptest.NewRecorder()
	Handler(newFake(), addressing, signedIn)(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/portal/shared-sites", http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	var list List
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	assert.Len(t, list.Environments, 2)
	assert.NotContains(t, rec.Body.String(), "owner@example.com")
	assert.NotContains(t, rec.Body.String(), `"prod"`)

	rec = httptest.NewRecorder()
	Handler(newFake(), addressing, func(w http.ResponseWriter, _ *http.Request) (sitesvc.Actor, bool) {
		w.WriteHeader(http.StatusUnauthorized)
		return sitesvc.Actor{}, false
	})(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	broken := newFake()
	broken.err = errors.New("connection refused to 10.0.0.5")
	rec = httptest.NewRecorder()
	Handler(broken, addressing, signedIn)(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "10.0.0.5")
}
