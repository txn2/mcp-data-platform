package sitesvc

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
)

const (
	siteID  = "site_1"
	envProd = "prod"
)

var (
	owner    = Actor{Email: "jane@example.com", Persona: "analyst"}
	manager  = Actor{Email: "mo@example.com", Persona: "analyst"}
	viewer   = Actor{Email: "vi@client.example"}
	stranger = Actor{Email: "joe@example.com"}
	admin    = Actor{Email: "admin@example.com", Persona: "admin", Admin: true}
)

// fixture is a service over one site owned by jane with default, stage and a
// prod that requires approval.
//
//nolint:revive // a fixture hands back each fake a test drives
func fixture(t *testing.T) (*Service, *memStore, *memObjects, *fakeApprovals) {
	t.Helper()
	store, objects, approvals := newMem(), newObjects(), &fakeApprovals{}
	store.sites[siteID] = &site.Site{
		ID: siteID, Name: "marketing", OwnerEmail: owner.Email, MainEnvironment: site.EnvDev,
		Environments: []site.Environment{{Name: site.EnvDev}, {Name: "stage"}, {Name: envProd, RequiresApproval: true}},
	}
	return &Service{
		Store: store, Objects: objects, Bucket: "portal", Approvals: approvals,
		PublicScheme: "https",
	}, store, objects, approvals
}

// released files release number n of the site and, when env is named, points
// the environment at it.
func released(store *memStore, n int, env string) *site.Release {
	r := site.Release{ID: "srel_" + strconv.Itoa(n), SiteID: siteID, Number: n, ManifestSHA256: "digest-" + strconv.Itoa(n)}
	store.releases[siteID] = append([]site.Release{r}, store.releases[siteID]...)
	if env != "" {
		e := store.sites[siteID].Environment(env)
		store.deployments[siteID] = append([]site.Deployment{{Environment: env, Action: site.ActionPublish, From: e.Release, To: n}}, store.deployments[siteID]...)
		e.Release = n
	}
	return &r
}

func grant(store *memStore, email, level string) {
	if store.grants[siteID] == nil {
		store.grants[siteID] = map[string]site.Grant{}
	}
	store.grants[siteID][email] = site.Grant{Email: email, Level: level}
}

func unverified(a Actor) Actor {
	a.EmailUnverified = true
	return a
}

func TestCreate(t *testing.T) {
	svc, store, _, _ := fixture(t)
	ctx := context.Background()
	got, err := svc.Create(ctx, owner, "blog", "the blog")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(got.ID, "site_"))
	assert.Equal(t, owner.Email, got.OwnerEmail)
	assert.Equal(t, owner.Email, got.CreatedBy)
	assert.Equal(t, []string{site.EnvDev, envProd}, got.Order(), "a new site starts with dev and prod")
	assert.False(t, got.Environments[0].RequiresApproval)
	assert.Len(t, store.sites, 2)

	var r *RefusedError
	_, err = svc.Create(ctx, Actor{}, "other", "")
	require.ErrorAs(t, err, &r, "a site needs an owner")
	_, err = svc.Create(ctx, owner, "Not A Name", "")
	require.ErrorAs(t, err, &r)
	_, err = svc.Create(ctx, owner, "blog", "")
	require.ErrorIs(t, err, site.ErrNameTaken)
}

func TestCreate_ANameAnApplicationHoldsIsRefused(t *testing.T) {
	svc, store, _, _ := fixture(t)
	svc.Apps = fakeApps{"shop": {ID: "app_shop", Name: "shop"}}

	_, err := svc.Create(context.Background(), owner, "shop", "")
	var r *RefusedError
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Msg, "the name of an application")
	assert.Len(t, store.sites, 1, "nothing was created")

	got, err := svc.Create(context.Background(), owner, "blog", "")
	require.NoError(t, err)
	assert.Equal(t, "blog", got.Name, "a name no application holds is free")
}

func TestFind_ReadsByIDOrName(t *testing.T) {
	svc, _, _, _ := fixture(t)
	ctx := context.Background()
	byName, err := svc.Find(ctx, "marketing")
	require.NoError(t, err)
	assert.Equal(t, siteID, byName.ID)
	byID, err := svc.Find(ctx, siteID)
	require.NoError(t, err)
	assert.Equal(t, "marketing", byID.Name)
	_, err = svc.Find(ctx, "site_none")
	require.ErrorIs(t, err, site.ErrNotFound)
	_, err = svc.Find(ctx, "nope")
	require.ErrorIs(t, err, site.ErrNotFound)
}

func TestUpdate(t *testing.T) {
	svc, store, _, _ := fixture(t)
	ctx := context.Background()
	store.grants[siteID] = map[string]site.Grant{manager.Email: {Email: manager.Email, Level: site.LevelManage}}

	got, err := svc.Update(ctx, owner, "marketing", UpdateRequest{Description: new("new words")})
	require.NoError(t, err)
	assert.Equal(t, "new words", got.Description)
	assert.Equal(t, owner.Email, got.OwnerEmail, "a nil owner leaves it")

	got, err = svc.Update(ctx, admin, siteID, UpdateRequest{Owner: new("  NEW@Example.com ")})
	require.NoError(t, err)
	assert.Equal(t, "new words", got.Description, "a nil description leaves it")
	assert.Equal(t, "new@example.com", store.sites[siteID].OwnerEmail, "an owner is stored normalized")

	cases := []struct {
		name  string
		actor Actor
		req   UpdateRequest
		want  error
	}{
		{"a stranger's description", stranger, UpdateRequest{Description: new("x")}, site.ErrNotAllowed},
		{"a manager's description", manager, UpdateRequest{Description: new("x")}, site.ErrNotAllowed},
		{"a manager's owner", manager, UpdateRequest{Owner: new("x@example.com")}, site.ErrNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Update(ctx, tc.actor, "marketing", tc.req)
			require.ErrorIs(t, err, tc.want)
			assert.Equal(t, "new words", store.sites[siteID].Description)
		})
	}

	_, err = svc.Update(ctx, admin, "nope", UpdateRequest{Description: new("x")})
	require.ErrorIs(t, err, site.ErrNotFound)
}

func TestUpdate_Refusals(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		req  UpdateRequest
		want string
	}{
		{"a description past its bound", UpdateRequest{Description: new(strings.Repeat("x", site.MaxDescription+1))}, "description"},
		{"an owner that is not an email", UpdateRequest{Owner: new("jane")}, "owner: an email address"},
		{"an owner who is external", UpdateRequest{Owner: new("Pat@Client.example")}, "only internal people can own a site"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _, _ := fixture(t)
			svc.External = func(_ context.Context, email string) bool { return email == "pat@client.example" }
			_, err := svc.Update(ctx, admin, "marketing", tc.req)
			var r *RefusedError
			require.ErrorAs(t, err, &r)
			assert.Contains(t, r.Msg, tc.want)
			assert.Equal(t, owner.Email, store.sites[siteID].OwnerEmail)
		})
	}

	t.Run("the store refuses", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		boom := errors.New("down")
		store.failUpdate = boom
		_, err := svc.Update(ctx, admin, "marketing", UpdateRequest{Description: new("x")})
		require.ErrorIs(t, err, boom)
	})
}

func TestDelete(t *testing.T) {
	svc, store, _, _ := fixture(t)
	ctx := context.Background()
	require.ErrorIs(t, svc.Delete(ctx, owner, "marketing"), site.ErrNotAllowed, "the owner may not delete a site")
	assert.Len(t, store.sites, 1)
	require.ErrorIs(t, svc.Delete(ctx, admin, "nope"), site.ErrNotFound)
	require.NoError(t, svc.Delete(ctx, admin, "marketing"))
	assert.Empty(t, store.sites)
}

func TestSetEnvironment(t *testing.T) {
	svc, store, _, _ := fixture(t)
	ctx := context.Background()
	yes, no := true, false

	got, err := svc.SetEnvironment(ctx, owner, "marketing", "qa", EnvironmentChange{RequiresApproval: &yes, Place: &site.Place{After: "stage"}})
	require.NoError(t, err)
	assert.True(t, got.Site.Environment("qa").RequiresApproval)
	assert.Empty(t, got.Site.Environment("qa").PublicDomains, "a new environment answers no one who has not signed in")
	assert.Equal(t, StatusDone, got.Status)
	assert.Equal(t, owner.Email, store.sites[siteID].Environment("qa").UpdatedBy)

	_, err = svc.SetEnvironment(ctx, owner, "marketing", envProd, EnvironmentChange{RequiresApproval: &no})
	require.ErrorIs(t, err, site.ErrNotAllowed, "only an administrator takes the rule off")
	assert.True(t, store.sites[siteID].Environment(envProd).RequiresApproval)

	_, err = svc.SetEnvironment(ctx, owner, "marketing", envProd, EnvironmentChange{RequiresApproval: &yes})
	require.NoError(t, err, "keeping the rule is not removing it")

	got, err = svc.SetEnvironment(ctx, admin, "marketing", envProd, EnvironmentChange{RequiresApproval: &no})
	require.NoError(t, err)
	assert.False(t, got.Site.Environment(envProd).RequiresApproval)

	_, err = svc.SetEnvironment(ctx, stranger, "marketing", "x", EnvironmentChange{})
	require.ErrorIs(t, err, site.ErrNotAllowed)
	var r *RefusedError
	_, err = svc.SetEnvironment(ctx, owner, "marketing", "Bad Env", EnvironmentChange{})
	require.ErrorAs(t, err, &r)
	_, err = svc.SetEnvironment(ctx, owner, "nope", "x", EnvironmentChange{})
	require.ErrorIs(t, err, site.ErrNotFound)
}

