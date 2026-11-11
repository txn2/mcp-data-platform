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

// Making an environment public is the owner's or an administrator's, never a
// manager's, and dev never (#2604).
func TestSetEnvironment_WhoMakesItPublic(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		actor  Actor
		env    string
		change EnvironmentChange
		allow  bool
		msg    string
	}{
		{"the owner", owner, "stage", EnvironmentChange{Public: new(true)}, true, ""},
		{"an administrator", admin, "stage", EnvironmentChange{Public: new(true)}, true, ""},
		{"a manager", manager, "stage", EnvironmentChange{Public: new(true)}, false, "makes an environment public"},
		{"a manager creating it public", manager, "qa", EnvironmentChange{Public: new(true)}, false, "makes an environment public"},
		{"the owner on dev", owner, site.EnvDev, EnvironmentChange{Public: new(true)}, false, "always private"},
		{"an administrator on dev", admin, site.EnvDev, EnvironmentChange{Public: new(true)}, false, "always private"},
		{"dev by its old name", admin, site.EnvAlias, EnvironmentChange{Public: new(true)}, false, "always private"},
		{"approval on dev", admin, site.EnvDev, EnvironmentChange{RequiresApproval: new(true)}, false, "never requires approval"},
		{"a stranger", stranger, "stage", EnvironmentChange{Public: new(false)}, false, "changed by its owner"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _, _ := fixture(t)
			grant(store, manager.Email, site.LevelManage)
			got, err := svc.SetEnvironment(ctx, tc.actor, "marketing", tc.env, tc.change)
			if tc.allow {
				require.NoError(t, err)
				assert.Equal(t, StatusDone, got.Status)
				assert.True(t, store.sites[siteID].Environment(tc.env).Public)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.msg)
			if e := store.sites[siteID].Environment(tc.env); e != nil {
				assert.False(t, e.Public, "nothing was made public")
			}
		})
	}
}

func TestSetEnvironment_AManagerMakesItPrivateAndSetsInternalView(t *testing.T) {
	svc, store, _, _ := fixture(t)
	grant(store, manager.Email, site.LevelManage)
	store.sites[siteID].Environment("stage").Public = true
	got, err := svc.SetEnvironment(context.Background(), manager, "marketing", "stage", EnvironmentChange{Public: new(false), InternalView: new(true)})
	require.NoError(t, err)
	assert.False(t, got.Site.Environment("stage").Public)
	assert.True(t, got.Site.Environment("stage").InternalView)
}

// Where an environment requires approval, a non-administrator's request to
// make it public waits in Approvals, fixed to the access it was asked
// against, and goes through once approved; an administrator's needs none.
func TestSetEnvironment_PublicThroughApprovals(t *testing.T) {
	ctx := context.Background()
	svc, store, _, approvals := fixture(t)
	var invalidated []string
	svc.Invalidate = func(h string) { invalidated = append(invalidated, h) }
	stamp := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	store.sites[siteID].Environment(envProd).AccessChangedAt = stamp

	waiting, err := svc.SetEnvironment(ctx, owner, "marketing", envProd, EnvironmentChange{Public: new(true), Reason: "launch"})
	require.NoError(t, err)
	assert.Equal(t, StatusAwaitingApproval, waiting.Status)
	require.NotNil(t, waiting.Approval)
	assert.False(t, store.sites[siteID].Environment(envProd).Public, "prod stays private while it waits")
	require.Len(t, approvals.envSubmitted, 1)
	assert.Equal(t, approval.SubmitEnvironment{SiteID: siteID, Site: "marketing", Target: envProd, Stamp: "public:" + stamp.Format(time.RFC3339Nano), Reason: "launch"}, approvals.envSubmitted[0])
	assert.Equal(t, [][3]string{{siteID, envProd, "public:" + stamp.Format(time.RFC3339Nano)}}, approvals.envAsked)

	approvals.envApproved = true
	done, err := svc.SetEnvironment(ctx, owner, "marketing", envProd, EnvironmentChange{Public: new(true)})
	require.NoError(t, err)
	assert.Equal(t, StatusDone, done.Status)
	assert.True(t, store.sites[siteID].Environment(envProd).Public)
	assert.Len(t, approvals.envSubmitted, 1, "an approved request files nothing more")
	assert.Contains(t, invalidated, "marketing")

	svc2, store2, _, approvals2 := fixture(t)
	_, err = svc2.SetEnvironment(ctx, admin, "marketing", envProd, EnvironmentChange{Public: new(true)})
	require.NoError(t, err)
	assert.True(t, store2.sites[siteID].Environment(envProd).Public, "an administrator's change needs no request")
	assert.Empty(t, approvals2.envSubmitted)
}

