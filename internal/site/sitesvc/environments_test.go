package sitesvc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/site"
)

// Public is a setting of a domain (#2619): asking for it on an environment is
// refused for everyone, administrators included, naming the domain setting;
// dev never requires approval; and a stranger changes nothing.
func TestSetEnvironment_PublicIsADomainSetting(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		actor  Actor
		env    string
		change EnvironmentChange
		msg    string
	}{
		{"the owner", owner, "stage", EnvironmentChange{Public: new(true)}, "public is a setting of a domain"},
		{"an administrator", admin, envProd, EnvironmentChange{Public: new(true)}, "set public on it (add_domain with public, or set_domain)"},
		{"making it private", admin, "stage", EnvironmentChange{Public: new(false)}, "public is a setting of a domain"},
		{"dev by its old name", admin, site.EnvAlias, EnvironmentChange{Public: new(true)}, "every address of dev on the pages domain asks for a sign-in"},
		{"approval on dev", admin, site.EnvDev, EnvironmentChange{RequiresApproval: new(true)}, "never requires approval"},
		{"a stranger", stranger, "stage", EnvironmentChange{InternalView: new(true)}, "changed by its owner"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _, approvals := fixture(t)
			_, err := svc.SetEnvironment(ctx, tc.actor, "marketing", tc.env, tc.change)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.msg)
			assert.False(t, store.sites[siteID].Environment("stage").InternalView, "nothing changed")
			assert.Empty(t, approvals.envSubmitted)
		})
	}
}

func TestSetEnvironment_AManagerSetsInternalView(t *testing.T) {
	svc, store, _, _ := fixture(t)
	grant(store, manager.Email, site.LevelManage)
	got, err := svc.SetEnvironment(context.Background(), manager, "marketing", "stage", EnvironmentChange{InternalView: new(true)})
	require.NoError(t, err)
	assert.Equal(t, StatusDone, got.Status)
	assert.True(t, got.Site.Environment("stage").InternalView)
}

// order is the site's environments by name, in its order.
func order(store *memStore) []string { return store.sites[siteID].Order() }

// A new environment is placed between dev and prod (#2617): after the
// environment it is asked to follow, or, when there is one place, there; a
// place after prod is refused.
func TestSetEnvironment_PlacesANewEnvironment(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := fixture(t)
	_, err := svc.SetEnvironment(ctx, owner, "marketing", "qa", EnvironmentChange{})
	require.ErrorContains(t, err, "say which environment qa goes after: dev or stage")
	assert.Equal(t, []string{site.EnvDev, "stage", envProd}, order(store), "a new environment with two places waits to be told")

	_, err = svc.SetEnvironment(ctx, owner, "marketing", "qa", EnvironmentChange{Place: &site.Place{After: site.EnvDev}})
	require.NoError(t, err)
	assert.Equal(t, []string{site.EnvDev, "qa", "stage", envProd}, order(store))

	refusals := []struct {
		place site.Place
		want  string
	}{
		{site.Place{After: envProd}, "prod is always last: nothing goes after it"},
		{site.Place{After: "nope"}, "no such environment"},
		{site.Place{After: "uat"}, "uat cannot go after itself"},
	}
	for _, r := range refusals {
		_, err := svc.SetEnvironment(ctx, owner, "marketing", "uat", EnvironmentChange{Place: &r.place})
		require.ErrorContains(t, err, r.want, r.place)
	}
	assert.Nil(t, store.sites[siteID].Environment("uat"))

	two, twoStore, _, _ := fixture(t)
	twoStore.sites[siteID].Environments = []site.Environment{{Name: site.EnvDev}, {Name: envProd}}
	_, err = two.SetEnvironment(ctx, owner, "marketing", "stage", EnvironmentChange{})
	require.NoError(t, err, "with dev and prod alone there is one place")
	assert.Equal(t, []string{site.EnvDev, "stage", envProd}, order(twoStore))
}