func TestPublish_MovesTheEnvironmentAndNumbersTheNext(t *testing.T) {
	svc, store, _, _ := fixture(t)
	var invalidated []string
	svc.Invalidate = func(h string) { invalidated = append(invalidated, h) }
	svc.Config.PagesDomain = "pages.example.net"
	ctx := context.Background()

	first, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Source: "ci", Note: "first", Files: []InputFile{page("a.html")}})
	require.NoError(t, err)
	assert.Equal(t, 1, first.Release.Number)
	assert.Equal(t, site.EnvDev, first.Env, "no environment named lands in dev")
	assert.Equal(t, "https://marketing.pages.example.net", first.Address, "dev holds the main address, so it answers there")
	assert.Equal(t, 1, store.sites[siteID].Environment(site.EnvDev).Release)
	assert.Equal(t, "ci", store.releases[siteID][0].Source)
	assert.Equal(t, owner.Email, store.releases[siteID][0].CreatedBy)
	assert.Equal(t, []string{"marketing"}, invalidated, "this replica's gateway is told")

	_, err = svc.Publish(ctx, owner, PublishRequest{Site: siteID, Environment: "stage", Files: []InputFile{page("a.html")}})
	require.ErrorContains(t, err, `environment "stage" is read-only`, "a publish never lands where people are served from")
	assert.Equal(t, 0, store.sites[siteID].Environment("stage").Release, "stage stays where it was")
	assert.Len(t, store.releases[siteID], 1, "a refused publish writes no release")

	store.sites[siteID].MainEnvironment = envProd
	second, err := svc.Publish(ctx, owner, PublishRequest{Site: siteID, Environment: site.EnvAlias, Files: []InputFile{page("a.html")}})
	require.NoError(t, err, "the old name of dev still publishes to dev")
	assert.Equal(t, 2, second.Release.Number)
	assert.Equal(t, site.EnvDev, second.Env)
	assert.Equal(t, "https://marketing--dev.pages.example.net", second.Address, "dev off the main address answers at <site>--dev")
	assert.Equal(t, 2, store.sites[siteID].Environment(site.EnvDev).Release)

	d := store.deployments[siteID][0]
	assert.Equal(t, site.Deployment{Environment: site.EnvDev, Action: site.ActionPublish, From: 1, To: 2, Actor: owner.Email}, d)
}

func TestPublish_OwnerManagerOrAdministrator(t *testing.T) {
	cases := []struct {
		name  string
		actor Actor
		err   error
	}{
		{"the owner", owner, nil},
		{"the owner in other capitals", Actor{Email: "JANE@Example.com"}, nil},
		{"an administrator", admin, nil},
		{"someone who manages it", manager, nil},
		{"someone who may only view it", viewer, site.ErrNotAllowed},
		{"someone else", stranger, site.ErrNotAllowed},
		{"no one", Actor{}, site.ErrNotAllowed},
		{"the owner's address, not verified", unverified(owner), site.ErrNotAllowed},
		{"a manager's address, not verified", unverified(manager), site.ErrNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, objects, _ := fixture(t)
			grant(store, manager.Email, site.LevelManage)
			grant(store, viewer.Email, site.LevelView)
			_, err := svc.Publish(context.Background(), tc.actor, publishReq(page("a.html")))
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				assert.Empty(t, objects.keys())
				return
			}
			require.NoError(t, err)
		})
	}

	t.Run("an empty email is not the owner of a site with no owner", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.sites[siteID].OwnerEmail = ""
		_, err := svc.Publish(context.Background(), Actor{}, publishReq(page("a.html")))
		require.ErrorIs(t, err, site.ErrNotAllowed)
	})
}

func TestPublish_Preconditions(t *testing.T) {
	var r *RefusedError
	t.Run("an environment people use is reached by promote", func(t *testing.T) {
		svc, store, objects, approvals := fixture(t)
		_, err := svc.Publish(context.Background(), admin, PublishRequest{Site: "marketing", Environment: envProd, Files: []InputFile{page("a")}})
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, `environment "prod" is read-only`)
		assert.Contains(t, r.Msg, "change dev")
		assert.Contains(t, r.Msg, "by promote")
		assert.Empty(t, objects.keys())
		assert.Empty(t, store.releases[siteID])
		assert.Empty(t, approvals.selfRecords, "an administrator's publish is no way round it")
	})
	t.Run("an environment that does not exist is refused as read-only, not created", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		_, err := svc.Publish(context.Background(), owner, PublishRequest{Site: "marketing", Environment: "qa", Files: []InputFile{page("a")}})
		require.ErrorAs(t, err, &r)
		assert.Nil(t, store.sites[siteID].Environment("qa"))
	})
	t.Run("a site with no dev", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.sites[siteID].Environments = store.sites[siteID].Environments[1:]
		_, err := svc.Publish(context.Background(), owner, PublishRequest{Site: "marketing", Files: []InputFile{page("a")}})
		require.ErrorIs(t, err, site.ErrNoEnvironment)
	})
	t.Run("no such site", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.Publish(context.Background(), owner, PublishRequest{Site: "nope"})
		require.ErrorIs(t, err, site.ErrNotFound)
	})
	t.Run("a note past its bound", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.Publish(context.Background(), owner, PublishRequest{Site: "marketing", Note: strings.Repeat("x", site.MaxNote+1), Files: []InputFile{page("a")}})
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "note")
	})
	t.Run("no object store", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		svc.Objects = nil
		_, err := svc.Publish(context.Background(), owner, publishReq(page("a")))
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "serves no sites")
	})
	t.Run("no bucket", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		svc.Bucket = ""
		_, err := svc.Publish(context.Background(), owner, publishReq(page("a")))
		require.ErrorAs(t, err, &r)
	})
}

func TestPromote_NoApprovalNeeded(t *testing.T) {
	svc, store, _, approvals := fixture(t)
	store.domains["stage.example.org"] = &site.Domain{Hostname: "stage.example.org", SiteID: siteID, Environment: "stage"}
	released(store, 1, site.EnvDev)

	got, err := svc.Promote(context.Background(), owner, PromoteRequest{Site: "marketing", From: site.EnvDev, To: "stage"})
	require.NoError(t, err)
	assert.Equal(t, StatusPromoted, got.Status)
	assert.Equal(t, 1, got.Release)
	assert.Equal(t, "https://stage.example.org", got.URL, "an environment answers at a domain added to it")
	assert.Nil(t, got.Approval)
	assert.Equal(t, 1, store.sites[siteID].Environment("stage").Release)
	assert.Equal(t, site.ActionPromote, store.deployments[siteID][0].Action)
	assert.Empty(t, approvals.asked, "the queue is not consulted")
	assert.Empty(t, approvals.submitted)
}

func TestPromote_ANonAdministratorWaitsOnARequest(t *testing.T) {
	svc, store, _, approvals := fixture(t)
	rel := released(store, 1, "stage")

	got, err := svc.Promote(context.Background(), owner, PromoteRequest{Site: "marketing", From: "stage", To: envProd, Reason: "launch"})
	require.NoError(t, err)
	assert.Equal(t, StatusAwaitingApproval, got.Status)
	require.NotNil(t, got.Approval)
	assert.Equal(t, "apr_1", got.Approval.ID)
	assert.Empty(t, got.URL, "nothing is served yet")
	assert.Zero(t, store.sites[siteID].Environment(envProd).Release, "prod did not move")

	require.Len(t, approvals.submitted, 1)
	assert.Equal(t, approval.SubmitRelease{
		ReleaseID: rel.ID, Site: "marketing", Number: 1, Target: envProd, Digest: rel.ManifestSHA256, Reason: "launch",
	}, approvals.submitted[0])
	assert.Equal(t, approval.Actor{Email: owner.Email, Persona: owner.Persona}, approvals.actors[0])
	assert.Equal(t, [][3]string{{rel.ID, envProd, rel.ManifestSHA256}}, approvals.asked, "the approval is asked of the digest")
	assert.Empty(t, approvals.selfRecords)
}