// A new environment asked for public and requiring approval is created
// private, and the request is filed against it once it exists.
func TestSetEnvironment_ANewGatedEnvironmentIsCreatedPrivateAndAsks(t *testing.T) {
	svc, store, _, approvals := fixture(t)
	got, err := svc.SetEnvironment(context.Background(), owner, "marketing", "launch", EnvironmentChange{Public: new(true), RequiresApproval: new(true)})
	require.NoError(t, err)
	assert.Equal(t, StatusAwaitingApproval, got.Status)
	require.NotNil(t, got.Approval)
	assert.Equal(t, "apr_env", got.Approval.ID)
	e := store.sites[siteID].Environment("launch")
	require.NotNil(t, e)
	assert.False(t, e.Public)
	assert.True(t, e.RequiresApproval)
	require.Len(t, approvals.envSubmitted, 1)
	assert.Equal(t, "launch", approvals.envSubmitted[0].Target)
}

func TestSetEnvironment_ApprovalFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("no approvals queue", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		svc.Approvals = nil
		_, err := svc.SetEnvironment(ctx, owner, "marketing", envProd, EnvironmentChange{Public: new(true)})
		var r *RefusedError
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "no approvals queue")
		assert.False(t, store.sites[siteID].Environment(envProd).Public)
	})
	t.Run("the queue refuses", func(t *testing.T) {
		svc, store, _, approvals := fixture(t)
		approvals.failSubmit = errors.New("down")
		_, err := svc.SetEnvironment(ctx, owner, "marketing", envProd, EnvironmentChange{Public: new(true)})
		require.ErrorContains(t, err, "requesting approval")
		assert.False(t, store.sites[siteID].Environment(envProd).Public)
	})
	t.Run("the store refuses", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.failSetEnv = errors.New("down")
		_, err := svc.SetEnvironment(ctx, owner, "marketing", "stage", EnvironmentChange{InternalView: new(true)})
		require.ErrorContains(t, err, "down")
	})
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
	svc.Apps = fakeApps{"marketing--preview": {ID: "app_1", Name: "marketing--preview"}}
	_, err := svc.SetEnvironment(ctx, owner, "marketing", "preview", EnvironmentChange{})
	require.ErrorContains(t, err, "name of an application", "an application already holding the label keeps it")

	long := "review-copy-for-the-quarterly-board-meeting-x"
	_, err = svc.SetEnvironment(ctx, owner, "marketing", long[:32], EnvironmentChange{})
	require.NoError(t, err, "marketing--<32> is 43 characters")

	ms, ok := svc.Store.(*memStore)
	require.True(t, ok)
	ms.sites[siteID].Name = "employee-benefits-open-enrollment-2026-information-hub"
	_, err = svc.SetEnvironment(ctx, owner, siteID, "review-copy", EnvironmentChange{})
	require.ErrorContains(t, err, "at most 63")
}