// An environment between dev and prod can be moved later, and its place is
// kept; dev and prod cannot; moving one forward past an environment that
// requires approval is an administrator's (#2617).
func TestSetEnvironment_MovesAnEnvironment(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := fixture(t)
	grant(store, manager.Email, site.LevelManage)
	_, err := svc.SetEnvironment(ctx, owner, "marketing", "qa", EnvironmentChange{Place: &site.Place{After: site.EnvDev}})
	require.NoError(t, err)

	got, err := svc.SetEnvironment(ctx, manager, "marketing", "qa", EnvironmentChange{Place: &site.Place{After: "stage"}})
	require.NoError(t, err)
	assert.Equal(t, []string{site.EnvDev, "stage", "qa", envProd}, got.Site.Order())

	_, err = svc.SetEnvironment(ctx, admin, "marketing", envProd, EnvironmentChange{Place: &site.Place{After: site.EnvDev}})
	require.ErrorContains(t, err, "prod is always last, so it cannot be moved")
	_, err = svc.SetEnvironment(ctx, admin, "marketing", site.EnvDev, EnvironmentChange{Place: &site.Place{After: "stage"}})
	require.ErrorContains(t, err, "dev is always first, so it cannot be moved")

	store.sites[siteID].Environment("stage").RequiresApproval = true
	_, err = svc.SetEnvironment(ctx, manager, "marketing", "qa", EnvironmentChange{Place: &site.Place{After: site.EnvDev}})
	require.NoError(t, err, "moving back before a gated environment puts the gate after it")
	_, err = svc.SetEnvironment(ctx, manager, "marketing", "qa", EnvironmentChange{Place: &site.Place{After: "stage"}})
	require.ErrorIs(t, err, site.ErrNotAllowed, "moving forward past stage would feed prod a release stage never approved")
	assert.Contains(t, err.Error(), "moves qa past stage")
	assert.Equal(t, []string{site.EnvDev, "qa", "stage", envProd}, order(store))
	_, err = svc.SetEnvironment(ctx, admin, "marketing", "qa", EnvironmentChange{Place: &site.Place{After: "stage"}})
	require.NoError(t, err)
	assert.Equal(t, []string{site.EnvDev, "stage", "qa", envProd}, order(store))
}

func TestSetEnvironment_StoreRefusals(t *testing.T) {
	svc, store, _, _ := fixture(t)
	store.failSetEnv = errors.New("down")
	_, err := svc.SetEnvironment(context.Background(), owner, "marketing", "stage", EnvironmentChange{InternalView: new(true)})
	require.ErrorContains(t, err, "down")
}

func TestSetEnvironment_TheOldNameCarriesANote(t *testing.T) {
	svc, store, _, _ := fixture(t)
	got, err := svc.SetEnvironment(context.Background(), owner, "marketing", site.EnvAlias, EnvironmentChange{InternalView: new(true)})
	require.NoError(t, err)
	assert.Equal(t, site.AliasNote, got.Note)
	assert.True(t, store.sites[siteID].Environment(site.EnvDev).InternalView, "the change landed on dev")
}

func TestSetEnvironment_ANewEnvironmentsAddressIsChecked(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := fixture(t)
	after := &site.Place{After: "stage"}
	svc.Apps = fakeApps{"marketing--preview": {ID: "app_1", Name: "marketing--preview"}}
	_, err := svc.SetEnvironment(ctx, owner, "marketing", "preview", EnvironmentChange{Place: after})
	require.ErrorContains(t, err, "name of an application", "an application already holding the label keeps it")

	long := "review-copy-for-the-quarterly-board-meeting-x"
	_, err = svc.SetEnvironment(ctx, owner, "marketing", long[:32], EnvironmentChange{Place: after})
	require.NoError(t, err, "marketing--<32> is 43 characters")

	ms, ok := svc.Store.(*memStore)
	require.True(t, ok)
	ms.sites[siteID].Name = "employee-benefits-open-enrollment-2026-information-hub"
	_, err = svc.SetEnvironment(ctx, owner, siteID, "review-copy", EnvironmentChange{Place: after})
	require.ErrorContains(t, err, "at most 63")
}