func TestPromote_AnAdministratorRecordsTheirOwnApproval(t *testing.T) {
	svc, store, _, approvals := fixture(t)
	rel := released(store, 1, "stage")

	got, err := svc.Promote(context.Background(), admin, PromoteRequest{Site: "marketing", To: envProd, Reason: "hotfix"})
	require.NoError(t, err)
	assert.Equal(t, StatusPromoted, got.Status)
	assert.Equal(t, 1, store.sites[siteID].Environment(envProd).Release)
	require.Len(t, approvals.selfRecords, 1)
	assert.Equal(t, rel.ManifestSHA256, approvals.selfRecords[0].Digest)
	assert.Equal(t, approval.Actor{Email: admin.Email, Persona: admin.Persona, Admin: true}, approvals.actors[0])
	assert.Empty(t, approvals.submitted)
}

func TestPromote_AnApprovedReleaseGoesThrough(t *testing.T) {
	svc, store, _, approvals := fixture(t)
	released(store, 1, "stage")
	approvals.approved = true

	got, err := svc.Promote(context.Background(), owner, PromoteRequest{Site: "marketing", To: envProd, Release: 1})
	require.NoError(t, err)
	assert.Equal(t, StatusPromoted, got.Status)
	assert.Equal(t, 1, store.sites[siteID].Environment(envProd).Release)
	assert.Empty(t, approvals.submitted, "no new request")
	assert.Empty(t, approvals.selfRecords, "no new record")
}

// Promotion goes one step (#2617): an environment takes a release only from
// the one before it, and only the release that one serves, and a refusal names
// the environment it must come from.
func TestPromote_OneStep(t *testing.T) {
	ctx := context.Background()
	svc, store, _, approvals := fixture(t)
	approvals.approved = true
	released(store, 1, "")
	released(store, 2, site.EnvDev)

	_, err := svc.Promote(ctx, owner, PromoteRequest{Site: "marketing", To: "stage", Release: 1})
	require.EqualError(t, err, "stage takes only the release dev serves, and dev serves release 2, not 1. Promote stage from dev to serve 2.")
	got, err := svc.Promote(ctx, owner, PromoteRequest{Site: "marketing", To: "stage"})
	require.NoError(t, err, "from left out is the environment before")
	assert.Equal(t, 2, got.Release)

	_, err = svc.Promote(ctx, admin, PromoteRequest{Site: "marketing", From: site.EnvDev, To: envProd})
	require.EqualError(t, err, "prod takes releases only from stage, the environment before it: promote it from stage")
	assert.Zero(t, store.sites[siteID].Environment(envProd).Release, "prod did not move")
	got, err = svc.Promote(ctx, admin, PromoteRequest{Site: "marketing", From: "stage", To: envProd})
	require.NoError(t, err)
	assert.Equal(t, 2, got.Release)

	_, err = svc.Promote(ctx, admin, PromoteRequest{Site: "marketing", From: envProd, To: site.EnvDev})
	require.EqualError(t, err, "dev takes every publish and edit directly, so nothing is promoted to it")
}

// The refusal of a promotion whose source serves nothing says where its
// release comes from first.
func TestPromote_TheEnvironmentBeforeServesNothing(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := fixture(t)
	_, err := svc.Promote(ctx, admin, PromoteRequest{Site: "marketing", To: "stage"})
	require.EqualError(t, err, "dev serves no release yet. Publish to dev first; stage can then take it from dev.")
	released(store, 1, site.EnvDev)
	_, err = svc.Promote(ctx, admin, PromoteRequest{Site: "marketing", To: envProd})
	require.EqualError(t, err, "stage serves no release yet. Promote dev's release to stage first; prod can then take it from stage.")
}

func TestPromote_Refusals(t *testing.T) {
	boom := errors.New("down")
	ctx := context.Background()
	var r *RefusedError

	t.Run("someone else's site", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		released(store, 1, site.EnvDev)
		_, err := svc.Promote(ctx, stranger, PromoteRequest{Site: "marketing", From: site.EnvDev, To: "stage"})
		require.ErrorIs(t, err, site.ErrNotAllowed)
	})
	t.Run("no such site", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.Promote(ctx, owner, PromoteRequest{Site: "nope", To: "stage"})
		require.ErrorIs(t, err, site.ErrNotFound)
	})
	t.Run("no such target", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.Promote(ctx, owner, PromoteRequest{Site: "marketing", From: site.EnvDev, To: "qa"})
		require.ErrorIs(t, err, site.ErrNoEnvironment)
	})
	t.Run("a source that is not the one before", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.Promote(ctx, owner, PromoteRequest{Site: "marketing", From: "qa", To: "stage"})
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "takes releases only from dev")
	})
	t.Run("a source serving nothing", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.Promote(ctx, owner, PromoteRequest{Site: "marketing", From: site.EnvDev, To: "stage"})
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "serves no release")
	})
	t.Run("a named release the source does not serve", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		released(store, 1, site.EnvDev)
		_, err := svc.Promote(ctx, owner, PromoteRequest{Site: "marketing", To: "stage", Release: 9})
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "dev serves release 1, not 9")
	})
	t.Run("the release the source serves is gone", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.sites[siteID].Environment(site.EnvDev).Release = 9
		_, err := svc.Promote(ctx, owner, PromoteRequest{Site: "marketing", To: "stage"})
		require.ErrorIs(t, err, site.ErrNoRelease)
	})
	t.Run("no approvals queue", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		released(store, 1, "stage")
		svc.Approvals = nil
		_, err := svc.Promote(ctx, admin, PromoteRequest{Site: "marketing", From: "stage", To: envProd})
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "no approvals queue")
		assert.Zero(t, store.sites[siteID].Environment(envProd).Release)
	})
	t.Run("the request cannot be filed", func(t *testing.T) {
		svc, store, _, approvals := fixture(t)
		released(store, 1, "stage")
		approvals.failSubmit = boom
		_, err := svc.Promote(ctx, owner, PromoteRequest{Site: "marketing", From: "stage", To: envProd})
		require.ErrorIs(t, err, boom)
		assert.Zero(t, store.sites[siteID].Environment(envProd).Release)
	})
	t.Run("the approval cannot be recorded", func(t *testing.T) {
		svc, store, _, approvals := fixture(t)
		released(store, 1, "stage")
		approvals.failSelf = boom
		_, err := svc.Promote(ctx, admin, PromoteRequest{Site: "marketing", From: "stage", To: envProd})
		require.ErrorIs(t, err, boom)
		assert.Zero(t, store.sites[siteID].Environment(envProd).Release, "no record, no move")
	})
	t.Run("another move got there first", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		released(store, 1, site.EnvDev)
		store.failPoint = site.ErrStale
		_, err := svc.Promote(ctx, owner, PromoteRequest{Site: "marketing", From: site.EnvDev, To: "stage"})
		require.ErrorIs(t, err, site.ErrStale)
	})
}

func TestRollback_ThePreviousRelease(t *testing.T) {
	svc, store, _, approvals := fixture(t)
	store.domains["www.example.org"] = &site.Domain{Hostname: "www.example.org", SiteID: siteID, Environment: envProd}
	released(store, 1, envProd)
	released(store, 2, envProd)
	released(store, 3, envProd)

	got, err := svc.Rollback(context.Background(), owner, "marketing", envProd, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Release)
	assert.Equal(t, site.ActionRollback, got.Status)
	assert.Equal(t, "https://www.example.org", got.URL)
	assert.Equal(t, 2, store.sites[siteID].Environment(envProd).Release)
	assert.Equal(t, site.Deployment{Environment: envProd, Action: site.ActionRollback, From: 3, To: 2, Actor: owner.Email}, store.deployments[siteID][0])
	assert.Empty(t, approvals.asked, "a release it served already went through what the environment requires")
	assert.Empty(t, approvals.submitted)
}

func TestRollback_ANamedRelease(t *testing.T) {
	svc, store, _, _ := fixture(t)
	released(store, 1, site.EnvDev)
	released(store, 2, site.EnvDev)
	released(store, 3, site.EnvDev)
	got, err := svc.Rollback(context.Background(), owner, siteID, site.EnvDev, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, got.Release)
}

func TestRollback_Refusals(t *testing.T) {
	ctx := context.Background()
	var r *RefusedError

	t.Run("a release the environment never served", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		released(store, 1, site.EnvDev)
		released(store, 2, "stage")
		released(store, 3, site.EnvDev)
		_, err := svc.Rollback(ctx, admin, "marketing", site.EnvDev, 2)
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "has not served release 2")
		assert.Equal(t, 3, store.sites[siteID].Environment(site.EnvDev).Release, "it did not move")
	})
	t.Run("the release it serves now", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		released(store, 1, site.EnvDev)
		released(store, 2, site.EnvDev)
		_, err := svc.Rollback(ctx, owner, "marketing", site.EnvDev, 2)
		require.ErrorAs(t, err, &r)
	})
	t.Run("no earlier release", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		released(store, 1, site.EnvDev)
		_, err := svc.Rollback(ctx, owner, "marketing", site.EnvDev, 0)
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "no earlier release")
	})
	t.Run("someone else's site", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		released(store, 1, site.EnvDev)
		_, err := svc.Rollback(ctx, stranger, "marketing", site.EnvDev, 0)
		require.ErrorIs(t, err, site.ErrNotAllowed)
	})
	t.Run("no such environment", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.Rollback(ctx, owner, "marketing", "qa", 0)
		require.ErrorIs(t, err, site.ErrNoEnvironment)
	})
	t.Run("no such site", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.Rollback(ctx, owner, "nope", "qa", 0)
		require.ErrorIs(t, err, site.ErrNotFound)
	})
	t.Run("the history cannot be read", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		boom := errors.New("down")
		store.failListing = boom
		_, err := svc.Rollback(ctx, owner, "marketing", site.EnvDev, 0)
		require.ErrorIs(t, err, boom)
	})
	t.Run("another move got there first", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		released(store, 1, site.EnvDev)
		released(store, 2, site.EnvDev)
		store.failPoint = site.ErrStale
		_, err := svc.Rollback(ctx, owner, "marketing", site.EnvDev, 0)
		require.ErrorIs(t, err, site.ErrStale)
	})
}

