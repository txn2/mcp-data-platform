package sitesvc

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
)

const siteID = "site_1"

var (
	owner    = Actor{Email: "jane@example.com", Persona: "analyst"}
	stranger = Actor{Email: "joe@example.com"}
	admin    = Actor{Email: "admin@example.com", Persona: "admin", Admin: true}
)

// fixture is a service over one site owned by jane with dev, stage and a prod
// that requires approval.
//
//nolint:revive // a fixture hands back each fake a test drives
func fixture(t *testing.T) (*Service, *memStore, *memObjects, *fakeApprovals) {
	t.Helper()
	store, objects, approvals := newMem(), newObjects(), &fakeApprovals{}
	store.sites[siteID] = &site.Site{
		ID: siteID, Name: "marketing", OwnerEmail: owner.Email,
		Environments: []site.Environment{{Name: site.EnvDev}, {Name: "stage"}, {Name: site.EnvProd, RequiresApproval: true}},
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

func TestCreate(t *testing.T) {
	svc, store, _, _ := fixture(t)
	ctx := context.Background()
	got, err := svc.Create(ctx, owner, "blog", "the blog")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(got.ID, "site_"))
	assert.Equal(t, owner.Email, got.OwnerEmail)
	assert.Equal(t, owner.Email, got.CreatedBy)
	require.Len(t, got.Environments, 3)
	assert.False(t, got.Environment(site.EnvDev).RequiresApproval)
	assert.False(t, got.Environment("stage").RequiresApproval)
	assert.True(t, got.Environment(site.EnvProd).RequiresApproval, "prod starts out requiring approval")
	assert.Len(t, store.sites, 2)

	var r *RefusedError
	_, err = svc.Create(ctx, Actor{}, "other", "")
	require.ErrorAs(t, err, &r, "a site needs an owner")
	_, err = svc.Create(ctx, owner, "Not A Name", "")
	require.ErrorAs(t, err, &r)
	_, err = svc.Create(ctx, owner, "blog", "")
	require.ErrorIs(t, err, site.ErrNameTaken)
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

	got, err := svc.Update(ctx, owner, "marketing", "new words", "")
	require.NoError(t, err)
	assert.Equal(t, "new words", got.Description)
	assert.Equal(t, owner.Email, got.OwnerEmail, "an empty owner leaves it")

	got, err = svc.Update(ctx, admin, siteID, "", "new@example.com")
	require.NoError(t, err)
	assert.Equal(t, "new words", got.Description, "an empty description leaves it")
	assert.Equal(t, "new@example.com", store.sites[siteID].OwnerEmail)

	_, err = svc.Update(ctx, stranger, "marketing", "x", "")
	require.ErrorIs(t, err, site.ErrNotAllowed)

	var r *RefusedError
	_, err = svc.Update(ctx, admin, "marketing", strings.Repeat("x", site.MaxDescription+1), "")
	require.ErrorAs(t, err, &r)

	_, err = svc.Update(ctx, admin, "nope", "x", "")
	require.ErrorIs(t, err, site.ErrNotFound)
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

	got, err := svc.SetEnvironment(ctx, owner, "marketing", "qa", true)
	require.NoError(t, err)
	assert.True(t, got.Environment("qa").RequiresApproval)
	assert.Equal(t, owner.Email, store.sites[siteID].Environment("qa").UpdatedBy)

	_, err = svc.SetEnvironment(ctx, owner, "marketing", site.EnvProd, false)
	require.ErrorIs(t, err, site.ErrNotAllowed, "only an administrator takes the rule off")
	assert.True(t, store.sites[siteID].Environment(site.EnvProd).RequiresApproval)

	_, err = svc.SetEnvironment(ctx, owner, "marketing", site.EnvProd, true)
	require.NoError(t, err, "keeping the rule is not removing it")

	got, err = svc.SetEnvironment(ctx, admin, "marketing", site.EnvProd, false)
	require.NoError(t, err)
	assert.False(t, got.Environment(site.EnvProd).RequiresApproval)

	_, err = svc.SetEnvironment(ctx, stranger, "marketing", "x", false)
	require.ErrorIs(t, err, site.ErrNotAllowed)
	var r *RefusedError
	_, err = svc.SetEnvironment(ctx, owner, "marketing", "Bad Env", false)
	require.ErrorAs(t, err, &r)
	_, err = svc.SetEnvironment(ctx, owner, "nope", "x", false)
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
	assert.Equal(t, "https://dev--marketing.pages.example.net", first.PreviewURL)
	assert.Equal(t, 1, store.sites[siteID].Environment(site.EnvDev).Release)
	assert.Equal(t, "ci", store.releases[siteID][0].Source)
	assert.Equal(t, owner.Email, store.releases[siteID][0].CreatedBy)
	assert.Equal(t, []string{"marketing"}, invalidated, "this replica's gateway is told")

	second, err := svc.Publish(ctx, owner, PublishRequest{Site: siteID, Environment: "stage", Files: []InputFile{page("a.html")}})
	require.NoError(t, err)
	assert.Equal(t, 2, second.Release.Number)
	assert.Equal(t, "stage", second.Env)
	assert.Equal(t, 1, store.sites[siteID].Environment(site.EnvDev).Release, "dev stays where it was")
	assert.Equal(t, 2, store.sites[siteID].Environment("stage").Release)

	d := store.deployments[siteID][0]
	assert.Equal(t, site.Deployment{Environment: "stage", Action: site.ActionPublish, From: 0, To: 2, Actor: owner.Email}, d)
}

func TestPublish_OwnerOrAdministrator(t *testing.T) {
	cases := []struct {
		name  string
		actor Actor
		err   error
	}{
		{"the owner", owner, nil},
		{"the owner in other capitals", Actor{Email: "JANE@Example.com"}, nil},
		{"an administrator", admin, nil},
		{"someone else", stranger, site.ErrNotAllowed},
		{"no one", Actor{}, site.ErrNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, objects, _ := fixture(t)
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
	t.Run("an environment that requires approval is reached by promote", func(t *testing.T) {
		svc, store, objects, approvals := fixture(t)
		_, err := svc.Publish(context.Background(), admin, PublishRequest{Site: "marketing", Environment: site.EnvProd, Files: []InputFile{page("a")}})
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "requires approval")
		assert.Empty(t, objects.keys())
		assert.Empty(t, store.releases[siteID])
		assert.Empty(t, approvals.selfRecords, "an administrator's publish is no way round it")
	})
	t.Run("no such environment", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.Publish(context.Background(), owner, PublishRequest{Site: "marketing", Environment: "qa", Files: []InputFile{page("a")}})
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
	svc.Config.PagesDomain = "pages.example.net"
	released(store, 1, site.EnvDev)

	got, err := svc.Promote(context.Background(), owner, PromoteRequest{Site: "marketing", From: site.EnvDev, To: "stage"})
	require.NoError(t, err)
	assert.Equal(t, StatusPromoted, got.Status)
	assert.Equal(t, 1, got.Release)
	assert.Equal(t, "https://stage--marketing.pages.example.net", got.URL)
	assert.Nil(t, got.Approval)
	assert.Equal(t, 1, store.sites[siteID].Environment("stage").Release)
	assert.Equal(t, site.ActionPromote, store.deployments[siteID][0].Action)
	assert.Empty(t, approvals.asked, "the queue is not consulted")
	assert.Empty(t, approvals.submitted)
}

func TestPromote_ANonAdministratorWaitsOnARequest(t *testing.T) {
	svc, store, _, approvals := fixture(t)
	rel := released(store, 1, site.EnvDev)

	got, err := svc.Promote(context.Background(), owner, PromoteRequest{Site: "marketing", From: site.EnvDev, To: site.EnvProd, Reason: "launch"})
	require.NoError(t, err)
	assert.Equal(t, StatusAwaitingApproval, got.Status)
	require.NotNil(t, got.Approval)
	assert.Equal(t, "apr_1", got.Approval.ID)
	assert.Empty(t, got.URL, "nothing is served yet")
	assert.Zero(t, store.sites[siteID].Environment(site.EnvProd).Release, "prod did not move")

	require.Len(t, approvals.submitted, 1)
	assert.Equal(t, approval.SubmitRelease{
		ReleaseID: rel.ID, Site: "marketing", Number: 1, Target: site.EnvProd, Digest: rel.ManifestSHA256, Reason: "launch",
	}, approvals.submitted[0])
	assert.Equal(t, approval.Actor{Email: owner.Email, Persona: owner.Persona}, approvals.actors[0])
	assert.Equal(t, [][3]string{{rel.ID, site.EnvProd, rel.ManifestSHA256}}, approvals.asked, "the approval is asked of the digest")
	assert.Empty(t, approvals.selfRecords)
}

func TestPromote_AnAdministratorRecordsTheirOwnApproval(t *testing.T) {
	svc, store, _, approvals := fixture(t)
	rel := released(store, 1, site.EnvDev)

	got, err := svc.Promote(context.Background(), admin, PromoteRequest{Site: "marketing", From: site.EnvDev, To: site.EnvProd, Reason: "hotfix"})
	require.NoError(t, err)
	assert.Equal(t, StatusPromoted, got.Status)
	assert.Equal(t, 1, store.sites[siteID].Environment(site.EnvProd).Release)
	require.Len(t, approvals.selfRecords, 1)
	assert.Equal(t, rel.ManifestSHA256, approvals.selfRecords[0].Digest)
	assert.Equal(t, approval.Actor{Email: admin.Email, Persona: admin.Persona, Admin: true}, approvals.actors[0])
	assert.Empty(t, approvals.submitted)
}

func TestPromote_AnApprovedReleaseGoesThrough(t *testing.T) {
	svc, store, _, approvals := fixture(t)
	released(store, 1, site.EnvDev)
	approvals.approved = true

	got, err := svc.Promote(context.Background(), owner, PromoteRequest{Site: "marketing", From: site.EnvDev, To: site.EnvProd})
	require.NoError(t, err)
	assert.Equal(t, StatusPromoted, got.Status)
	assert.Equal(t, 1, store.sites[siteID].Environment(site.EnvProd).Release)
	assert.Empty(t, approvals.submitted, "no new request")
	assert.Empty(t, approvals.selfRecords, "no new record")
}

func TestPromote_ANamedReleaseIsTheOneMoved(t *testing.T) {
	svc, store, _, _ := fixture(t)
	released(store, 1, "")
	released(store, 2, site.EnvDev)
	got, err := svc.Promote(context.Background(), owner, PromoteRequest{Site: "marketing", To: "stage", Release: 1})
	require.NoError(t, err)
	assert.Equal(t, 1, got.Release)
	assert.Equal(t, 1, store.sites[siteID].Environment("stage").Release)
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
	t.Run("no such source", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.Promote(ctx, owner, PromoteRequest{Site: "marketing", From: "qa", To: "stage"})
		require.ErrorIs(t, err, site.ErrNoEnvironment)
	})
	t.Run("a source serving nothing", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.Promote(ctx, owner, PromoteRequest{Site: "marketing", From: site.EnvDev, To: "stage"})
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "serves no release")
	})
	t.Run("a named release that does not exist", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.Promote(ctx, owner, PromoteRequest{Site: "marketing", To: "stage", Release: 9})
		require.ErrorIs(t, err, site.ErrNoRelease)
	})
	t.Run("no approvals queue", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		released(store, 1, site.EnvDev)
		svc.Approvals = nil
		_, err := svc.Promote(ctx, admin, PromoteRequest{Site: "marketing", From: site.EnvDev, To: site.EnvProd})
		require.ErrorAs(t, err, &r)
		assert.Contains(t, r.Msg, "no approvals queue")
		assert.Zero(t, store.sites[siteID].Environment(site.EnvProd).Release)
	})
	t.Run("the request cannot be filed", func(t *testing.T) {
		svc, store, _, approvals := fixture(t)
		released(store, 1, site.EnvDev)
		approvals.failSubmit = boom
		_, err := svc.Promote(ctx, owner, PromoteRequest{Site: "marketing", From: site.EnvDev, To: site.EnvProd})
		require.ErrorIs(t, err, boom)
		assert.Zero(t, store.sites[siteID].Environment(site.EnvProd).Release)
	})
	t.Run("the approval cannot be recorded", func(t *testing.T) {
		svc, store, _, approvals := fixture(t)
		released(store, 1, site.EnvDev)
		approvals.failSelf = boom
		_, err := svc.Promote(ctx, admin, PromoteRequest{Site: "marketing", From: site.EnvDev, To: site.EnvProd})
		require.ErrorIs(t, err, boom)
		assert.Zero(t, store.sites[siteID].Environment(site.EnvProd).Release, "no record, no move")
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
	svc.Config.PagesDomain = "pages.example.net"
	released(store, 1, site.EnvProd)
	released(store, 2, site.EnvProd)
	released(store, 3, site.EnvProd)

	got, err := svc.Rollback(context.Background(), owner, "marketing", site.EnvProd, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Release)
	assert.Equal(t, site.ActionRollback, got.Status)
	assert.Equal(t, "https://prod--marketing.pages.example.net", got.URL)
	assert.Equal(t, 2, store.sites[siteID].Environment(site.EnvProd).Release)
	assert.Equal(t, site.Deployment{Environment: site.EnvProd, Action: site.ActionRollback, From: 3, To: 2, Actor: owner.Email}, store.deployments[siteID][0])
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

func TestPreviewURLAndPageOrigin(t *testing.T) {
	svc, _, _, _ := fixture(t)
	assert.Empty(t, svc.PreviewURL("marketing", "dev"), "no pages domain, no preview")
	assert.Empty(t, svc.PageOrigin("shop"), "no pages domain, no page origin")

	svc.Config.PagesDomain = "pages.example.net"
	assert.Equal(t, "https://dev--marketing.pages.example.net", svc.PreviewURL("marketing", "dev"))
	assert.Equal(t, "https://shop.pages.example.net", svc.PageOrigin("shop"))

	svc.PublicPort = "8443"
	assert.Equal(t, "https://dev--marketing.pages.example.net:8443", svc.PreviewURL("marketing", "dev"))
	assert.Equal(t, "https://shop.pages.example.net:8443", svc.PageOrigin("shop"))
	assert.Equal(t, "https://www.example.com:8443", svc.Origin("www.example.com"))
}

func domainFixture(t *testing.T) (*Service, *memStore) {
	t.Helper()
	svc, store, _, _ := fixture(t)
	svc.Config = siteconfig.Config{PagesDomain: "pages.example.net"}
	svc.PortalHost = "portal.example.com"
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
		{"the portal's host", DomainRequest{Hostname: "portal.example.com", Site: "marketing", Environment: "prod"}, "the portal's own hostname"},
		{"the portal's host in capitals with a port", DomainRequest{Hostname: "PORTAL.example.com:443", Site: "marketing", Environment: "prod"}, "the portal's own hostname"},
		{"the pages domain", DomainRequest{Hostname: "pages.example.net", Site: "marketing", Environment: "prod"}, "under the pages domain"},
		{"under the pages domain", DomainRequest{Hostname: "shop.pages.example.net", Site: "marketing", Environment: "prod"}, "under the pages domain"},
		{"the cookie domain", DomainRequest{Hostname: "session.example.com", Site: "marketing", Environment: "prod"}, "session cookie's domain"},
		{"under the cookie domain", DomainRequest{Hostname: "a.session.example.com", Site: "marketing", Environment: "prod"}, "session cookie's domain"},
		{"neither site nor application", DomainRequest{Hostname: "www.example.org"}, "maps to a site and environment, or to an application"},
		{"both site and application", DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "prod", Application: "shop"}, "maps to a site and environment, or to an application"},
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

	t.Run("not an administrator", func(t *testing.T) {
		svc, store := domainFixture(t)
		_, err := svc.AddDomain(ctx, owner, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "prod"})
		require.ErrorIs(t, err, site.ErrNotAllowed, "the site's owner does not map hostnames")
		assert.Empty(t, store.domains)
	})
	t.Run("no such site", func(t *testing.T) {
		svc, _ := domainFixture(t)
		_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Site: "nope", Environment: "prod"})
		require.ErrorIs(t, err, site.ErrNotFound)
	})
	t.Run("no such environment", func(t *testing.T) {
		svc, _ := domainFixture(t)
		_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "qa"})
		require.ErrorIs(t, err, site.ErrNoEnvironment)
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
	t.Run("already mapped", func(t *testing.T) {
		svc, _ := domainFixture(t)
		req := DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "prod"}
		_, err := svc.AddDomain(ctx, admin, req)
		require.NoError(t, err)
		_, err = svc.AddDomain(ctx, admin, req)
		require.ErrorIs(t, err, site.ErrDomainTaken)
	})
}