// A new site starts with dev and prod, prod holding the main address
// (#2617); both addresses must be DNS labels no application holds.
func TestCreate_DevAndProd(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := fixture(t)
	got, err := svc.Create(ctx, owner, "d4l", "")
	require.NoError(t, err)
	assert.Equal(t, envProd, got.MainEnvironment, "prod holds the main address")
	assert.Equal(t, []string{site.EnvDev, envProd}, got.Order())
	assert.Empty(t, got.Environment(envProd).PublicDomains, "nothing answers anyone")

	_, err = svc.Create(ctx, owner, "xn", "")
	require.ErrorContains(t, err, "international domain name")
	_, err = svc.Create(ctx, owner, "ab", "")
	require.ErrorContains(t, err, "a site name cannot be two characters", "ab--dev is a label certificate authorities refuse (#2611)")
	_, err = svc.Create(ctx, owner, "a234567890123456789012345678901234567890123456789012345678x", "")
	require.ErrorContains(t, err, "at most 63", "a 59-character name leaves no room for --dev")
	_, err = svc.Create(ctx, owner, "a23456789012345678901234567890123456789012345678901234567x", "")
	require.ErrorContains(t, err, "at most 63", "a 58-character name leaves room for --dev and none for --prod")

	svc.Apps = fakeApps{"blog--prod": {ID: "app_1", Name: "blog--prod"}}
	_, err = svc.Create(ctx, owner, "blog", "")
	require.ErrorContains(t, err, "name of an application", "an application named blog--prod keeps that address")
	assert.Len(t, store.sites, 2)
}

func TestSetMainEnvironment(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := fixture(t)
	grant(store, manager.Email, site.LevelManage)
	var invalidated []string
	svc.Invalidate = func(h string) { invalidated = append(invalidated, h) }

	got, err := svc.SetMainEnvironment(ctx, owner, "marketing", envProd)
	require.NoError(t, err)
	assert.Equal(t, envProd, got.MainEnvironment)
	assert.Equal(t, []string{"marketing"}, invalidated)

	_, err = svc.SetMainEnvironment(ctx, manager, "marketing", site.EnvDev)
	require.ErrorIs(t, err, site.ErrNotAllowed, "moving the main address is the owner's or an administrator's")
	assert.Equal(t, envProd, store.sites[siteID].MainEnvironment)

	got, err = svc.SetMainEnvironment(ctx, admin, "marketing", " default ")
	require.NoError(t, err)
	assert.Equal(t, site.EnvDev, got.MainEnvironment, "the old name of dev is read as dev")

	got, err = svc.SetMainEnvironment(ctx, owner, "marketing", "")
	require.NoError(t, err)
	assert.Empty(t, got.MainEnvironment, "to none")

	_, err = svc.SetMainEnvironment(ctx, owner, "marketing", "qa")
	require.ErrorIs(t, err, site.ErrNoEnvironment)
	_, err = svc.SetMainEnvironment(ctx, owner, "nope", "")
	require.ErrorIs(t, err, site.ErrNotFound)
}