func TestOwnAddressAndPageOrigin(t *testing.T) {
	svc, _, _, _ := fixture(t)
	st := &site.Site{Name: "marketing", MainEnvironment: site.EnvDev}
	assert.Empty(t, svc.Addressing().OwnAddress(st), "no pages domain, no address")
	assert.Empty(t, svc.Addressing().PageOrigin("shop"), "no pages domain, no page origin")

	svc.Config.PagesDomain = "pages.example.net"
	assert.Equal(t, "https://marketing.pages.example.net", svc.Addressing().OwnAddress(st))
	assert.Empty(t, svc.Addressing().OwnAddress(&site.Site{Name: "marketing"}), "a main address given to no environment is no address")
	assert.Equal(t, "https://marketing--prod.pages.example.net", svc.Addressing().PagesAddress(st, "prod"))
	assert.Equal(t, "https://marketing.pages.example.net", svc.Addressing().PagesAddress(st, site.EnvDev), "the main environment answers at the main address")
	assert.Equal(t, "https://shop.pages.example.net", svc.Addressing().PageOrigin("shop"))

	svc.PublicPort = "8443"
	assert.Equal(t, "https://marketing.pages.example.net:8443", svc.Addressing().OwnAddress(st))
	assert.Equal(t, "https://shop.pages.example.net:8443", svc.Addressing().PageOrigin("shop"))
	assert.Equal(t, "https://www.example.com:8443", svc.Addressing().Origin("www.example.com"))
}

func TestCfg_TheLiveConfigurationWinsOverTheSeed(t *testing.T) {
	svc, _, _, _ := fixture(t)
	st := &site.Site{Name: "marketing", MainEnvironment: site.EnvDev}
	svc.Config.PagesDomain = "seed.example.net"
	assert.Equal(t, "https://marketing.seed.example.net", svc.Addressing().OwnAddress(st))

	svc.Live = siteconfig.NewLive(siteconfig.Config{PagesDomain: "live.example.net"})
	assert.Equal(t, "https://marketing.live.example.net", svc.Addressing().OwnAddress(st))

	svc.Live.Set(siteconfig.Config{PagesDomain: "saved.example.net"})
	assert.Equal(t, "https://marketing.saved.example.net", svc.Addressing().OwnAddress(st), "a saved setting is in force on the next read")
}

func TestAddress(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := fixture(t)
	svc.Config.PagesDomain = "pages.example.net"
	st := store.sites[siteID]
	store.domains["b.example.org"] = &site.Domain{Hostname: "b.example.org", SiteID: siteID, Environment: envProd}
	store.domains["a.example.org"] = &site.Domain{Hostname: "a.example.org", SiteID: siteID, Environment: envProd}
	store.domains["other.example.org"] = &site.Domain{Hostname: "other.example.org", SiteID: "site_2", Environment: "stage"}

	assert.Equal(t, "https://marketing.pages.example.net", svc.Address(ctx, st, site.EnvDev), "dev holds the main address")
	assert.Equal(t, "https://a.example.org", svc.Address(ctx, st, envProd), "the first domain added to it")
	assert.Equal(t, "https://marketing--stage.pages.example.net", svc.Address(ctx, st, "stage"), "another site's domain is not this one's: its own address is")

	store.failDomains = errors.New("down")
	assert.Equal(t, "https://marketing--prod.pages.example.net", svc.Address(ctx, st, envProd), "a failed read falls back to the pages address")

	svc.Config.PagesDomain = ""
	store.failDomains = nil
	assert.Empty(t, svc.Address(ctx, st, "stage"), "no domain and no pages domain is no address")
	assert.Empty(t, svc.Addressing().PagesAddresses(st)[site.EnvDev], "no pages domain, no pages address")
	svc.Config.PagesDomain = "pages.example.net"
	assert.Equal(t, map[string]string{
		site.EnvDev: "https://marketing.pages.example.net", "stage": "https://marketing--stage.pages.example.net", envProd: "https://marketing--prod.pages.example.net",
	}, svc.Addressing().PagesAddresses(st))
}

// TestAddresses pins #2607: each environment is addressed by the first of its
// domains, else its address on the pages domain, the answer the portal's site
// view and manage_site get share.
func TestAddresses(t *testing.T) {
	svc, store, _, _ := fixture(t)
	svc.Config.PagesDomain = "pages.example.net"
	st := store.sites[siteID]
	domains := []site.Domain{
		{Hostname: "a.example.org", SiteID: siteID, Environment: envProd},
		{Hostname: "b.example.org", SiteID: siteID, Environment: envProd},
		{Hostname: "dev.example.org", SiteID: siteID, Environment: site.EnvDev},
	}
	assert.Equal(t, map[string]string{
		site.EnvDev: "https://dev.example.org",
		"stage":     "https://marketing--stage.pages.example.net",
		envProd:     "https://a.example.org",
	}, svc.Addressing().Addresses(st, domains), "a domain wins over the main address and over <site>--<env>")
	assert.Equal(t, svc.Addressing().PagesAddresses(st), svc.Addressing().Addresses(st, nil), "with no domain, every environment's pages address")
}

// TestPagesAddress_AReservedLabelIsNoAddress pins #2611: a two-character
// site's <site>--<env> is a label certificate authorities refuse, so it is no
// address; its main address still is, and the site is told why.
func TestPagesAddress_AReservedLabelIsNoAddress(t *testing.T) {
	svc, _, _, _ := fixture(t)
	st := &site.Site{Name: "ab", MainEnvironment: site.EnvDev, Environments: []site.Environment{{Name: site.EnvDev}, {Name: envProd}}}
	assert.Empty(t, svc.Addressing().AddressNote(st), "no pages domain, nothing to explain")

	svc.Config.PagesDomain = "pages.example.net"
	assert.Equal(t, "https://ab.pages.example.net", svc.Addressing().PagesAddress(st, site.EnvDev), "the main address is one label of two characters")
	assert.Empty(t, svc.Addressing().PagesAddress(st, envProd), "ab--prod is refused by certificate authorities")
	assert.Equal(t, map[string]string{site.EnvDev: "https://ab.pages.example.net", envProd: ""}, svc.Addressing().PagesAddresses(st))
	assert.Equal(t, "https://ab.example.org", svc.Addressing().Addresses(st, []site.Domain{{Hostname: "ab.example.org", Environment: envProd}})[envProd],
		"a domain is still the environment's address")
	assert.Contains(t, svc.Addressing().AddressNote(st), "an address like ab--prod.pages.example.net")
	assert.Empty(t, svc.Addressing().AddressNote(&site.Site{Name: "abc"}))
}

func domainFixture(t *testing.T) (*Service, *memStore) {
	t.Helper()
	svc, store, _, _ := fixture(t)
	svc.Config = siteconfig.Config{PagesDomain: "pages.example.net"}
	svc.PortalHost = "portal.example.com"
	svc.ReservedHosts = []string{"mcp.example.org", "oauth.example.org"}
	svc.CookieDomain = ".session.example.com"
	svc.Apps = fakeApps{"shop": {ID: "app_shop", Name: "shop"}}
	return svc, store
}