func TestAddDomain_RecordsAnUnverifiedHostname(t *testing.T) {
	svc, store := domainFixture(t)
	ctx := context.Background()

	got, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "WWW.Example.org.", Site: "marketing", Environment: site.EnvProd})
	require.NoError(t, err)
	assert.Equal(t, "www.example.org", got.Hostname, "stored as a request's Host names it")
	assert.Equal(t, siteID, got.SiteID)
	assert.Equal(t, site.EnvProd, got.Environment)
	assert.Empty(t, got.ApplicationID)
	assert.False(t, got.Verified(), "a hostname routes only once verified")
	assert.Equal(t, admin.Email, got.CreatedBy)
	assert.True(t, strings.HasPrefix(got.Token, "platform-site-verify="))

	app, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "shop.example.org", Application: "shop"})
	require.NoError(t, err)
	assert.Equal(t, "app_shop", app.ApplicationID)
	assert.Empty(t, app.SiteID)
	assert.Empty(t, app.Environment)
	assert.NotEqual(t, got.Token, app.Token, "each hostname has a token of its own")
	assert.Len(t, store.domains, 2)
}

func TestAddDomain_ANoCookieDomainDeploymentAcceptsItsSiblings(t *testing.T) {
	svc, _ := domainFixture(t)
	svc.CookieDomain = ""
	_, err := svc.AddDomain(context.Background(), admin, DomainRequest{Hostname: "a.session.example.com", Site: "marketing", Environment: "prod"})
	require.NoError(t, err)
}