func TestCreate_TheDevAddressMustBeALabel(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := fixture(t)
	got, err := svc.Create(ctx, owner, "intranet", "")
	require.NoError(t, err)
	assert.Equal(t, site.EnvDev, got.MainEnvironment, "a new site gives dev the main address")
	require.Len(t, got.Environments, 1)
	assert.Equal(t, site.EnvDev, got.Environments[0].Name)
	assert.False(t, got.Environments[0].Public)

	_, err = svc.Create(ctx, owner, "xn", "")
	require.ErrorContains(t, err, "cannot be named xn")
	_, err = svc.Create(ctx, owner, "a234567890123456789012345678901234567890123456789012345678x", "")
	require.ErrorContains(t, err, "at most 63", "a 59-character name leaves no room for --dev")

	svc.Apps = fakeApps{"blog--dev": {ID: "app_1", Name: "blog--dev"}}
	_, err = svc.Create(ctx, owner, "blog", "")
	require.ErrorContains(t, err, "name of an application", "an application named blog--dev keeps that address")
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
// for them: every public one, and each private one by email, persona,
// ownership or internal view.
func TestOpenableEnvironments(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := fixture(t)
	svc.External = func(_ context.Context, email string) bool { return email == "pat@client.example" }
	st := store.sites[siteID]
	st.Environment(envProd).Public = true
	store.grants[siteID] = map[string]site.Grant{
		viewer.Email:           {Email: viewer.Email, Level: site.LevelView, Environments: []string{"stage"}},
		"persona:finance-team": {Persona: "finance-team", Level: site.LevelView, Environments: []string{site.EnvDev}},
	}
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
	assert.Equal(t, []string{"stage", envProd}, names(viewer), "their grant opens stage, prod is public")
	assert.Equal(t, []string{envProd}, names(unverified(viewer)), "an unverified email matches no grant")
	assert.Equal(t, []string{site.EnvDev, envProd}, names(Actor{Email: "x@example.com", Persona: "finance-team"}), "the persona grant opens dev")
	assert.Equal(t, []string{site.EnvDev, "stage", envProd}, names(owner), "the owner opens every private environment")
	assert.Equal(t, []string{site.EnvDev, "stage", envProd}, names(admin))
	assert.Equal(t, []string{envProd}, names(stranger))

	st.Environment("stage").InternalView = true
	assert.Equal(t, []string{"stage", envProd}, names(Actor{Email: "staff@example.com", Persona: "analyst"}), "internal view opens stage to staff")
	assert.Equal(t, []string{envProd}, names(Actor{Email: "pat@client.example", Persona: "client"}), "but not to an external person")
	assert.Equal(t, []string{envProd}, names(Actor{Email: "nopersona@example.com"}), "nor to someone with no persona")

	store.failGrant = errors.New("down")
	_, err := svc.OpenableEnvironments(ctx, viewer, st)
	require.Error(t, err)
}

// A domain that creates its environment creates it private unless the owner or
// an administrator asks for public; one that requires approval is made public
// through Approvals instead (#2604).
func TestAddDomain_TheEnvironmentItCreates(t *testing.T) {
	ctx := context.Background()
	t.Run("private by default", func(t *testing.T) {
		svc, store := domainFixture(t)
		grant(store, manager.Email, site.LevelManage)
		_, err := svc.AddDomain(ctx, manager, DomainRequest{Hostname: "review.example.org", Site: "marketing", Environment: "review"})
		require.NoError(t, err)
		assert.False(t, store.sites[siteID].Environment("review").Public, "an agent adding a domain never makes anything public")
	})
	t.Run("public when the owner asks", func(t *testing.T) {
		svc, store := domainFixture(t)
		_, err := svc.AddDomain(ctx, owner, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "launch", Public: true})
		require.NoError(t, err)
		assert.True(t, store.sites[siteID].Environment("launch").Public)
	})
	t.Run("never public when a manager asks", func(t *testing.T) {
		svc, store := domainFixture(t)
		grant(store, manager.Email, site.LevelManage)
		_, err := svc.AddDomain(ctx, manager, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "launch", Public: true})
		require.ErrorIs(t, err, site.ErrNotAllowed)
		assert.Nil(t, store.sites[siteID].Environment("launch"))
		assert.Empty(t, store.domains)
	})
	t.Run("public and gated goes through Approvals", func(t *testing.T) {
		svc, store := domainFixture(t)
		_, err := svc.AddDomain(ctx, owner, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "launch", Public: true, RequiresApproval: true})
		var r *RefusedError
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "made public through Approvals")
		assert.Nil(t, store.sites[siteID].Environment("launch"))
	})
	t.Run("an administrator may create it public and gated", func(t *testing.T) {
		svc, store := domainFixture(t)
		_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "launch", Public: true, RequiresApproval: true})
		require.NoError(t, err)
		e := store.sites[siteID].Environment("launch")
		assert.True(t, e.Public)
		assert.True(t, e.RequiresApproval)
	})
	t.Run("an environment whose address would not be a label", func(t *testing.T) {
		svc, store := domainFixture(t)
		store.sites[siteID].Name = "employee-benefits-open-enrollment-2026-information-hub"
		_, err := svc.AddDomain(ctx, owner, DomainRequest{Hostname: "www.example.org", Site: siteID, Environment: "review-copy"})
		require.ErrorContains(t, err, "at most 63")
	})
}

func TestRequestPublic_TheEnvironmentGoneBeforeTheRequest(t *testing.T) {
	svc, _, _, _ := fixture(t)
	_, err := svc.requestPublic(context.Background(), owner, &site.Site{ID: siteID, Name: "marketing"}, "gone", "")
	require.ErrorIs(t, err, site.ErrNoEnvironment)
	_, err = svc.requestPublic(context.Background(), owner, &site.Site{ID: "site_none", Name: "x"}, envProd, "")
	require.ErrorIs(t, err, site.ErrNotFound)
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