func TestAddDomain_Refusals(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		req  DomainRequest
		want string
	}{
		{"not a DNS name", DomainRequest{Hostname: "*.example.org", Site: "marketing", Environment: "prod"}, "a DNS name"},
		{"the portal's host", DomainRequest{Hostname: "portal.example.com", Site: "marketing", Environment: "prod"}, "answers on itself"},
		{"the portal's host in capitals with a port", DomainRequest{Hostname: "PORTAL.example.com:443", Site: "marketing", Environment: "prod"}, "answers on itself"},
		{"a reserved host", DomainRequest{Hostname: "mcp.example.org", Site: "marketing", Environment: "prod"}, "answers on itself"},
		{"a reserved host in capitals with a trailing dot", DomainRequest{Hostname: "MCP.Example.org.", Site: "marketing", Environment: "prod"}, "answers on itself"},
		{"a second reserved host", DomainRequest{Hostname: "oauth.example.org", Application: "shop"}, "answers on itself"},
		{"the pages domain", DomainRequest{Hostname: "pages.example.net", Site: "marketing", Environment: "prod"}, "under the pages domain"},
		{"under the pages domain", DomainRequest{Hostname: "shop.pages.example.net", Site: "marketing", Environment: "prod"}, "under the pages domain"},
		{"the cookie domain", DomainRequest{Hostname: "session.example.com", Site: "marketing", Environment: "prod"}, "session cookie's domain"},
		{"under the cookie domain", DomainRequest{Hostname: "a.session.example.com", Site: "marketing", Environment: "prod"}, "session cookie's domain"},
		{"neither site nor application", DomainRequest{Hostname: "www.example.org"}, "maps to a site and environment, or to an application"},
		{"both site and application", DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "prod", Application: "shop"}, "maps to a site and environment, or to an application"},
		{"dev", DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: site.EnvDev}, "dev is the working copy and cannot have a domain"},
		{"dev by its old name", DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: site.EnvAlias}, "dev is the working copy and cannot have a domain"},
		{"no environment named", DomainRequest{Hostname: "www.example.org", Site: "marketing"}, "environment name"},
		{"a bad environment name", DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "Bad Env"}, "environment name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := domainFixture(t)
			_, err := svc.AddDomain(ctx, admin, tc.req)
			var r *RefusedError
			require.ErrorAs(t, err, &r)
			assert.Contains(t, r.Msg, tc.want)
			assert.Empty(t, store.domains)
		})
	}

	t.Run("someone who does not manage the site", func(t *testing.T) {
		for _, actor := range []Actor{stranger, viewer, unverified(owner), unverified(manager)} {
			svc, store := domainFixture(t)
			grant(store, manager.Email, site.LevelManage)
			grant(store, viewer.Email, site.LevelView)
			_, err := svc.AddDomain(ctx, actor, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "new"})
			require.ErrorIs(t, err, site.ErrNotAllowed, actor.Email)
			assert.Empty(t, store.domains)
			assert.Nil(t, store.sites[siteID].Environment("new"), "a refused request creates no environment")
		}
	})
	t.Run("an application is an administrator's", func(t *testing.T) {
		svc, store := domainFixture(t)
		_, err := svc.AddDomain(ctx, owner, DomainRequest{Hostname: "shop.example.org", Application: "shop"})
		require.ErrorIs(t, err, site.ErrNotAllowed)
		assert.Empty(t, store.domains)
	})
	t.Run("no such site", func(t *testing.T) {
		svc, _ := domainFixture(t)
		_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Site: "nope", Environment: "prod"})
		require.ErrorIs(t, err, site.ErrNotFound)
	})
	t.Run("no such application", func(t *testing.T) {
		svc, _ := domainFixture(t)
		_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Application: "nope"})
		require.ErrorIs(t, err, application.ErrNotFound)
	})
	t.Run("no applications here", func(t *testing.T) {
		svc, _ := domainFixture(t)
		svc.Apps = nil
		_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Application: "shop"})
		var r *RefusedError
		require.ErrorAs(t, err, &r)
	})
	t.Run("already mapped creates no environment", func(t *testing.T) {
		svc, store := domainFixture(t)
		_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "prod"})
		require.NoError(t, err)
		_, err = svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "other"})
		require.ErrorIs(t, err, site.ErrDomainTaken)
		assert.Nil(t, store.sites[siteID].Environment("other"))
	})
	t.Run("the store cannot say whether it is mapped", func(t *testing.T) {
		svc, store := domainFixture(t)
		boom := errors.New("down")
		store.failDomain = boom
		_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "prod"})
		require.ErrorIs(t, err, boom)
		assert.ErrorContains(t, err, "reading the domain")
	})
	t.Run("the environment cannot be created", func(t *testing.T) {
		svc, store := domainFixture(t)
		boom := errors.New("down")
		store.failSetEnv = boom
		_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "new", Place: &site.Place{After: "stage"}})
		require.ErrorIs(t, err, boom)
		assert.Empty(t, store.domains)
	})
	t.Run("the domain cannot be stored", func(t *testing.T) {
		svc, store := domainFixture(t)
		boom := errors.New("down")
		store.failAdd = boom
		var invalidated []string
		svc.Invalidate = func(h string) { invalidated = append(invalidated, h) }
		_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "prod"})
		require.ErrorIs(t, err, boom)
		assert.Empty(t, invalidated, "nothing changed, so no route is dropped")
	})
}

func TestAddDomain_RoutesAsSoonAsAdded(t *testing.T) {
	svc, store := domainFixture(t)
	ctx := context.Background()
	var invalidated []string
	svc.Invalidate = func(h string) { invalidated = append(invalidated, h) }

	got, err := svc.AddDomain(ctx, owner, DomainRequest{Hostname: "WWW.Example.org.", Site: "marketing", Environment: envProd})
	require.NoError(t, err)
	assert.Equal(t, "www.example.org", got.Hostname, "stored as a request's Host names it")
	assert.Equal(t, siteID, got.SiteID)
	assert.Equal(t, envProd, got.Environment)
	assert.Empty(t, got.ApplicationID)
	assert.Equal(t, owner.Email, got.CreatedBy)
	assert.Equal(t, []string{"www.example.org"}, invalidated, "a replica's negative cache is told at once")

	app, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "shop.example.org", Application: "shop"})
	require.NoError(t, err)
	assert.Equal(t, "app_shop", app.ApplicationID)
	assert.Empty(t, app.SiteID)
	assert.Empty(t, app.Environment)
	assert.Len(t, store.domains, 2)
}

func TestAddDomain_AManagerAddsAndCreatesTheEnvironment(t *testing.T) {
	yes := true
	svc, store := domainFixture(t)
	grant(store, manager.Email, site.LevelManage)

	got, err := svc.AddDomain(context.Background(), manager, DomainRequest{
		Hostname: "qa.example.org", Site: "marketing", Environment: "qa", RequiresApproval: &yes, Place: &site.Place{After: site.EnvDev},
	})
	require.NoError(t, err)
	assert.Equal(t, "qa", got.Environment)
	env := store.sites[siteID].Environment("qa")
	require.NotNil(t, env, "the environment is created")
	assert.True(t, env.RequiresApproval, "with the rule the request asked for")
	assert.Equal(t, manager.Email, env.UpdatedBy)
}

func TestAddDomain_AnExistingEnvironmentIsLeftAlone(t *testing.T) {
	svc, store := domainFixture(t)
	_, err := svc.AddDomain(context.Background(), owner, DomainRequest{Hostname: "qa.example.org", Site: "marketing", Environment: envProd})
	require.NoError(t, err)
	assert.True(t, store.sites[siteID].Environment(envProd).RequiresApproval, "a request does not take the approval rule off")
}

// Every site holds prod, so the approval rule asked for with a domain applies
// to an environment that exists as to one the request creates; taking it off
// that way is an administrator's, as on set_environment.
func TestAddDomain_RequiresApprovalAppliesToAnExistingEnvironment(t *testing.T) {
	yes := true
	svc, store := domainFixture(t)
	store.sites[siteID].Environment("stage").RequiresApproval = false
	_, err := svc.AddDomain(context.Background(), owner, DomainRequest{Hostname: "stage.example.org", Site: "marketing", Environment: "stage", RequiresApproval: &yes})
	require.NoError(t, err)
	assert.True(t, store.sites[siteID].Environment("stage").RequiresApproval, "the rule the request asked for")

	no := false
	_, err = svc.AddDomain(context.Background(), owner, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: envProd, RequiresApproval: &no})
	require.ErrorIs(t, err, site.ErrNotAllowed, "taking the rule off is an administrator's")
	assert.True(t, store.sites[siteID].Environment(envProd).RequiresApproval)
	_, err = svc.Store.Domain(context.Background(), "www.example.org")
	require.ErrorIs(t, err, site.ErrNoDomain, "a refused request adds no domain")
}

func TestAddDomain_ANoCookieDomainDeploymentAcceptsItsSiblings(t *testing.T) {
	svc, _ := domainFixture(t)
	svc.CookieDomain = ""
	_, err := svc.AddDomain(context.Background(), admin, DomainRequest{Hostname: "a.session.example.com", Site: "marketing", Environment: "prod"})
	require.NoError(t, err)
}

func TestRemoveDomain(t *testing.T) {
	ctx := context.Background()
	svc, store := domainFixture(t)
	grant(store, manager.Email, site.LevelManage)
	_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "stage"})
	require.NoError(t, err)
	var invalidated []string
	svc.Invalidate = func(h string) { invalidated = append(invalidated, h) }

	require.ErrorIs(t, svc.RemoveDomain(ctx, stranger, "www.example.org"), site.ErrNotAllowed)
	assert.Len(t, store.domains, 1)
	require.ErrorIs(t, svc.RemoveDomain(ctx, admin, "other.example.org"), site.ErrNoDomain)
	assert.Empty(t, invalidated)
	require.NoError(t, svc.RemoveDomain(ctx, manager, "WWW.example.org."), "someone who manages the site removes its domains")
	assert.Empty(t, store.domains)
	assert.Equal(t, []string{"www.example.org"}, invalidated)
}