func TestVerifyDomain(t *testing.T) {
	ctx := context.Background()
	add := func(t *testing.T) (*Service, *memStore, *fakeVerifier, *site.Domain) {
		t.Helper()
		svc, store := domainFixture(t)
		v := &fakeVerifier{}
		svc.Verifier = v
		d, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "prod"})
		require.NoError(t, err)
		return svc, store, v, d
	}

	t.Run("a passing check routes the hostname", func(t *testing.T) {
		svc, _, v, d := add(t)
		var invalidated []string
		svc.Invalidate = func(h string) { invalidated = append(invalidated, h) }
		got, err := svc.VerifyDomain(ctx, admin, "WWW.example.org")
		require.NoError(t, err)
		assert.True(t, got.Verified())
		assert.Equal(t, admin.Email, got.VerifiedBy)
		assert.Equal(t, [][2]string{{"www.example.org", d.Token}}, v.calls, "the check looks for the stored token")
		assert.Equal(t, []string{"www.example.org"}, invalidated)
	})
	t.Run("not an administrator", func(t *testing.T) {
		svc, store, v, _ := add(t)
		_, err := svc.VerifyDomain(ctx, owner, "www.example.org")
		require.ErrorIs(t, err, site.ErrNotAllowed)
		assert.Empty(t, v.calls)
		assert.Nil(t, store.domains["www.example.org"].VerifiedAt)
	})
	t.Run("no such domain", func(t *testing.T) {
		svc, _, _, _ := add(t)
		_, err := svc.VerifyDomain(ctx, admin, "other.example.org")
		require.ErrorIs(t, err, site.ErrNoDomain)
	})
	t.Run("no verifier", func(t *testing.T) {
		svc, _, _, _ := add(t)
		svc.Verifier = nil
		_, err := svc.VerifyDomain(ctx, admin, "www.example.org")
		var r *RefusedError
		require.ErrorAs(t, err, &r)
	})
	t.Run("a refusal is passed on as itself", func(t *testing.T) {
		svc, store, v, _ := add(t)
		want := &RefusedError{Msg: "publish a TXT record"}
		v.err = want
		_, err := svc.VerifyDomain(ctx, admin, "www.example.org")
		var r *RefusedError
		require.ErrorAs(t, err, &r)
		assert.Same(t, want, r)
		assert.Nil(t, store.domains["www.example.org"].VerifiedAt, "a failed check does not route")
	})
	t.Run("another failure is wrapped", func(t *testing.T) {
		svc, store, v, _ := add(t)
		boom := errors.New("resolver down")
		v.err = boom
		_, err := svc.VerifyDomain(ctx, admin, "www.example.org")
		require.ErrorIs(t, err, boom)
		assert.ErrorContains(t, err, "verifying www.example.org")
		assert.Nil(t, store.domains["www.example.org"].VerifiedAt)
	})
}