// A persona is shared view of named environments; a manage grant is
// site-wide and to a person.
func TestGrant_PersonasAndEnvironments(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := fixture(t)
	grant(store, manager.Email, site.LevelManage)
	svc.PersonaExists = func(name string) bool { return name == "finance-team" }

	got, err := svc.Grant(ctx, manager, "marketing", GrantRequest{Persona: " finance-team ", Environments: []string{envProd, "default", envProd}})
	require.NoError(t, err, "anyone who manages the site shares a persona view")
	assert.Equal(t, site.Grant{Persona: "finance-team", Level: site.LevelView, Environments: []string{envProd, site.EnvDev}, GrantedBy: manager.Email, CreatedAt: got.CreatedAt}, *got,
		"trimmed, view, the old name read as dev, each environment once")

	cases := []struct {
		name string
		req  GrantRequest
		want string
	}{
		{"a persona that does not exist", GrantRequest{Persona: "nobody"}, `persona "nobody" does not exist`},
		{"a persona to manage", GrantRequest{Persona: "finance-team", Level: site.LevelManage}, "manage is given to a person"},
		{"both principals", GrantRequest{Email: "x@example.com", Persona: "finance-team", Level: site.LevelView}, "not both"},
		{"neither principal", GrantRequest{Level: site.LevelView}, "not both"},
		{"a manage grant narrowed", GrantRequest{Email: "x@example.com", Level: site.LevelManage, Environments: []string{envProd}}, "a manage grant is for the whole site"},
		{"an environment the site lacks", GrantRequest{Email: "x@example.com", Level: site.LevelView, Environments: []string{"qa"}}, "no such environment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Grant(ctx, owner, "marketing", tc.req)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	_, err = svc.Grant(ctx, stranger, "marketing", GrantRequest{Persona: "finance-team"})
	require.ErrorIs(t, err, site.ErrNotAllowed, "a stranger shares nothing")

	require.NoError(t, svc.Revoke(ctx, manager, "marketing", "", "finance-team"))
	assert.NotContains(t, store.grants[siteID], "persona:finance-team")
	require.Error(t, svc.Revoke(ctx, manager, "marketing", "", ""), "name an email or a persona")
	require.Error(t, svc.Revoke(ctx, manager, "marketing", "x@example.com", "finance-team"))
}

// Someone who does not manage the site is shown the environments that open
// for them: each one with a public domain, and each other by email, persona,
// ownership or internal view.
func TestOpenableEnvironments(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := fixture(t)
	svc.External = func(_ context.Context, email string) bool { return email == "pat@client.example" }
	store.domains["www.example.org"] = &site.Domain{Hostname: "www.example.org", SiteID: siteID, Environment: envProd, Public: true}
	store.grants[siteID] = map[string]site.Grant{
		viewer.Email:           {Email: viewer.Email, Level: site.LevelView, Environments: []string{"stage"}},
		"persona:finance-team": {Persona: "finance-team", Level: site.LevelView, Environments: []string{site.EnvDev}},
	}
	st, err := svc.Find(ctx, "marketing")
	require.NoError(t, err)
	names := func(a Actor) []string {
		t.Helper()
		got, err := svc.OpenableEnvironments(ctx, a, st)
		require.NoError(t, err)
		out := make([]string, 0, len(got))
		for _, e := range got {
			out = append(out, e.Name)
		}
		return out
	}
	assert.Equal(t, []string{"stage", envProd}, names(viewer), "their grant opens stage, prod has a public domain")
	assert.Equal(t, []string{envProd}, names(unverified(viewer)), "an unverified email matches no grant")
	assert.Equal(t, []string{site.EnvDev, envProd}, names(Actor{Email: "x@example.com", Persona: "finance-team"}), "the persona grant opens dev")
	assert.Equal(t, []string{site.EnvDev, "stage", envProd}, names(owner), "the owner opens every environment")
	assert.Equal(t, []string{site.EnvDev, "stage", envProd}, names(admin))
	assert.Equal(t, []string{envProd}, names(stranger))

	st.Environment("stage").InternalView = true
	store.sites[siteID].Environment("stage").InternalView = true
	assert.Equal(t, []string{"stage", envProd}, names(Actor{Email: "staff@example.com", Persona: "analyst"}), "internal view opens stage to staff")
	assert.Equal(t, []string{envProd}, names(Actor{Email: "pat@client.example", Persona: "client"}), "but not to an external person")
	assert.Equal(t, []string{envProd}, names(Actor{Email: "nopersona@example.com"}), "nor to someone with no persona")

	store.failGrant = errors.New("down")
	_, err = svc.OpenableEnvironments(ctx, viewer, st)
	require.Error(t, err)
}

// A domain is added private unless the owner or an administrator asks for
// public (#2619); one on an environment that requires approval is added
// private by someone who is not an administrator, and the request goes to
// Approvals.
func TestAddDomain_Access(t *testing.T) {
	ctx := context.Background()
	t.Run("private by default", func(t *testing.T) {
		svc, store := domainFixture(t)
		grant(store, manager.Email, site.LevelManage)
		got, err := svc.AddDomain(ctx, manager, DomainRequest{Hostname: "review.example.org", Site: "marketing", Environment: "stage"})
		require.NoError(t, err)
		assert.False(t, got.Public, "an agent adding a domain never makes anything public")
		assert.Equal(t, StatusDone, got.Status)
	})
	t.Run("public when the owner asks", func(t *testing.T) {
		svc, store := domainFixture(t)
		got, err := svc.AddDomain(ctx, owner, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "stage", Public: true})
		require.NoError(t, err)
		assert.True(t, got.Public)
		assert.True(t, store.domains["www.example.org"].Public)
	})
	t.Run("never public when a manager asks", func(t *testing.T) {
		svc, store := domainFixture(t)
		grant(store, manager.Email, site.LevelManage)
		_, err := svc.AddDomain(ctx, manager, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "stage", Public: true})
		require.ErrorIs(t, err, site.ErrNotAllowed)
		assert.Empty(t, store.domains)
	})
	t.Run("public on a gated environment goes through Approvals", func(t *testing.T) {
		svc, store := domainFixture(t)
		approvals, _ := svc.Approvals.(*fakeApprovals)
		got, err := svc.AddDomain(ctx, owner, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: envProd, Public: true, Reason: "launch"})
		require.NoError(t, err)
		assert.Equal(t, StatusAwaitingApproval, got.Status)
		require.NotNil(t, got.Approval)
		assert.False(t, store.domains["www.example.org"].Public, "added private while it waits")
		require.Len(t, approvals.envSubmitted, 1)
		assert.Equal(t, "www.example.org", approvals.envSubmitted[0].Target, "the request names the domain")
		assert.Equal(t, "launch", approvals.envSubmitted[0].Reason)
	})
	t.Run("an administrator may make it public on a gated environment", func(t *testing.T) {
		svc, store := domainFixture(t)
		_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: envProd, Public: true})
		require.NoError(t, err)
		assert.True(t, store.domains["www.example.org"].Public)
	})
	t.Run("a new environment is placed", func(t *testing.T) {
		svc, store := domainFixture(t)
		_, err := svc.AddDomain(ctx, owner, DomainRequest{Hostname: "uat.example.org", Site: "marketing", Environment: "uat"})
		require.ErrorContains(t, err, "say which environment uat goes after")
		_, err = svc.AddDomain(ctx, owner, DomainRequest{Hostname: "uat.example.org", Site: "marketing", Environment: "uat", Place: &site.Place{After: "stage"}})
		require.NoError(t, err)
		assert.Equal(t, []string{site.EnvDev, "stage", "uat", envProd}, order(store))
	})
	t.Run("an application's domain has no public setting", func(t *testing.T) {
		svc, _ := domainFixture(t)
		_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "shop.example.org", Application: "shop", Public: true})
		require.ErrorContains(t, err, "answers by the application's own rules")
	})
	t.Run("an environment whose address would not be a label", func(t *testing.T) {
		svc, store := domainFixture(t)
		store.sites[siteID].Name = "employee-benefits-open-enrollment-2026-information-hub"
		_, err := svc.AddDomain(ctx, owner, DomainRequest{Hostname: "www.example.org", Site: siteID, Environment: "review-copy", Place: &site.Place{After: "stage"}})
		require.ErrorContains(t, err, "at most 63")
	})
}