func TestRemoveDomain_AnApplicationsIsAnAdministrators(t *testing.T) {
	ctx := context.Background()
	svc, store := domainFixture(t)
	store.domains["shop.example.org"] = &site.Domain{Hostname: "shop.example.org", ApplicationID: "app_shop"}

	require.ErrorIs(t, svc.RemoveDomain(ctx, owner, "shop.example.org"), site.ErrNotAllowed)
	assert.Len(t, store.domains, 1)
	require.NoError(t, svc.RemoveDomain(ctx, admin, "shop.example.org"))
	assert.Empty(t, store.domains)
}

func TestRemoveDomain_StoreFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("down")

	t.Run("reading the domain", func(t *testing.T) {
		svc, store := domainFixture(t)
		store.failDomain = boom
		require.ErrorIs(t, svc.RemoveDomain(ctx, admin, "www.example.org"), boom)
	})
	t.Run("the site is gone", func(t *testing.T) {
		svc, store := domainFixture(t)
		store.domains["www.example.org"] = &site.Domain{Hostname: "www.example.org", SiteID: "site_gone"}
		require.ErrorIs(t, svc.RemoveDomain(ctx, admin, "www.example.org"), site.ErrNotFound)
	})
}

func TestPermissionsOf(t *testing.T) {
	ctx := context.Background()
	full := Permissions{Manage: true, Own: true, Delete: true}
	cases := []struct {
		name  string
		actor Actor
		want  Permissions
	}{
		{"an administrator", admin, full},
		{"an administrator whose address is not verified", unverified(admin), full},
		{"the owner", owner, Permissions{Manage: true, Own: true}},
		{"the owner in other capitals", Actor{Email: "Jane@EXAMPLE.com"}, Permissions{Manage: true, Own: true}},
		{"someone who manages it", manager, Permissions{Manage: true}},
		{"someone who manages it in other capitals", Actor{Email: "MO@example.com"}, Permissions{Manage: true}},
		{"someone who may view it", viewer, Permissions{}},
		{"someone else", stranger, Permissions{}},
		{"no email", Actor{}, Permissions{}},
		{"the owner's address, not verified", unverified(owner), Permissions{}},
		{"a manager's address, not verified", unverified(manager), Permissions{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, _, _ := fixture(t)
			grant(store, manager.Email, site.LevelManage)
			grant(store, viewer.Email, site.LevelView)
			got, err := svc.PermissionsOf(ctx, tc.actor, store.sites[siteID])
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("an empty email is never the owner of a site with none", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.sites[siteID].OwnerEmail = ""
		got, err := svc.PermissionsOf(ctx, Actor{}, store.sites[siteID])
		require.NoError(t, err)
		assert.Equal(t, Permissions{}, got)
	})
	t.Run("the store fails", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		boom := errors.New("down")
		store.failGrant = boom
		_, err := svc.PermissionsOf(ctx, stranger, store.sites[siteID])
		require.ErrorIs(t, err, boom)
		_, err = svc.Publish(ctx, stranger, publishReq(page("a")))
		require.ErrorIs(t, err, boom, "mayEdit passes the store's failure on")
		_, err = svc.Update(ctx, stranger, "marketing", UpdateRequest{Description: new("x")})
		require.ErrorIs(t, err, boom, "mayOwn passes the store's failure on")
	})
}

// target is someone the matrix acts on: their email and the level they hold
// before the action, "" for none.
type target struct{ email, level string }

var (
	newPerson  = target{"new@example.com", ""}
	aViewer    = target{viewer.Email, site.LevelView}
	aManager   = target{manager.Email, site.LevelManage}
	everyActor = []struct {
		name string
		who  Actor
	}{
		{"administrator", admin},
		{"owner", owner},
		{"manager", manager},
		{"viewer", viewer},
		{"stranger", stranger},
		{"unverified owner", unverified(owner)},
		{"unverified manager", unverified(manager)},
		{"no email", Actor{}},
	}
)

// allowedBy reads which of everyActor the row admits.
func allowedBy(actors ...string) func(string) bool {
	return func(name string) bool { return slices.Contains(actors, name) }
}

func matrixFixture(t *testing.T) (*Service, *memStore) {
	t.Helper()
	svc, store, _, _ := fixture(t)
	grant(store, manager.Email, site.LevelManage)
	grant(store, viewer.Email, site.LevelView)
	return svc, store
}

func TestGrant_Matrix(t *testing.T) {
	ctx := context.Background()
	ownerOnly := allowedBy("administrator", "owner")
	anyManager := allowedBy("administrator", "owner", "manager")
	rows := []struct {
		name    string
		target  target
		level   string
		allowed func(string) bool
	}{
		{"view to someone new", newPerson, site.LevelView, anyManager},
		{"manage to someone new", newPerson, site.LevelManage, ownerOnly},
		{"a viewer made a manager", aViewer, site.LevelManage, ownerOnly},
		{"a manager made a viewer", aManager, site.LevelView, ownerOnly},
		{"a manager kept a manager", aManager, site.LevelManage, ownerOnly},
		{"a viewer kept a viewer", aViewer, site.LevelView, anyManager},
	}
	for _, row := range rows {
		for _, actor := range everyActor {
			t.Run(row.name+"/"+actor.name, func(t *testing.T) {
				svc, store := matrixFixture(t)
				got, err := svc.Grant(ctx, actor.who, "marketing", GrantRequest{Email: row.target.email, Level: row.level})
				if row.allowed(actor.name) {
					require.NoError(t, err)
					assert.Equal(t, site.Grant{Email: row.target.email, Level: row.level, Environments: []string{}, GrantedBy: actor.who.Email, CreatedAt: store.grants[siteID][row.target.email].CreatedAt}, *got)
					assert.False(t, got.CreatedAt.IsZero(), "the time the store wrote is what is returned")
					assert.Equal(t, row.level, store.grants[siteID][row.target.email].Level)
					return
				}
				require.ErrorIs(t, err, site.ErrNotAllowed)
				assert.Equal(t, row.target.level, store.grants[siteID][row.target.email].Level, "a refusal changes nothing")
			})
		}
	}
}

func TestRevoke_Matrix(t *testing.T) {
	ctx := context.Background()
	rows := []struct {
		name    string
		target  target
		allowed func(string) bool
	}{
		{"a viewer", aViewer, allowedBy("administrator", "owner", "manager")},
		{"a manager", aManager, allowedBy("administrator", "owner")},
	}
	for _, row := range rows {
		for _, actor := range everyActor {
			t.Run(row.name+"/"+actor.name, func(t *testing.T) {
				svc, store := matrixFixture(t)
				err := svc.Revoke(ctx, actor.who, "marketing", row.target.email, "")
				if row.allowed(actor.name) {
					require.NoError(t, err)
					assert.NotContains(t, store.grants[siteID], row.target.email)
					return
				}
				require.ErrorIs(t, err, site.ErrNotAllowed)
				assert.Equal(t, row.target.level, store.grants[siteID][row.target.email].Level)
			})
		}
	}
}

func TestGrant_Refusals(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, email, level, want string
	}{
		{"a level that does not exist", "x@example.com", "owner", "level:"},
		{"not an email", "nobody", site.LevelView, "email: an email address"},
		{"the owner", " JANE@example.com ", site.LevelView, "owns this site"},
		{"an external person to manage", "pat@client.example", site.LevelManage, "only internal people can manage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := matrixFixture(t)
			svc.External = func(_ context.Context, email string) bool { return email == "pat@client.example" }
			_, err := svc.Grant(ctx, admin, "marketing", GrantRequest{Email: tc.email, Level: tc.level})
			var r *RefusedError
			require.ErrorAs(t, err, &r)
			assert.Contains(t, r.Msg, tc.want)
			assert.Len(t, store.grants[siteID], 2, "nothing was added")
		})
	}
}

func TestGrant_ExternalPeopleMayView(t *testing.T) {
	svc, store := matrixFixture(t)
	svc.External = func(context.Context, string) bool { return true }
	got, err := svc.Grant(context.Background(), owner, "marketing", GrantRequest{Email: "  Pat@Client.Example ", Level: site.LevelView})
	require.NoError(t, err)
	assert.Equal(t, "pat@client.example", got.Email, "stored in the form it is matched in")
	assert.Equal(t, site.LevelView, store.grants[siteID]["pat@client.example"].Level)
}

func TestGrant_NoExternalCheckConfiguredAdmitsAManager(t *testing.T) {
	svc, _ := matrixFixture(t)
	svc.External = nil
	_, err := svc.Grant(context.Background(), owner, "marketing", GrantRequest{Email: "pat@client.example", Level: site.LevelManage})
	require.NoError(t, err)
}