func TestRemoveDomain(t *testing.T) {
	ctx := context.Background()
	svc, store := domainFixture(t)
	_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: "www.example.org", Site: "marketing", Environment: "prod"})
	require.NoError(t, err)
	var invalidated []string
	svc.Invalidate = func(h string) { invalidated = append(invalidated, h) }

	require.ErrorIs(t, svc.RemoveDomain(ctx, owner, "www.example.org"), site.ErrNotAllowed)
	assert.Len(t, store.domains, 1)
	require.ErrorIs(t, svc.RemoveDomain(ctx, admin, "other.example.org"), site.ErrNoDomain)
	assert.Empty(t, invalidated)
	require.NoError(t, svc.RemoveDomain(ctx, admin, "WWW.example.org."))
	assert.Empty(t, store.domains)
	assert.Equal(t, []string{"www.example.org"}, invalidated)
}

func TestHosts(t *testing.T) {
	ctx := context.Background()
	svc, store := domainFixture(t)
	for _, h := range []string{"a.example.org", "b.example.org", "c.example.org"} {
		_, err := svc.AddDomain(ctx, admin, DomainRequest{Hostname: h, Site: "marketing", Environment: "prod"})
		require.NoError(t, err)
	}
	require.NoError(t, store.MarkVerified(ctx, "a.example.org", admin.Email))
	require.NoError(t, store.MarkVerified(ctx, "c.example.org", admin.Email))

	got, err := svc.Hosts(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.example.org", "c.example.org"}, got.Hostnames)
	assert.Equal(t, []string{"b.example.org"}, got.Unverified)
	assert.Equal(t, "*.pages.example.net", got.PagesWildcard)

	assert.Contains(t, got.CertManager, "kind: Certificate")
	assert.Contains(t, got.CertManager, "secretName: platform-sites-tls")
	assert.Contains(t, got.CertManager, "    - \"a.example.org\"\n")
	assert.Contains(t, got.CertManager, "    - \"c.example.org\"\n")
	assert.Contains(t, got.CertManager, "    - \"*.pages.example.net\"\n", "the wildcard is quoted, since a bare * is a YAML alias")
	assert.NotContains(t, got.CertManager, "b.example.org", "an unverified hostname gets no certificate")
	assert.NotContains(t, got.Hostnames, "*.pages.example.net", "the hostnames list stays the mapped names")
}

func TestHosts_NoPagesDomainAndNoDomains(t *testing.T) {
	svc, store, _, _ := fixture(t)
	got, err := svc.Hosts(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, got.Hostnames, "none is [], never null")
	assert.NotNil(t, got.Unverified, "none is [], never null")
	assert.Empty(t, got.PagesWildcard)
	assert.NotContains(t, got.CertManager, "*.")

	boom := errors.New("down")
	store.failDomains = boom
	_, err = svc.Hosts(context.Background())
	require.ErrorIs(t, err, boom)
}

func TestLatestRelease(t *testing.T) {
	svc, store, _, _ := fixture(t)
	ctx := context.Background()
	n, err := svc.LatestRelease(ctx, "marketing")
	require.NoError(t, err)
	assert.Zero(t, n)

	released(store, 1, "")
	released(store, 2, "")
	n, err = svc.LatestRelease(ctx, siteID)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	_, err = svc.LatestRelease(ctx, "nope")
	require.ErrorIs(t, err, site.ErrNotFound)

	boom := errors.New("down")
	store.failListing = boom
	_, err = svc.LatestRelease(ctx, "marketing")
	require.ErrorIs(t, err, boom)
}

func TestEnvironmentOwner(t *testing.T) {
	svc, _, _, _ := fixture(t)
	ctx := context.Background()
	got, err := svc.EnvironmentOwner(ctx, "marketing", site.EnvProd)
	require.NoError(t, err)
	assert.Equal(t, owner.Email, got)
	_, err = svc.EnvironmentOwner(ctx, "marketing", "qa")
	require.ErrorIs(t, err, site.ErrNoEnvironment)
	_, err = svc.EnvironmentOwner(ctx, "nope", site.EnvProd)
	require.ErrorIs(t, err, site.ErrNotFound)
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
		require.NoError(t, svc.ApplyPin(ctx, admin.Email, "marketing", site.EnvProd, 1))
		assert.Equal(t, 1, store.sites[siteID].Environment(site.EnvProd).Release)
		assert.Equal(t, site.Deployment{Environment: site.EnvProd, Action: site.ActionApplication, To: 1, Actor: admin.Email}, store.deployments[siteID][0])
		assert.Empty(t, approvals.asked)
	})
	t.Run("an environment already there records no move", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		released(store, 1, site.EnvProd)
		before := len(store.deployments[siteID])
		require.NoError(t, svc.ApplyPin(ctx, admin.Email, "marketing", site.EnvProd, 1))
		assert.Len(t, store.deployments[siteID], before)
	})
	t.Run("no such environment", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		require.ErrorIs(t, svc.ApplyPin(ctx, admin.Email, "marketing", "qa", 1), site.ErrNoEnvironment)
	})
	t.Run("no such site", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		require.ErrorIs(t, svc.ApplyPin(ctx, admin.Email, "nope", site.EnvProd, 1), site.ErrNotFound)
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
		{Path: "index.html", GzipSize: 10, BrotliSize: 9},
		{Path: "logo.png"},
	}}
	store.releases[siteID] = []site.Release{rel}
	for _, key := range []string{
		ObjectKey(siteID, rel.ID, "", "index.html"), ObjectKey(siteID, rel.ID, "gz", "index.html"),
		ObjectKey(siteID, rel.ID, "br", "index.html"), ObjectKey(siteID, rel.ID, "", "logo.png"),
	} {
		require.NoError(t, objects.PutObject(ctx, "portal", key, []byte("x"), "text/plain"))
	}

	require.NoError(t, svc.Delete(ctx, admin, "marketing"))
	assert.Empty(t, objects.keys(), "every form of every file is gone")
}