// SetDomainAccess makes one domain public or private (#2619): public is the
// owner's or an administrator's, through Approvals on a gated environment and
// fixed to the access it was asked against; private is anyone's who manages
// the site.
func TestSetDomainAccess(t *testing.T) {
	ctx := context.Background()
	svc, store := domainFixture(t)
	approvals, _ := svc.Approvals.(*fakeApprovals)
	grant(store, manager.Email, site.LevelManage)
	stamp := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	store.domains["stage.example.org"] = &site.Domain{Hostname: "stage.example.org", SiteID: siteID, Environment: "stage"}
	store.domains["www.example.org"] = &site.Domain{Hostname: "www.example.org", SiteID: siteID, Environment: envProd, AccessChangedAt: stamp}
	store.domains["shop.example.org"] = &site.Domain{Hostname: "shop.example.org", ApplicationID: "app_shop"}
	var invalidated []string
	svc.Invalidate = func(h string) { invalidated = append(invalidated, h) }

	_, err := svc.SetDomainAccess(ctx, manager, "stage.example.org", true, "")
	require.ErrorIs(t, err, site.ErrNotAllowed, "a manager makes no domain public")
	got, err := svc.SetDomainAccess(ctx, owner, "STAGE.example.org", true, "")
	require.NoError(t, err)
	assert.True(t, got.Public)
	assert.Equal(t, []string{"stage.example.org"}, invalidated)
	got, err = svc.SetDomainAccess(ctx, manager, "stage.example.org", false, "")
	require.NoError(t, err, "anyone who manages the site makes a domain private")
	assert.False(t, got.Public)

	waiting, err := svc.SetDomainAccess(ctx, owner, "www.example.org", true, "launch")
	require.NoError(t, err)
	assert.Equal(t, StatusAwaitingApproval, waiting.Status)
	assert.False(t, store.domains["www.example.org"].Public, "it stays private while it waits")
	require.Len(t, approvals.envSubmitted, 1)
	assert.Equal(t, approval.SubmitEnvironment{SiteID: siteID, Site: "marketing", Target: "www.example.org", Stamp: "public:" + stamp.Format(time.RFC3339Nano), Reason: "launch"},
		approvals.envSubmitted[0])
	approvals.envApproved = true
	done, err := svc.SetDomainAccess(ctx, owner, "www.example.org", true, "")
	require.NoError(t, err)
	assert.Equal(t, StatusDone, done.Status)
	assert.True(t, store.domains["www.example.org"].Public)
	assert.Len(t, approvals.envSubmitted, 1, "an approved request files nothing more")

	_, err = svc.SetDomainAccess(ctx, admin, "shop.example.org", true, "")
	require.ErrorContains(t, err, "maps to an application")
	_, err = svc.SetDomainAccess(ctx, admin, "none.example.org", true, "")
	require.ErrorIs(t, err, site.ErrNoDomain)
	_, err = svc.SetDomainAccess(ctx, stranger, "stage.example.org", false, "")
	require.ErrorIs(t, err, site.ErrNotAllowed)

	again, err := svc.SetDomainAccess(ctx, owner, "www.example.org", true, "")
	require.NoError(t, err)
	assert.Equal(t, StatusDone, again.Status, "setting what it already is changes nothing")
}