// A manage grant made to an address before its first sign-in, when no one
// could say whether it was external, gives an external person no management
// once they sign in: they view, as an external person may.
func TestPermissions_AManageGrantIsJudgedOnUse(t *testing.T) {
	ctx := context.Background()
	svc, _ := matrixFixture(t)
	signedIn := map[string]bool{}
	svc.External = func(_ context.Context, email string) bool { return signedIn[email] }
	_, err := svc.Grant(ctx, owner, "marketing", GrantRequest{Email: "pat@client.example", Level: site.LevelManage})
	require.NoError(t, err, "not yet known to be external")
	st, err := svc.Find(ctx, "marketing")
	require.NoError(t, err)
	pat := Actor{Email: "pat@client.example", Persona: "client"}
	perms, err := svc.PermissionsOf(ctx, pat, st)
	require.NoError(t, err)
	assert.True(t, perms.Manage)

	signedIn["pat@client.example"] = true
	perms, err = svc.PermissionsOf(ctx, pat, st)
	require.NoError(t, err)
	assert.False(t, perms.Manage, "an external person never manages a site")
	_, err = svc.Promote(ctx, pat, PromoteRequest{Site: "marketing", From: site.EnvDev, To: "stage"})
	require.ErrorIs(t, err, site.ErrNotAllowed)
}

func TestGrant_StoreFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("down")

	t.Run("no such site", func(t *testing.T) {
		svc, _ := matrixFixture(t)
		_, err := svc.Grant(ctx, admin, "nope", GrantRequest{Email: "x@example.com", Level: site.LevelView})
		require.ErrorIs(t, err, site.ErrNotFound)
	})
	t.Run("reading the person's level", func(t *testing.T) {
		svc, store := matrixFixture(t)
		store.failGrant = boom
		_, err := svc.Grant(ctx, admin, "marketing", GrantRequest{Email: "x@example.com", Level: site.LevelView})
		require.ErrorIs(t, err, boom)
	})
	t.Run("saving the grant", func(t *testing.T) {
		svc, store := matrixFixture(t)
		store.failSet = boom
		_, err := svc.Grant(ctx, admin, "marketing", GrantRequest{Email: "x@example.com", Level: site.LevelView})
		require.ErrorIs(t, err, boom)
	})
}

func TestRevoke_Failures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("down")

	t.Run("someone with no access", func(t *testing.T) {
		svc, _ := matrixFixture(t)
		require.ErrorIs(t, svc.Revoke(ctx, admin, "marketing", "x@example.com", ""), site.ErrNoGrant)
	})
	t.Run("matches however the email is written", func(t *testing.T) {
		svc, store := matrixFixture(t)
		require.NoError(t, svc.Revoke(ctx, admin, "marketing", "  VI@client.example ", ""))
		assert.NotContains(t, store.grants[siteID], viewer.Email)
	})
	t.Run("no such site", func(t *testing.T) {
		svc, _ := matrixFixture(t)
		require.ErrorIs(t, svc.Revoke(ctx, admin, "nope", viewer.Email, ""), site.ErrNotFound)
	})
	t.Run("reading the person's level", func(t *testing.T) {
		svc, store := matrixFixture(t)
		store.failGrant = boom
		require.ErrorIs(t, svc.Revoke(ctx, admin, "marketing", viewer.Email, ""), boom)
	})
	t.Run("removing the grant", func(t *testing.T) {
		svc, store := matrixFixture(t)
		store.failRemove = boom
		require.ErrorIs(t, svc.Revoke(ctx, admin, "marketing", viewer.Email, ""), boom)
	})
}

func TestGrants_ListsForThoseWhoManage(t *testing.T) {
	ctx := context.Background()
	svc, store := matrixFixture(t)

	got, err := svc.Grants(ctx, manager, "marketing")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, manager.Email, got[0].Email)

	for _, actor := range []Actor{viewer, stranger, unverified(owner)} {
		_, err = svc.Grants(ctx, actor, "marketing")
		require.ErrorIs(t, err, site.ErrNotAllowed, actor.Email)
	}
	_, err = svc.Grants(ctx, admin, "nope")
	require.ErrorIs(t, err, site.ErrNotFound)

	boom := errors.New("down")
	store.failGrant = boom
	_, err = svc.Grants(ctx, admin, "marketing")
	require.ErrorIs(t, err, boom)
}

func TestCompare(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*Service, *memStore) {
		t.Helper()
		svc, store, _, _ := fixture(t)
		store.releases[siteID] = []site.Release{
			{ID: "srel_2", SiteID: siteID, Number: 2, Redirects: []site.Redirect{{From: "/kept", To: "/k", Status: 301}, {From: "/new", To: "/n", Status: 301}}, Files: []site.File{
				{Path: "z-added.html", SHA256: "z"}, {Path: "a-added.html", SHA256: "a"}, {Path: "same.html", SHA256: "s"}, {Path: "edit.html", SHA256: "after"},
			}},
			{ID: "srel_1", SiteID: siteID, Number: 1, Redirects: []site.Redirect{{From: "/kept", To: "/k", Status: 301}, {From: "/old", To: "/o", Status: 302}}, Files: []site.File{
				{Path: "gone.html", SHA256: "g"}, {Path: "same.html", SHA256: "s"}, {Path: "edit.html", SHA256: "before"},
			}},
		}
		return svc, store
	}

	t.Run("what moving from one to the other changes", func(t *testing.T) {
		svc, _ := setup(t)
		got, err := svc.Compare(ctx, "marketing", 1, 2)
		require.NoError(t, err)
		assert.Equal(t, &Comparison{
			From: 1, To: 2,
			Changed: []string{"edit.html"}, Added: []string{"a-added.html", "z-added.html"}, Removed: []string{"gone.html"},
			RedirectsAdded:   []site.Redirect{{From: "/new", To: "/n", Status: 301}},
			RedirectsRemoved: []site.Redirect{{From: "/old", To: "/o", Status: 302}},
		}, got)
	})
	t.Run("from nothing every file is added", func(t *testing.T) {
		svc, _ := setup(t)
		got, err := svc.Compare(ctx, siteID, 0, 1)
		require.NoError(t, err)
		assert.Equal(t, []string{"edit.html", "gone.html", "same.html"}, got.Added)
		assert.NotNil(t, got.Changed, "none is [], never null")
		assert.Empty(t, got.Changed)
		assert.NotNil(t, got.Removed, "none is [], never null")
		assert.Len(t, got.RedirectsAdded, 2)
		assert.NotNil(t, got.RedirectsRemoved, "none is [], never null")
	})
	t.Run("the same release changes nothing", func(t *testing.T) {
		svc, _ := setup(t)
		got, err := svc.Compare(ctx, "marketing", 1, 1)
		require.NoError(t, err)
		assert.Empty(t, got.Changed)
		assert.Empty(t, got.Added)
		assert.Empty(t, got.Removed)
		assert.Empty(t, got.RedirectsAdded)
		assert.Empty(t, got.RedirectsRemoved)
	})

	boom := errors.New("down")
	failures := []struct {
		name     string
		ref      string
		from, to int
		breakIt  func(*memStore)
		want     error
	}{
		{"no such site", "nope", 1, 2, nil, site.ErrNotFound},
		{"no such target", "marketing", 1, 9, nil, site.ErrNoRelease},
		{"no such base", "marketing", 9, 2, nil, site.ErrNoRelease},
		{"the target's files", "marketing", 1, 2, func(m *memStore) { m.failFiles, m.failFilesOf = boom, "srel_2" }, boom},
		{"the base's files", "marketing", 1, 2, func(m *memStore) { m.failFiles, m.failFilesOf = boom, "srel_1" }, boom},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			svc, store := setup(t)
			if tc.breakIt != nil {
				tc.breakIt(store)
			}
			_, err := svc.Compare(ctx, tc.ref, tc.from, tc.to)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

// TestStepRelease is what an application release pins a site environment at
// (#2617): the release the environment before it serves, or, for dev, the
// site's newest release.
func TestStepRelease(t *testing.T) {
	svc, store, _, _ := fixture(t)
	ctx := context.Background()
	n, err := svc.StepRelease(ctx, "marketing", site.EnvDev)
	require.NoError(t, err)
	assert.Zero(t, n, "no release yet")

	released(store, 1, site.EnvDev)
	released(store, 2, site.EnvDev)
	store.sites[siteID].Environment("stage").Release = 1
	n, err = svc.StepRelease(ctx, siteID, site.EnvDev)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "dev is pinned at the newest release")
	n, err = svc.StepRelease(ctx, siteID, "stage")
	require.NoError(t, err)
	assert.Equal(t, 2, n, "stage at what dev serves")
	n, err = svc.StepRelease(ctx, siteID, envProd)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "prod at what stage serves, not the newest")

	_, err = svc.StepRelease(ctx, "nope", envProd)
	require.ErrorIs(t, err, site.ErrNotFound)
	_, err = svc.StepRelease(ctx, "marketing", "qa")
	require.ErrorIs(t, err, site.ErrNoEnvironment)

	boom := errors.New("down")
	store.failListing = boom
	_, err = svc.StepRelease(ctx, "marketing", site.EnvDev)
	require.ErrorIs(t, err, boom)
}

