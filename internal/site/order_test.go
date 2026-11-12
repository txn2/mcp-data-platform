package site

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlaced is #2617's order: a new environment goes after the one its place
// names, last before prod with none; an existing one moves, or stays with
// none; dev is always first and prod always last.
func TestPlaced(t *testing.T) {
	base := []string{EnvDev, "stage", EnvProd}
	cases := []struct {
		name  string
		env   string
		place *Place
		want  []string
	}{
		{"new with no place goes before prod", "qa", nil, []string{EnvDev, "stage", "qa", EnvProd}},
		{"new after dev", "test", &Place{After: EnvDev}, []string{EnvDev, "test", "stage", EnvProd}},
		{"new after stage", "test", &Place{After: "stage"}, []string{EnvDev, "stage", "test", EnvProd}},
		{"existing with no place stays", "stage", nil, base},
		{"dev always first", EnvDev, &Place{After: "stage"}, base},
		{"prod always last", EnvProd, &Place{After: EnvDev}, base},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Placed(base, tc.env, tc.place)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	moved, err := Placed([]string{EnvDev, "test", "stage", EnvProd}, "test", &Place{After: "stage"})
	require.NoError(t, err)
	assert.Equal(t, []string{EnvDev, "stage", "test", EnvProd}, moved, "an existing environment moves")

	noProd, err := Placed([]string{EnvDev, "live"}, "qa", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{EnvDev, "live", "qa"}, noProd, "with no prod a new environment goes last")
}

func TestPlaced_Refusals(t *testing.T) {
	base := []string{EnvDev, "stage", EnvProd}
	for _, tc := range []struct {
		env   string
		place Place
		want  string
	}{
		{"test", Place{}, "say which environment test goes after"},
		{"stage", Place{After: "stage"}, "stage cannot go after itself"},
		{"test", Place{After: EnvProd}, "prod is always last: nothing goes after it"},
	} {
		_, err := Placed(base, tc.env, &tc.place)
		require.Error(t, err, "%s %+v", tc.env, tc.place)
		assert.Contains(t, err.Error(), tc.want)
	}
	_, err := Placed(base, "test", &Place{After: "nowhere"})
	assert.True(t, errors.Is(err, ErrNoEnvironment), "an environment the site does not have: %v", err)
}

func TestPlaceChoicesAndRefusal(t *testing.T) {
	assert.Equal(t, []Place{{After: EnvDev}}, PlaceChoices([]string{EnvDev, EnvProd}, "stage"),
		"between dev and prod there is one place")
	choices := PlaceChoices([]string{EnvDev, "stage", "uat", EnvProd}, "test")
	assert.Equal(t, []Place{{After: EnvDev}, {After: "stage"}, {After: "uat"}}, choices)
	assert.Equal(t, []Place{{After: EnvDev}}, PlaceChoices([]string{EnvDev, "stage", EnvProd}, "stage"),
		"an environment being moved is not a place after itself")
	assert.Equal(t, []Place{{After: EnvDev}}, PlaceChoices(nil, "stage"), "a site with no environments yet")
	assert.Equal(t, "say which environment test goes after: dev, stage or uat", PlaceRefusal("test", choices))
	assert.Equal(t, "say which environment test goes after: dev", PlaceRefusal("test", []Place{{After: EnvDev}}))
	assert.Equal(t, "after stage", Place{After: "stage"}.String())
}

func TestSitePreviousAndOrder(t *testing.T) {
	st := &Site{Environments: []Environment{{Name: EnvDev, Release: 3}, {Name: "stage", Release: 2}, {Name: EnvProd, Release: 1}}}
	assert.Equal(t, []string{EnvDev, "stage", EnvProd}, st.Order())
	assert.Nil(t, st.Previous(EnvDev), "nothing comes before dev")
	assert.Nil(t, st.Previous("nowhere"))
	require.NotNil(t, st.Previous(EnvProd))
	assert.Equal(t, "stage", st.Previous(EnvProd).Name)
	assert.Equal(t, 3, st.Previous("stage").Release)
}

// TestAddressing is where every address a response names comes from: the
// main address for the environment holding it, <site>--<env> for the rest,
// an environment's first domain where it has one, and nothing without a
// pages domain.
func TestAddressing(t *testing.T) {
	a := Addressing{PagesDomain: "pages.example.net", Scheme: "https"}
	st := &Site{Name: "d4l", MainEnvironment: EnvProd, Environments: []Environment{{Name: EnvDev}, {Name: EnvProd}}}
	assert.Equal(t, "https://d4l.pages.example.net", a.OwnAddress(st))
	assert.Equal(t, map[string]string{EnvDev: "https://d4l--dev.pages.example.net", EnvProd: "https://d4l.pages.example.net"}, a.PagesAddresses(st))
	domains := []Domain{{Hostname: "d4l.example.com", Environment: EnvProd}}
	assert.Equal(t, map[string]string{EnvDev: "https://d4l--dev.pages.example.net", EnvProd: "https://d4l.example.com"}, a.Addresses(st, domains))
	assert.Equal(t, "https://shop.pages.example.net", a.PageOrigin("shop"))
	assert.Empty(t, a.AddressNote(st))

	withPort := Addressing{PagesDomain: "pages.localhost", Scheme: "http", Port: "28080"}
	assert.Equal(t, "http://d4l--dev.pages.localhost:28080", withPort.PagesAddress(st, EnvDev))

	none := &Site{Name: "d4l", Environments: st.Environments}
	assert.Empty(t, a.OwnAddress(none), "a main address given to no environment")
	assert.Equal(t, "https://d4l--prod.pages.example.net", a.PagesAddress(none, EnvProd))

	reserved := &Site{Name: "ab", MainEnvironment: EnvProd, Environments: st.Environments}
	assert.Empty(t, a.PagesAddress(reserved, EnvDev), "ab--dev is a reserved label no certificate is issued for")
	assert.NotEmpty(t, a.AddressNote(reserved))

	off := Addressing{Scheme: "https"}
	assert.Empty(t, off.OwnAddress(st))
	assert.Empty(t, off.PagesAddress(st, EnvDev))
	assert.Empty(t, off.PageOrigin("shop"))
	assert.Empty(t, off.AddressNote(st))
}

// TestDomainClaimable refuses a hostname that is not a plain name, one under
// the pages domain, the portal's own or a reserved one, and one under the
// session cookie's domain.
func TestDomainClaimable(t *testing.T) {
	rules := ClaimRules{PagesDomain: "pages.example.net", PortalHost: "portal.example.com", Reserved: []string{"internal.example.com"}, CookieDomain: ".corp.example.org"}
	require.NoError(t, (&Domain{Hostname: "www.example.com"}).Claimable(rules))
	for host, want := range map[string]string{
		"d4l.pages.example.net":    "under the pages domain",
		"portal.example.com":       "this platform answers on itself",
		"api.internal.example.com": "this platform answers on itself",
		"docs.corp.example.org":    "session cookie's domain",
		"not a host":               "",
	} {
		err := (&Domain{Hostname: host}).Claimable(rules)
		require.Error(t, err, host)
		assert.Contains(t, err.Error(), want, host)
	}
}

func TestPlaceNew(t *testing.T) {
	got, err := PlaceNew([]string{EnvDev, EnvProd}, "stage", nil)
	require.NoError(t, err)
	assert.Nil(t, got, "one place: nothing to say")
	_, err = PlaceNew([]string{EnvDev, "stage", EnvProd}, "test", nil)
	require.EqualError(t, err, "say which environment test goes after: dev or stage")
	got, err = PlaceNew([]string{EnvDev, "stage", EnvProd}, "test", &Place{After: "stage"})
	require.NoError(t, err)
	assert.Equal(t, &Place{After: "stage"}, got)
	_, err = PlaceNew([]string{EnvDev, "stage", EnvProd}, "test", &Place{After: EnvProd})
	require.ErrorContains(t, err, "nothing goes after it")
	got, err = PlaceNew([]string{EnvDev, "stage"}, EnvProd, nil)
	require.NoError(t, err, "an empty prod added last needs no place")
	assert.Nil(t, got)
}

func TestSitePassedGated(t *testing.T) {
	st := &Site{Environments: []Environment{{Name: EnvDev}, {Name: "qa"}, {Name: "stage", RequiresApproval: true}, {Name: EnvProd}}}
	before := []string{EnvDev, "qa", "stage", EnvProd}
	assert.Equal(t, "stage", st.PassedGated(before, []string{EnvDev, "stage", "qa", EnvProd}, "qa"), "moving forward past a gated environment")
	assert.Empty(t, st.PassedGated(before, before, "qa"), "staying put")
	assert.Empty(t, st.PassedGated([]string{EnvDev, "stage", "qa", EnvProd}, before, "qa"), "moving back before it")
}

func TestDomainPublicStampAndApprovalOff(t *testing.T) {
	at := time.Date(2026, 11, 12, 1, 2, 3, 4, time.FixedZone("x", 3600))
	assert.Equal(t, "public:2026-11-12T00:02:03.000000004Z", (&Domain{AccessChangedAt: at}).PublicStamp())
	yes, no := true, false
	gated := &Environment{RequiresApproval: true}
	assert.True(t, gated.TakesApprovalOff(&no))
	assert.False(t, gated.TakesApprovalOff(&yes))
	assert.False(t, gated.TakesApprovalOff(nil))
	assert.False(t, (&Environment{}).TakesApprovalOff(&no))
}