func TestSetDomainAccess_ApprovalFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("no approvals queue", func(t *testing.T) {
		svc, store := domainFixture(t)
		svc.Approvals = nil
		store.domains["www.example.org"] = &site.Domain{Hostname: "www.example.org", SiteID: siteID, Environment: envProd}
		_, err := svc.SetDomainAccess(ctx, owner, "www.example.org", true, "")
		require.ErrorContains(t, err, "no approvals queue")
		assert.False(t, store.domains["www.example.org"].Public)
	})
	t.Run("the queue refuses", func(t *testing.T) {
		svc, store := domainFixture(t)
		approvals, _ := svc.Approvals.(*fakeApprovals)
		approvals.failSubmit = errors.New("down")
		store.domains["www.example.org"] = &site.Domain{Hostname: "www.example.org", SiteID: siteID, Environment: envProd}
		_, err := svc.SetDomainAccess(ctx, owner, "www.example.org", true, "")
		require.ErrorContains(t, err, "requesting approval")
		assert.False(t, store.domains["www.example.org"].Public)
	})
}

func TestSetMainEnvironment_TheStoreRefuses(t *testing.T) {
	svc, store, _, _ := fixture(t)
	store.sites[siteID].Environments = append(store.sites[siteID].Environments, site.Environment{Name: "x"})
	_, err := svc.SetMainEnvironment(context.Background(), owner, "marketing", "x")
	require.NoError(t, err)
	delete(store.sites, siteID)
	_, err = svc.SetMainEnvironment(context.Background(), owner, siteID, "")
	require.ErrorIs(t, err, site.ErrNotFound)
}