func TestEnvironmentOwner(t *testing.T) {
	svc, _, _, _ := fixture(t)
	ctx := context.Background()
	got, err := svc.EnvironmentOwner(ctx, "marketing", envProd)
	require.NoError(t, err)
	assert.Equal(t, owner.Email, got)
	_, err = svc.EnvironmentOwner(ctx, "marketing", "qa")
	require.ErrorIs(t, err, site.ErrNoEnvironment)
	_, err = svc.EnvironmentOwner(ctx, "nope", envProd)
	require.ErrorIs(t, err, site.ErrNotFound)
}

// TestSiteEnvironments pins #2613: every environment of every site, with its
// owner and when it last moved (the site's time for one never moved), for an
// application's search for parts.
func TestSiteEnvironments(t *testing.T) {
	svc, store, _, _ := fixture(t)
	ctx := context.Background()
	moved := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	store.sites[siteID].Environment(envProd).UpdatedAt = moved
	store.sites[siteID].UpdatedAt = moved.Add(-time.Hour)

	got, err := svc.SiteEnvironments(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []application.SiteEnvironment{
		{Site: "marketing", Environment: site.EnvDev, Owner: owner.Email, UpdatedAt: moved.Add(-time.Hour)},
		{Site: "marketing", Environment: "stage", Owner: owner.Email, UpdatedAt: moved.Add(-time.Hour)},
		{Site: "marketing", Environment: envProd, Owner: owner.Email, UpdatedAt: moved},
	}, got)

	delete(store.sites, siteID)
	none, err := svc.SiteEnvironments(ctx)
	require.NoError(t, err)
	assert.NotNil(t, none, "an empty list is [], never null")

	store.failList = errors.New("down")
	_, err = svc.SiteEnvironments(ctx)
	require.ErrorIs(t, err, store.failList)
}

func TestReleaseExists(t *testing.T) {
	svc, store, _, _ := fixture(t)
	ctx := context.Background()
	released(store, 1, "")
	assert.True(t, svc.ReleaseExists(ctx, "marketing", 1))
	assert.False(t, svc.ReleaseExists(ctx, "marketing", 2))
	assert.False(t, svc.ReleaseExists(ctx, "nope", 1))
}

func TestApplyPin(t *testing.T) {
	ctx := context.Background()

	t.Run("moves the environment with no approval, as an application release", func(t *testing.T) {
		svc, store, _, approvals := fixture(t)
		released(store, 1, "")
		require.NoError(t, svc.ApplyPin(ctx, admin.Email, "marketing", envProd, 1))
		assert.Equal(t, 1, store.sites[siteID].Environment(envProd).Release)
		assert.Equal(t, site.Deployment{Environment: envProd, Action: site.ActionApplication, To: 1, Actor: admin.Email}, store.deployments[siteID][0])
		assert.Empty(t, approvals.asked)
	})
	t.Run("an environment already there records no move", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		released(store, 1, envProd)
		before := len(store.deployments[siteID])
		require.NoError(t, svc.ApplyPin(ctx, admin.Email, "marketing", envProd, 1))
		assert.Len(t, store.deployments[siteID], before)
	})
	t.Run("no such environment", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		require.ErrorIs(t, svc.ApplyPin(ctx, admin.Email, "marketing", "qa", 1), site.ErrNoEnvironment)
	})
	t.Run("no such site", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		require.ErrorIs(t, svc.ApplyPin(ctx, admin.Email, "nope", envProd, 1), site.ErrNotFound)
	})
}

func TestRefusedError(t *testing.T) {
	assert.Equal(t, "because 3", refused("because %d", 3).Error())
}

// A deleted site's objects go with it, compressed forms included; the rows that
// name them are read first.
func TestDelete_RemovesTheObjectsOfEveryRelease(t *testing.T) {
	ctx := context.Background()
	svc, store, objects, _ := fixture(t)
	rel := site.Release{ID: "srel_a", SiteID: siteID, Number: 1, Files: []site.File{
		{Path: "index.html", SHA256: "aa", GzipSize: 10, BrotliSize: 9},
		{Path: "logo.png", SHA256: "bb"},
	}}
	store.releases[siteID] = []site.Release{rel}
	for _, key := range []string{
		ObjectKey(siteID, rel.Files[0], ""), ObjectKey(siteID, rel.Files[0], "gz"),
		ObjectKey(siteID, rel.Files[0], "br"), ObjectKey(siteID, rel.Files[1], ""),
	} {
		require.NoError(t, objects.PutObject(ctx, "portal", key, []byte("x"), "text/plain"))
	}

	require.NoError(t, svc.Delete(ctx, admin, "marketing"))
	assert.Empty(t, objects.keys(), "every form of every file is gone")
}

func TestDelete_StoreFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("down")

	t.Run("a failed delete leaves the objects", func(t *testing.T) {
		svc, store, objects, _ := fixture(t)
		store.releases[siteID] = []site.Release{{ID: "srel_a", SiteID: siteID, Number: 1, Files: []site.File{{Path: "a.html"}}}}
		key := ObjectKey(siteID, site.File{Path: "a.html"}, "")
		require.NoError(t, objects.PutObject(ctx, "portal", key, []byte("x"), "text/html"))
		store.failDelete = boom
		require.ErrorIs(t, svc.Delete(ctx, admin, "marketing"), boom)
		assert.True(t, objects.has("portal/"+key))
	})
	t.Run("releases that cannot be read still delete the record", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.failListing = boom
		require.NoError(t, svc.Delete(ctx, admin, "marketing"))
		assert.Empty(t, store.sites)
	})
	t.Run("files that cannot be read still delete the record", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.releases[siteID] = []site.Release{{ID: "srel_a", SiteID: siteID, Number: 1}}
		store.failFiles = boom
		require.NoError(t, svc.Delete(ctx, admin, "marketing"))
		assert.Empty(t, store.sites)
	})
	t.Run("no object store", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.releases[siteID] = []site.Release{{ID: "srel_a", SiteID: siteID, Number: 1, Files: []site.File{{Path: "a.html"}}}}
		svc.Objects = nil
		require.NoError(t, svc.Delete(ctx, admin, "marketing"))
	})
	t.Run("a bucket that refuses is logged, not returned", func(t *testing.T) {
		svc, store, objects, _ := fixture(t)
		store.releases[siteID] = []site.Release{{ID: "srel_a", SiteID: siteID, Number: 1, Files: []site.File{{Path: "a.html"}}}}
		objects.failDelete = boom
		require.NoError(t, svc.Delete(ctx, admin, "marketing"))
		assert.Empty(t, store.sites)
	})
}

// Someone with no rights must not learn who owns a site or who is external from
// what a grant is refused with (#2559).
func TestGrant_SaysNothingAboutThePersonToSomeoneWithNoRights(t *testing.T) {
	ctx := context.Background()
	for _, email := range []string{"jane@example.com", "pat@client.example", "nobody"} {
		t.Run(email, func(t *testing.T) {
			svc, _ := matrixFixture(t)
			svc.External = func(_ context.Context, e string) bool { return e == "pat@client.example" }
			_, err := svc.Grant(ctx, Actor{Email: "someone@example.com"}, "marketing", GrantRequest{Email: email, Level: site.LevelManage})
			require.ErrorIs(t, err, site.ErrNotAllowed)
		})
	}
}

// Moving a name off an environment that requires approval would publish to it
// unapproved, so only an administrator takes it off (#2559).
func TestRemoveDomain_FromAnEnvironmentThatRequiresApprovalIsAnAdministrators(t *testing.T) {
	ctx := context.Background()
	yes := true
	svc, store := domainFixture(t)
	grant(store, manager.Email, site.LevelManage)
	_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "live.example.org", Site: "marketing", Environment: "launch", RequiresApproval: &yes, Place: &site.Place{After: "stage"}})
	require.NoError(t, err)
	require.ErrorIs(t, svc.RemoveDomain(ctx, manager, "live.example.org"), site.ErrNotAllowed)
	require.ErrorIs(t, svc.RemoveDomain(ctx, owner, "live.example.org"), site.ErrNotAllowed)
	assert.Len(t, store.domains, 1)
	require.NoError(t, svc.RemoveDomain(ctx, admin, "live.example.org"))
	assert.Empty(t, store.domains)
}

func TestClaimable_ReservedHostsAndWhatIsUnderThem(t *testing.T) {
	svc, _ := domainFixture(t)
	svc.ReservedHosts = []string{"platform.internal.example"}
	for _, host := range []string{"platform.internal.example", "x.platform.internal.example"} {
		var r *RefusedError
		require.ErrorAs(t, svc.claimable(host), &r, host)
	}
	require.NoError(t, svc.claimable("other.internal.example"))
}
