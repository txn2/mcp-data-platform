//go:build integration

package sitestore

import (
	"context"
	"database/sql"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appstore"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

const (
	realOwner = "jane@example.com"
	realProd  = "prod"
)

func realSite(ctx context.Context, t *testing.T, s *Store, name string) *site.Site {
	t.Helper()
	st := &site.Site{
		ID: NewSiteID(), Name: name, Description: "d", OwnerEmail: realOwner, CreatedBy: realOwner, MainEnvironment: site.EnvDev,
		Environments: []site.Environment{{Name: site.EnvDev}, {Name: realProd, RequiresApproval: true}},
	}
	require.NoError(t, s.Create(ctx, st))
	return st
}

func realRelease(ctx context.Context, t *testing.T, s *Store, siteID string, n int) *site.Release {
	t.Helper()
	r := &site.Release{
		ID: NewReleaseID(), SiteID: siteID, Number: n, Source: "ci", FileCount: 2, TotalBytes: 30, ManifestSHA256: "digest",
		Redirects: []site.Redirect{{From: "/old/*", To: "/new/:splat", Status: 301}}, CreatedBy: realOwner,
		Files: []site.File{
			{Path: "b/site.css", SHA256: "bb", ContentType: "text/css", Size: 20, GzipSize: 8, BrotliSize: 6},
			{Path: "a.png", SHA256: "aa", ContentType: "image/png", Size: 10},
		},
	}
	require.NoError(t, s.RecordRelease(ctx, r, nil))
	return r
}

func realApplication(ctx context.Context, t *testing.T, db *sql.DB, name string) *application.Application {
	t.Helper()
	a := &application.Application{ID: "app_" + uuid.NewString(), Name: name, OwnerEmail: realOwner, CreatedBy: realOwner}
	require.NoError(t, appstore.New(db).Create(ctx, a))
	return a
}

// A site is created with its environments, a name is held once, and an update
// and a delete find it or say it is gone.
func TestRealDB_SiteRecord(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)

	st := realSite(ctx, t, s, "marketing")
	assert.False(t, st.CreatedAt.IsZero(), "the database stamps it")

	dup := &site.Site{ID: NewSiteID(), Name: "marketing", OwnerEmail: realOwner, CreatedBy: realOwner}
	require.ErrorIs(t, s.Create(ctx, dup), site.ErrNameTaken)

	got, err := s.GetByName(ctx, "marketing")
	require.NoError(t, err)
	assert.Equal(t, st.ID, got.ID)
	require.Len(t, got.Environments, 2)
	assert.Zero(t, got.Environment(site.EnvDev).Release, "no release yet reads as 0")
	assert.True(t, got.Environment(realProd).RequiresApproval)

	_, err = s.Get(ctx, "site_none")
	require.ErrorIs(t, err, site.ErrNotFound)

	yes, no := true, false
	require.NoError(t, s.SetEnvironment(ctx, st.ID, "qa", site.EnvironmentSettings{RequiresApproval: &yes}, realOwner))
	require.NoError(t, s.SetEnvironment(ctx, st.ID, "qa", site.EnvironmentSettings{RequiresApproval: &no}, "other@example.com"))
	got, err = s.Get(ctx, st.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Environment("qa"))
	assert.False(t, got.Environment("qa").RequiresApproval, "a second write changes the rule")
	assert.Equal(t, "other@example.com", got.Environment("qa").UpdatedBy)

	assert.False(t, got.Environment("qa").Public, "an environment is private until someone opens it")
	got.Description, got.OwnerEmail = "new", "new@example.com"
	require.NoError(t, s.Update(ctx, got))
	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "new@example.com", list[0].OwnerEmail)
	assert.Len(t, list[0].Environments, 3)

	require.ErrorIs(t, s.Update(ctx, &site.Site{ID: "site_none"}), site.ErrNotFound)
	require.NoError(t, s.Delete(ctx, st.ID))
	require.ErrorIs(t, s.Delete(ctx, st.ID), site.ErrNotFound)
}

// A release is read back with its files and redirect map, a number is taken
// once, and the pointer moves under a compare-and-set that records each move.
func TestRealDB_ReleaseAndPoint(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	st := realSite(ctx, t, s, "marketing")

	one := realRelease(ctx, t, s, st.ID, 1)
	assert.False(t, one.CreatedAt.IsZero())
	realRelease(ctx, t, s, st.ID, 2)

	got, err := s.Release(ctx, st.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, []site.Redirect{{From: "/old/*", To: "/new/:splat", Status: 301}}, got.Redirects)
	byID, err := s.ReleaseByID(ctx, one.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, byID.Number)
	_, err = s.Release(ctx, st.ID, 9)
	require.ErrorIs(t, err, site.ErrNoRelease)
	_, err = s.ReleaseByID(ctx, "srel_none")
	require.ErrorIs(t, err, site.ErrNoRelease)

	third := &site.Release{ID: NewReleaseID(), SiteID: st.ID, ManifestSHA256: "x", Redirects: []site.Redirect{}, CreatedBy: realOwner}
	require.NoError(t, s.RecordRelease(ctx, third, nil))
	assert.Equal(t, 3, third.Number, "the row numbers the release, so two publishes never share one")
	require.ErrorIs(t, s.RecordRelease(ctx, &site.Release{
		ID: NewReleaseID(), SiteID: "site_none", Number: 1, ManifestSHA256: "x", Redirects: []site.Redirect{}, CreatedBy: realOwner,
	}, nil), site.ErrNotFound)

	rels, err := s.Releases(ctx, st.ID)
	require.NoError(t, err)
	require.Len(t, rels, 3)
	assert.Equal(t, 3, rels[0].Number, "newest first")

	files, err := s.Files(ctx, one.ID)
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Equal(t, "a.png", files[0].Path, "in path order")
	assert.Equal(t, int64(8), files[1].GzipSize)

	require.NoError(t, s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: site.EnvDev, After: 0, To: 1, Action: site.ActionPublish, Actor: realOwner}))
	require.ErrorIs(t, s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: site.EnvDev, After: 0, To: 2, Action: site.ActionPublish, Actor: realOwner}),
		site.ErrStale, "the move decided against 0 lost to the move already recorded")
	require.NoError(t, s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: site.EnvDev, After: 1, To: 2, Action: site.ActionPublish, Actor: realOwner}))
	require.NoError(t, s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: realProd, After: 0, To: 2, Action: site.ActionPromote, Actor: realOwner}))
	require.ErrorIs(t, s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: "qa", To: 1, Action: site.ActionPromote, Actor: realOwner}), site.ErrNoEnvironment)

	after, err := s.Get(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, after.Environment(site.EnvDev).Release)

	dev, err := s.Deployments(ctx, st.ID, site.EnvDev, 0)
	require.NoError(t, err)
	require.Len(t, dev, 2, "the refused move is not recorded")
	assert.Equal(t, 2, dev[0].To, "newest first")
	assert.Equal(t, 1, dev[0].From)
	all, err := s.Deployments(ctx, st.ID, "", 10)
	require.NoError(t, err)
	assert.Len(t, all, 3, "no environment named lists every one")

	route, err := s.RouteOwn(ctx, "marketing")
	require.NoError(t, err)
	require.NotNil(t, route.Release)
	assert.Equal(t, 2, route.Release.Number)
	assert.Equal(t, site.EnvDev, route.Environment)
	assert.True(t, route.Private)
	_, err = s.RouteOwn(ctx, "none")
	require.ErrorIs(t, err, site.ErrNoDomain)
}

// A hostname maps to a site environment or an application, routes as soon as
// it is added, and goes with its site.
func TestRealDB_Domains(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	st := realSite(ctx, t, s, "marketing")
	realRelease(ctx, t, s, st.ID, 1)
	require.NoError(t, s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: realProd, To: 1, Action: site.ActionPromote, Actor: realOwner}))
	app := realApplication(ctx, t, db, "shop")

	d := &site.Domain{Hostname: "www.example.com", SiteID: st.ID, Environment: realProd, CreatedBy: realOwner}
	require.NoError(t, s.AddDomain(ctx, d))
	require.ErrorIs(t, s.AddDomain(ctx, &site.Domain{
		Hostname: "www.example.com", SiteID: st.ID, Environment: site.EnvDev, CreatedBy: realOwner,
	}), site.ErrDomainTaken)
	require.NoError(t, s.AddDomain(ctx, &site.Domain{Hostname: "shop.example.com", ApplicationID: app.ID, CreatedBy: realOwner}))
	require.Error(t, s.AddDomain(ctx, &site.Domain{Hostname: "none.example.com", CreatedBy: realOwner}),
		"a hostname maps to something")

	got, err := s.Domain(ctx, "www.example.com")
	require.NoError(t, err)
	assert.Equal(t, "marketing", got.Site)
	viaApp, err := s.Domain(ctx, "shop.example.com")
	require.NoError(t, err)
	assert.Equal(t, "shop", viaApp.Application)
	assert.Empty(t, viaApp.SiteID)
	_, err = s.Domain(ctx, "none.example.com")
	require.ErrorIs(t, err, site.ErrNoDomain)

	route, err := s.RouteHost(ctx, "www.example.com")
	require.NoError(t, err, "a hostname routes as soon as it is added")
	assert.Equal(t, realProd, route.Environment)
	assert.True(t, route.Private, "a domain serves its environment's access, private until made public")
	require.NotNil(t, route.Release)
	assert.Equal(t, 1, route.Release.Number)

	appRoute, err := s.RouteHost(ctx, "shop.example.com")
	require.NoError(t, err)
	assert.Equal(t, "shop", appRoute.Application)
	assert.Nil(t, appRoute.Release)

	hosts, err := s.Hosts(ctx)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"www.example.com", "shop.example.com"}, hosts)
	list, err := s.Domains(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "shop.example.com", list[0].Hostname, "in hostname order")
	ofSite, err := s.SiteDomains(ctx, st.ID)
	require.NoError(t, err)
	require.Len(t, ofSite, 1)
	assert.Equal(t, "www.example.com", ofSite[0].Hostname)

	require.NoError(t, s.RemoveDomain(ctx, "shop.example.com"))
	require.ErrorIs(t, s.RemoveDomain(ctx, "shop.example.com"), site.ErrNoDomain)

	require.NoError(t, s.Delete(ctx, st.ID))
	_, err = s.Domain(ctx, "www.example.com")
	require.ErrorIs(t, err, site.ErrNoDomain, "a site's domains go with it")
}

// A site environment an application holds as a part names its added
// hostnames, and a part that is a default environment names the site's own
// address; nothing of a part or a hostname that is not the application's.
func TestRealDB_ApplicationHosts(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s, apps := New(db), appstore.New(db)
	st := realSite(ctx, t, s, "marketing")
	other := realSite(ctx, t, s, "blog")
	app := realApplication(ctx, t, db, "shop")
	require.NoError(t, apps.AddPart(ctx, app.ID, application.Part{Kind: application.KindSite, ID: "marketing/prod", AddedBy: realOwner}))

	for _, d := range []*site.Domain{
		{Hostname: "www.example.com", SiteID: st.ID, Environment: realProd, CreatedBy: realOwner},
		{Hostname: "dev.example.com", SiteID: st.ID, Environment: site.EnvDev, CreatedBy: realOwner},
		{Hostname: "blog.example.com", SiteID: other.ID, Environment: realProd, CreatedBy: realOwner},
	} {
		require.NoError(t, s.AddDomain(ctx, d))
	}

	got, err := s.ApplicationHosts(ctx, app.ID, "pages.example.net")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"www.example.com", "marketing--prod.pages.example.net"}, got,
		"a part names its domains and its <site>--<env> address, and the main address only when it holds it")

	require.NoError(t, apps.AddPart(ctx, app.ID, application.Part{Kind: application.KindSite, ID: "blog/dev", AddedBy: realOwner}))
	got, err = s.ApplicationHosts(ctx, app.ID, "pages.example.net")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"www.example.com", "marketing--prod.pages.example.net", "blog--dev.pages.example.net", "blog.pages.example.net"}, got)

	noPages, err := s.ApplicationHosts(ctx, app.ID, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"www.example.com"}, noPages, "no pages domain, no own address")

	none, err := s.ApplicationHosts(ctx, "app_none", "pages.example.net")
	require.NoError(t, err)
	assert.Equal(t, []string{}, none)
}

// A handoff code passes once, and only an expired one is swept.
func TestRealDB_UseHandoffIsSingleUse(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	now := time.Now().UTC()

	first, err := s.UseHandoff(ctx, "hash-1", now.Add(time.Minute))
	require.NoError(t, err)
	assert.True(t, first)
	again, err := s.UseHandoff(ctx, "hash-1", now.Add(time.Minute))
	require.NoError(t, err)
	assert.False(t, again, "the second use of a code is refused")

	expired, err := s.UseHandoff(ctx, "hash-2", now.Add(-time.Minute))
	require.NoError(t, err)
	require.True(t, expired)

	require.NoError(t, s.ExpireHandoffs(ctx, now))
	var left int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM page_handoffs`).Scan(&left))
	assert.Equal(t, 1, left, "the expired row is gone and the live one kept")

	stillUsed, err := s.UseHandoff(ctx, "hash-1", now.Add(time.Minute))
	require.NoError(t, err)
	assert.False(t, stillUsed, "a live code stays used across a sweep")
}

// A grant is one row per person or persona and site, stored in the form it is
// matched in, and SiteAccess reads, fresh and for one environment, the owner,
// that environment's internal view, and whether a grant to the email or the
// persona opens it (#2604).
func TestRealDB_GrantsAndSiteAccess(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	st := realSite(ctx, t, s, "marketing")

	access, err := s.SiteAccess(ctx, st.ID, realProd, "pat@client.example", "")
	require.NoError(t, err)
	assert.Equal(t, &site.Access{OwnerEmail: realOwner}, access, "no grant opens nothing")

	require.NoError(t, s.SetGrant(ctx, st.ID, &site.Grant{Email: "pat@client.example", Level: site.LevelView, Environments: []string{realProd}, GrantedBy: realOwner}))
	require.NoError(t, s.SetGrant(ctx, st.ID, &site.Grant{Email: "mo@example.com", Level: site.LevelManage, GrantedBy: realOwner}))
	require.NoError(t, s.SetGrant(ctx, st.ID, &site.Grant{Persona: "finance-team", Level: site.LevelView, Environments: []string{realProd}, GrantedBy: realOwner}))
	require.NoError(t, s.SetGrant(ctx, st.ID, &site.Grant{Email: "old@example.com", Level: site.LevelView, GrantedBy: realOwner}))

	opens := func(env, email, persona string) (byEmail, byPersona bool) {
		t.Helper()
		a, err := s.SiteAccess(ctx, st.ID, env, email, persona)
		require.NoError(t, err)
		return a.ByEmail, a.ByPersona
	}
	byEmail, _ := opens(realProd, "  PAT@Client.Example ", "")
	assert.True(t, byEmail, "a view grant opens the environment it names, however the address is written")
	byEmail, _ = opens(site.EnvDev, "pat@client.example", "")
	assert.False(t, byEmail, "and no other")
	byEmail, _ = opens(site.EnvDev, "mo@example.com", "")
	assert.True(t, byEmail, "a manage grant is site-wide")
	byEmail, _ = opens(site.EnvDev, "old@example.com", "")
	assert.True(t, byEmail, "a grant naming no environment opens every one")
	_, byPersona := opens(realProd, "anyone@example.com", "finance-team")
	assert.True(t, byPersona, "a persona grant opens for whoever holds it")
	_, byPersona = opens(site.EnvDev, "anyone@example.com", "finance-team")
	assert.False(t, byPersona, "only where it names")
	_, byPersona = opens(realProd, "anyone@example.com", "")
	assert.False(t, byPersona, "no persona matches no persona grant")

	level, err := s.GrantLevel(ctx, st.ID, "mo@example.com")
	require.NoError(t, err)
	assert.Equal(t, site.LevelManage, level)
	level, err = s.GrantLevel(ctx, st.ID, "")
	require.NoError(t, err)
	assert.Empty(t, level, "a persona grant has no email level")

	grants, err := s.Grants(ctx, st.ID)
	require.NoError(t, err)
	require.Len(t, grants, 4)
	assert.Equal(t, "mo@example.com", grants[0].Email, "managers list first")
	assert.Equal(t, []string{realProd}, grants[3].Environments)
	assert.Equal(t, "finance-team", grants[3].Persona, "then people, then personas")

	require.Error(t, s.SetGrant(ctx, st.ID, &site.Grant{Email: "UP@example.com", Level: site.LevelView, GrantedBy: realOwner}),
		"the table holds an address in the form it is matched in")
	require.Error(t, s.SetGrant(ctx, st.ID, &site.Grant{Persona: "finance-team", Level: site.LevelManage, GrantedBy: realOwner}),
		"a persona is only ever given view")
	require.Error(t, s.SetGrant(ctx, st.ID, &site.Grant{Email: "x@example.com", Persona: "p", Level: site.LevelView, GrantedBy: realOwner}),
		"a grant names one principal")

	require.NoError(t, s.RemoveGrant(ctx, st.ID, "", "finance-team"))
	_, byPersona = opens(realProd, "anyone@example.com", "finance-team")
	assert.False(t, byPersona, "removing a persona takes effect on the next read")
	require.NoError(t, s.RemoveGrant(ctx, st.ID, "pat@client.example", ""))
	require.ErrorIs(t, s.RemoveGrant(ctx, st.ID, "pat@client.example", ""), site.ErrNoGrant)

	yes := true
	require.NoError(t, s.SetEnvironment(ctx, st.ID, realProd, site.EnvironmentSettings{InternalView: &yes}, realOwner))
	access, err = s.SiteAccess(ctx, st.ID, realProd, "pat@client.example", "")
	require.NoError(t, err)
	assert.True(t, access.InternalView, "internal view belongs to the environment")
	access, err = s.SiteAccess(ctx, st.ID, site.EnvDev, "pat@client.example", "")
	require.NoError(t, err)
	assert.False(t, access.InternalView)

	_, err = s.SiteAccess(ctx, "site_none", realProd, "pat@client.example", "")
	require.ErrorIs(t, err, site.ErrNotFound)

	require.NoError(t, s.Delete(ctx, st.ID))
	grants, err = s.Grants(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, []site.Grant{}, grants, "a site's grants go with it")
}

// The main address serves the environment it is given to, or nothing; every
// environment answers at <site>--<env>, private unless made public, and dev
// can never be made public (#2604).
func TestRealDB_RoutesAndAccess(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	st := &site.Site{
		ID: NewSiteID(), Name: "intranet", OwnerEmail: realOwner, CreatedBy: realOwner, MainEnvironment: site.EnvDev,
		Environments: []site.Environment{{Name: site.EnvDev}, {Name: realProd}},
	}
	require.NoError(t, s.Create(ctx, st))

	own, err := s.RouteOwn(ctx, "intranet")
	require.NoError(t, err)
	assert.Equal(t, site.EnvDev, own.Environment)
	assert.True(t, own.Private)

	env, err := s.RouteEnv(ctx, "intranet", realProd)
	require.NoError(t, err)
	assert.Equal(t, realProd, env.Environment)
	assert.True(t, env.Private, "a new environment is private")
	assert.Empty(t, env.PartOf)

	require.NoError(t, s.SetMainEnvironment(ctx, st.ID, realProd))
	own, err = s.RouteOwn(ctx, "intranet")
	require.NoError(t, err)
	assert.Equal(t, realProd, own.Environment, "the main address moved")

	require.ErrorIs(t, s.SetMainEnvironment(ctx, st.ID, "nope"), site.ErrNoEnvironment)
	require.NoError(t, s.SetMainEnvironment(ctx, st.ID, ""))
	_, err = s.RouteOwn(ctx, "intranet")
	require.ErrorIs(t, err, site.ErrNoDomain, "a main address given to none serves nothing")

	before, err := s.Get(ctx, st.ID)
	require.NoError(t, err)
	yes := true
	require.NoError(t, s.SetEnvironment(ctx, st.ID, realProd, site.EnvironmentSettings{Public: &yes}, realOwner))
	env, err = s.RouteEnv(ctx, "intranet", realProd)
	require.NoError(t, err)
	assert.False(t, env.Private)
	after, err := s.Get(ctx, st.ID)
	require.NoError(t, err)
	assert.True(t, after.Environment(realProd).AccessChangedAt.After(before.Environment(realProd).AccessChangedAt),
		"a change to public stamps the access")

	require.Error(t, s.SetEnvironment(ctx, st.ID, site.EnvDev, site.EnvironmentSettings{Public: &yes}, realOwner),
		"the database refuses a public dev")
}

// A job is recorded running, reports progress while reading a stop asked for on
// another replica, and records how it ended.
func TestRealDB_Jobs(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	st := realSite(ctx, t, s, "marketing")

	j := &site.Job{ID: NewJobID(), SiteID: st.ID, Kind: site.JobCrawl, Phase: "Copying pages", Environment: site.EnvDev, Source: "https://old.example.com/", CreatedBy: realOwner}
	require.NoError(t, s.CreateJob(ctx, j))
	assert.Equal(t, site.JobRunning, j.Status, "the database starts it running")
	assert.False(t, j.CreatedAt.IsZero())
	assert.Equal(t, []site.Skipped{}, j.Skipped, "none is [], never null")

	stop, err := s.ReportJob(ctx, j.ID, site.JobProgress{Phase: "Copying pages", Done: 2, Total: 9, Pages: 2, Redirects: 1})
	require.NoError(t, err)
	assert.False(t, stop)

	got, err := s.Job(ctx, j.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Done)
	assert.Equal(t, 9, got.Total)
	assert.Equal(t, 1, got.Redirects)
	assert.Equal(t, []site.Skipped{}, got.Skipped)

	active, err := s.ActiveJobs(ctx, st.ID)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, j.ID, active[0].ID)

	require.NoError(t, s.StopJob(ctx, j.ID))
	stop, err = s.ReportJob(ctx, j.ID, site.JobProgress{Phase: "Copying pages", Done: 3, Total: 9, Pages: 3})
	require.NoError(t, err)
	assert.True(t, stop, "the next report reads the stop")

	skipped := []site.Skipped{{Address: "/gone", Reason: "answered 404"}}
	require.NoError(t, s.FinishJob(ctx, j.ID, site.JobResult{Status: site.JobStopped, Pages: 3, Redirects: 1, Skipped: skipped, Error: ""}))
	got, err = s.Job(ctx, j.ID)
	require.NoError(t, err)
	assert.Equal(t, site.JobStopped, got.Status)
	assert.Equal(t, skipped, got.Skipped)
	require.ErrorIs(t, s.StopJob(ctx, j.ID), site.ErrNoJob, "only a running job can be asked to stop")

	active, err = s.ActiveJobs(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, []site.Job{}, active, "a finished job is not active")

	_, err = s.Job(ctx, "sjob_none")
	require.ErrorIs(t, err, site.ErrNoJob)
	_, err = s.ReportJob(ctx, "sjob_none", site.JobProgress{})
	require.ErrorIs(t, err, site.ErrNoJob)
	require.ErrorIs(t, s.FinishJob(ctx, "sjob_none", site.JobResult{Status: site.JobDone}), site.ErrNoJob)

	nilSkipped := &site.Job{ID: NewJobID(), SiteID: st.ID, Kind: site.JobPublish, Environment: site.EnvDev, CreatedBy: realOwner}
	require.NoError(t, s.CreateJob(ctx, nilSkipped))
	require.NoError(t, s.FinishJob(ctx, nilSkipped.ID, site.JobResult{Status: site.JobDone, Release: 4}))
	got, err = s.Job(ctx, nilSkipped.ID)
	require.NoError(t, err)
	assert.Equal(t, 4, got.Release)
	assert.Equal(t, []site.Skipped{}, got.Skipped)

	require.NoError(t, s.Delete(ctx, st.ID))
	_, err = s.Job(ctx, j.ID)
	require.ErrorIs(t, err, site.ErrNoJob, "a site's jobs go with it")
}

// A derived release is only what differs from its parent, and reads as the whole
// file set: nearest row per path, removals honored, through a chain of releases.
func TestRealDB_DerivedReleases(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	st := realSite(ctx, t, s, "derived")

	record := func(r *site.Release, move *site.Pointer) *site.Release {
		t.Helper()
		r.ID, r.SiteID, r.ManifestSHA256, r.CreatedBy = NewReleaseID(), st.ID, "digest", realOwner
		r.Redirects = []site.Redirect{}
		require.NoError(t, s.RecordRelease(ctx, r, move))
		return r
	}
	file := func(p, sum string) site.File {
		return site.File{Path: p, SHA256: sum, ContentType: "text/plain", Size: 1}
	}

	root := record(&site.Release{Files: []site.File{file("a", "a1"), file("b", "b1"), file("c", "c1")}}, nil)
	mid := record(&site.Release{Base: root.Number, ManifestParent: root.ID, ManifestDepth: 1, Files: []site.File{file("a", "a2")}, Removed: []string{"b"}}, nil)
	tip := record(&site.Release{Base: mid.Number, ManifestParent: mid.ID, ManifestDepth: 2, Files: []site.File{file("b", "b3"), file("d", "d1")}}, nil)

	read := func(id string) map[string]string {
		t.Helper()
		files, err := s.Files(ctx, id)
		require.NoError(t, err)
		out := map[string]string{}
		for _, f := range files {
			out[f.Path] = f.SHA256
		}
		return out
	}
	assert.Equal(t, map[string]string{"a": "a1", "b": "b1", "c": "c1"}, read(root.ID), "a parent never changes")
	assert.Equal(t, map[string]string{"a": "a2", "c": "c1"}, read(mid.ID), "a removal hides the parent's row")
	assert.Equal(t, map[string]string{"a": "a2", "b": "b3", "c": "c1", "d": "d1"}, read(tip.ID), "and a later release can add it back")

	var rows int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM site_release_files WHERE release_id = $1`, mid.ID).Scan(&rows))
	assert.Equal(t, 2, rows, "one changed file and one removal, however many files the release holds")

	got, err := s.Release(ctx, st.ID, tip.Number)
	require.NoError(t, err)
	assert.Equal(t, mid.Number, got.Base)
	assert.Equal(t, mid.ID, got.ManifestParent)
	assert.Equal(t, 2, got.ManifestDepth)
}

// Writing a release and moving to it are one transaction: a move that lost the
// race leaves no release, and the number is not burned.
func TestRealDB_AMoveThatLosesLeavesNoRelease(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	st := realSite(ctx, t, s, "race")
	release := func() *site.Release {
		return &site.Release{
			ID: NewReleaseID(), SiteID: st.ID, ManifestSHA256: "d", Redirects: []site.Redirect{}, CreatedBy: realOwner,
			Files: []site.File{{Path: "a", SHA256: "a", ContentType: "text/plain", Size: 1}},
		}
	}
	first := release()
	require.NoError(t, s.RecordRelease(ctx, first, &site.Pointer{SiteID: st.ID, Environment: site.EnvDev, After: 0, Action: site.ActionPublish, Actor: realOwner}))
	got, err := s.Get(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, first.Number, got.Environment(site.EnvDev).Release, "the environment moved with the write")

	stale := release()
	err = s.RecordRelease(ctx, stale, &site.Pointer{SiteID: st.ID, Environment: site.EnvDev, After: 0, Action: site.ActionPublish, Actor: realOwner})
	require.ErrorIs(t, err, site.ErrStale)
	list, err := s.Releases(ctx, st.ID)
	require.NoError(t, err)
	assert.Len(t, list, 1, "the losing release was never written")
}

// A digest shared by many releases is one object; a file under a release's own
// prefix is one object per path.
func TestRealDB_StoredFilesListsEachObjectOnce(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	st := realSite(ctx, t, s, "stored")
	for range 3 {
		require.NoError(t, s.RecordRelease(ctx, &site.Release{
			ID: NewReleaseID(), SiteID: st.ID, ManifestSHA256: "d", Redirects: []site.Redirect{}, CreatedBy: realOwner,
			Files: []site.File{
				{Path: "a", SHA256: "shared", ContentType: "text/plain", Size: 1},
				{Path: "legacy", SHA256: "old", ContentType: "text/plain", Size: 1, StoredIn: "srel_old"},
			}, Removed: []string{"gone"},
		}, nil))
	}
	got, err := s.StoredFiles(ctx, st.ID)
	require.NoError(t, err)
	require.Len(t, got, 2, "a removal holds no object, and the shared digest is listed once")
}

// A file belongs to a tree: a source file and the output rendered from it can
// share a path and differ, and each tree walks the chain on its own.
func TestRealDB_ATreeIsResolvedOnItsOwn(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	st := realSite(ctx, t, s, "trees")
	record := func(r *site.Release) *site.Release {
		t.Helper()
		r.ID, r.SiteID, r.ManifestSHA256, r.CreatedBy, r.Rendered = NewReleaseID(), st.ID, "digest", realOwner, true
		r.Redirects = []site.Redirect{}
		require.NoError(t, s.RecordRelease(ctx, r, nil))
		return r
	}
	file := func(p, sum string) site.File {
		return site.File{Path: p, SHA256: sum, ContentType: "text/html", Size: 1}
	}
	index := file("index.json.gz", "idx")
	root := record(&site.Release{
		Files:       []site.File{file("index.html", "out1")},
		SourceFiles: []site.File{file("index.html", "src1"), file("_layouts/base.html", "lay1")},
		RenderIndex: &index,
	})
	tip := record(&site.Release{
		ManifestParent: root.ID, ManifestDepth: 1,
		Files: []site.File{file("index.html", "out2")}, SourceFiles: []site.File{file("_layouts/base.html", "lay2")},
	})
	read := func(id, tree string) map[string]string {
		t.Helper()
		files, err := s.Tree(ctx, id, tree)
		require.NoError(t, err)
		out := map[string]string{}
		for _, f := range files {
			out[f.Path] = f.SHA256
		}
		return out
	}
	assert.Equal(t, map[string]string{"index.html": "out2"}, read(tip.ID, site.TreeOutput))
	assert.Equal(t, map[string]string{"index.html": "src1", "_layouts/base.html": "lay2"}, read(tip.ID, site.TreeSource))
	assert.Equal(t, map[string]string{"index.json.gz": "idx"}, read(root.ID, site.TreeRender))

	served, err := s.Files(ctx, tip.ID)
	require.NoError(t, err)
	require.Len(t, served, 1, "what a gateway reads is the output alone")

	got, err := s.Release(ctx, st.ID, tip.Number)
	require.NoError(t, err)
	assert.True(t, got.Rendered)
}

// A sweep (#2602) of a chain of 40 derived releases keeps what each
// environment's window, the release it serves and nothing else holds; every
// kept release resolves to the files it held before; report mode decides the
// same and writes nothing; and the objects it returns are exactly those no
// kept release names, a file under an old release's prefix included.
func TestRealDB_PruneKeepsEveryRetainedReleaseIntact(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	st := realSite(ctx, t, s, "pruned")
	require.NoError(t, s.SetEnvironment(ctx, st.ID, "stage", site.EnvironmentSettings{}, realOwner))
	file := func(p, sum string) site.File {
		return site.File{Path: p, SHA256: sum, ContentType: "text/plain", Size: 1, GzipSize: 1}
	}
	note := func(n int) string { return "note" + strconv.Itoa(n) }

	rels := map[int]*site.Release{}
	root := &site.Release{
		ID: NewReleaseID(), SiteID: st.ID, ManifestSHA256: "digest", CreatedBy: realOwner, Redirects: []site.Redirect{},
		Files: []site.File{file("index", "i1"), file("shared", "sh"), file("n1", note(1))},
	}
	require.NoError(t, s.RecordRelease(ctx, root, &site.Pointer{SiteID: st.ID, Environment: site.EnvDev, Action: site.ActionPublish, Actor: realOwner}))
	rels[1] = root
	// A file stored under the first release's own prefix, as releases before
	// content addressing were, carried by every later release by path.
	_, err := db.ExecContext(ctx, `INSERT INTO site_release_files (release_id, tree, path, sha256, content_type, size, stored_in)
		VALUES ($1, 'output', 'legacy', 'lg', 'text/plain', 1, $1)`, root.ID)
	require.NoError(t, err)
	for n := 2; n <= 40; n++ {
		prev := rels[n-1]
		r := &site.Release{
			ID: NewReleaseID(), SiteID: st.ID, ManifestSHA256: "digest", CreatedBy: realOwner, Redirects: []site.Redirect{},
			Base: prev.Number, ManifestParent: prev.ID, ManifestDepth: prev.ManifestDepth + 1,
			Files: []site.File{file("index", "i"+strconv.Itoa(n)), file("n"+strconv.Itoa(n), note(n))}, Removed: []string{"n" + strconv.Itoa(n-1)},
		}
		require.NoError(t, s.RecordRelease(ctx, r, &site.Pointer{SiteID: st.ID, Environment: site.EnvDev, After: prev.Number, Action: site.ActionPublish, Actor: realOwner}))
		rels[n] = r
	}
	require.NoError(t, s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: realProd, To: 10, Action: site.ActionPromote, Actor: realOwner}))
	require.NoError(t, s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: "stage", To: 20, Action: site.ActionPromote, Actor: realOwner}))
	for env, keep := range map[string]int{site.EnvDev: 5, realProd: 1, "stage": 1} {
		require.NoError(t, s.SetEnvironment(ctx, st.ID, env, site.EnvironmentSettings{Keep: &keep}, realOwner))
	}

	kept := []int{10, 20, 36, 37, 38, 39, 40}
	before := map[int]map[string][]site.File{}
	for _, n := range kept {
		before[n] = map[string][]site.File{}
		for _, kind := range treeKinds {
			files, err := s.Tree(ctx, rels[n].ID, kind)
			require.NoError(t, err)
			before[n][kind] = files
		}
	}
	var want []int
	for n := 1; n <= 40; n++ {
		if !slices.Contains(kept, n) {
			want = append(want, n)
		}
	}

	report, err := s.Prune(ctx, st.ID, false)
	require.NoError(t, err)
	assert.Equal(t, want, report.Releases)
	got, err := s.Release(ctx, st.ID, 5)
	require.NoError(t, err)
	assert.False(t, got.Pruned, "report mode writes nothing")

	done, err := s.Prune(ctx, st.ID, true)
	require.NoError(t, err)
	assert.Equal(t, report.Releases, done.Releases)
	assert.Equal(t, report.Bytes, done.Bytes)
	assert.Len(t, done.Objects, len(report.Objects))

	gone := map[string]bool{}
	for _, f := range done.Objects {
		gone[objectKey(f)] = true
	}
	for _, n := range want {
		assert.True(t, gone["d:"+note(n)], "release %d's note is named by no kept release", n)
	}
	for _, n := range kept {
		assert.False(t, gone["d:"+note(n)], "release %d's note is kept", n)
	}
	assert.False(t, gone["d:sh"], "a file every release shares stays")
	assert.False(t, gone["r:"+root.ID+"\x00legacy"], "a file under a pruned release's prefix that kept releases name stays")

	for _, n := range kept {
		for _, kind := range treeKinds {
			files, err := s.Tree(ctx, rels[n].ID, kind)
			require.NoError(t, err)
			assert.Equal(t, before[n][kind], files, "release %d still resolves to its %s tree", n, kind)
		}
	}
	for _, n := range want {
		got, err := s.Release(ctx, st.ID, n)
		require.NoError(t, err)
		assert.True(t, got.Pruned, "release %d keeps its record, marked", n)
		var rows int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM site_release_files WHERE release_id = $1`, rels[n].ID).Scan(&rows))
		assert.Zero(t, rows, "release %d's file rows are gone", n)
	}
	deployments, err := s.Deployments(ctx, st.ID, site.EnvDev, 100)
	require.NoError(t, err)
	assert.Len(t, deployments, 40, "the deployment history naming pruned releases stays")

	again, err := s.Prune(ctx, st.ID, true)
	require.NoError(t, err)
	assert.Empty(t, again.Releases, "a second sweep finds nothing more")

	why, err := s.KeptBy(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{realProd}, why[10])
	assert.Equal(t, []string{"stage"}, why[20])
	assert.Equal(t, []string{site.EnvDev}, why[38])
	assert.Empty(t, why[5])

	fresh, err := s.Get(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, 5, fresh.Environment(site.EnvDev).Kept)
	assert.Equal(t, 1, fresh.Environment(realProd).Kept)
}

// A site with a job running is left for the next sweep, and one where every
// environment keeps every release is never pruned.
func TestRealDB_PruneLeavesBusyAndKeepAllSitesAlone(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	st := realSite(ctx, t, s, "busy")
	for n := 1; n <= 3; n++ {
		realRelease(ctx, t, s, st.ID, n)
	}
	zero := 0
	for _, env := range []string{site.EnvDev, realProd} {
		require.NoError(t, s.SetEnvironment(ctx, st.ID, env, site.EnvironmentSettings{Keep: &zero}, realOwner))
	}
	res, err := s.Prune(ctx, st.ID, true)
	require.NoError(t, err)
	assert.Empty(t, res.Releases, "every environment keeps every release, including those never served")

	one := 1
	require.NoError(t, s.SetEnvironment(ctx, st.ID, site.EnvDev, site.EnvironmentSettings{Keep: &one}, realOwner))
	require.NoError(t, s.CreateJob(ctx, &site.Job{ID: "job_" + uuid.NewString(), SiteID: st.ID, Kind: site.JobPublish, Environment: site.EnvDev, CreatedBy: realOwner}))
	res, err = s.Prune(ctx, st.ID, true)
	require.NoError(t, err)
	assert.True(t, res.Skipped)

	_, err = s.Prune(ctx, NewSiteID(), true)
	require.ErrorIs(t, err, site.ErrNotFound)

	w := &Sweep{Mode: "delete", Sites: 1, StartedAt: time.Now().UTC()}
	require.NoError(t, s.RecordSweep(ctx, w))
	sweeps, err := s.Sweeps(ctx, 5)
	require.NoError(t, err)
	require.NotEmpty(t, sweeps)
	assert.Equal(t, 1, sweeps[0].Sites)
	refs, err := s.SiteRefs(ctx)
	require.NoError(t, err)
	assert.Contains(t, refs, SiteRef{ID: st.ID, Name: st.Name})
}

// A publish's hold on a site's stored files and a sweep's exclude each other
// (#2602): while a publish holds them a sweep takes no hold and prunes
// nothing; while a sweep holds them a publish is refused past its wait; a
// hold left by a process that died stops counting when it expires; and a
// publish whose hold ran out records nothing.
func TestRealDB_HoldsKeepPublishAndSweepApart(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	s.holds = holdTiming{wait: 50 * time.Millisecond, poll: 10 * time.Millisecond, renewAfter: time.Nanosecond}
	st := realSite(ctx, t, s, "held")
	for n := 1; n <= 3; n++ {
		realRelease(ctx, t, s, st.ID, n)
	}
	one := 1
	require.NoError(t, s.SetEnvironment(ctx, st.ID, site.EnvDev, site.EnvironmentSettings{Keep: &one}, realOwner))
	expire := func(t *testing.T, kind string) {
		t.Helper()
		_, err := db.ExecContext(ctx, `UPDATE site_object_holds SET expires_at = clock_timestamp() - interval '1 second' WHERE site_id = $1 AND kind = $2`, st.ID, kind)
		require.NoError(t, err)
	}

	upload, err := s.HoldUpload(ctx, st.ID)
	require.NoError(t, err)
	require.NotNil(t, upload)
	sweep, err := s.HoldSweep(ctx, st.ID)
	require.NoError(t, err)
	assert.Nil(t, sweep, "a sweep takes no hold while a publish stores files")
	res, err := s.Prune(ctx, st.ID, true)
	require.NoError(t, err)
	assert.True(t, res.Skipped, "nor does it prune")
	require.NoError(t, upload.Renew(ctx), "a live hold renews")
	upload.Release(ctx)

	sweep, err = s.HoldSweep(ctx, st.ID)
	require.NoError(t, err)
	require.NotNil(t, sweep)
	_, err = s.HoldUpload(ctx, st.ID)
	require.ErrorIs(t, err, ErrCleanupRunning, "a publish never stores while a sweep deletes")
	expire(t, holdSweep)
	upload, err = s.HoldUpload(ctx, st.ID)
	require.NoError(t, err)
	require.NotNil(t, upload, "a sweep's hold that expired stops counting")
	require.ErrorIs(t, sweep.Renew(ctx), ErrHoldExpired, "and the sweep that held it must stop")

	expire(t, holdUpload)
	r := &site.Release{ID: NewReleaseID(), SiteID: st.ID, Source: "ci", ManifestSHA256: "d", CreatedBy: realOwner, Redirects: []site.Redirect{}}
	require.ErrorIs(t, s.RecordRelease(WithHold(ctx, upload), r, nil), ErrHoldExpired)
	rels, err := s.Releases(ctx, st.ID)
	require.NoError(t, err)
	assert.Len(t, rels, 3, "a publish whose hold ran out records nothing")
	require.ErrorIs(t, upload.Renew(ctx), ErrHoldExpired)

	sweep, err = s.HoldSweep(ctx, st.ID)
	require.NoError(t, err)
	require.NotNil(t, sweep, "an upload hold that expired stops counting too")
	sweep.Release(ctx)
	upload, err = s.HoldUpload(ctx, st.ID)
	require.NoError(t, err)
	require.NoError(t, s.RecordRelease(WithHold(ctx, upload), r, nil), "a live hold records")
	upload.Release(ctx)
}

// A move never lands on a release a sweep pruned under it (#2602): a move
// started while a sweep holds the site's row waits for the sweep and is then
// refused, where before it moved after the sweep committed.
func TestRealDB_AMoveNeverLandsOnAPrunedRelease(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	st := realSite(ctx, t, s, "moving")
	for n := 1; n <= 3; n++ {
		realRelease(ctx, t, s, st.ID, n)
		require.NoError(t, s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: site.EnvDev, After: n - 1, To: n, Action: site.ActionPublish, Actor: realOwner}))
	}

	sweep, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = sweep.Rollback() }()
	_, err = sweep.ExecContext(ctx, `SELECT 1 FROM sites WHERE id = $1 FOR UPDATE`, st.ID)
	require.NoError(t, err)
	_, err = sweep.ExecContext(ctx, `UPDATE site_releases SET pruned_at = NOW() WHERE site_id = $1 AND number = 2`, st.ID)
	require.NoError(t, err)

	moved := make(chan error, 1)
	go func() {
		moved <- s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: site.EnvDev, After: 3, To: 2, Action: site.ActionRollback, Actor: realOwner})
	}()
	waiting := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && !waiting; time.Sleep(10 * time.Millisecond) {
		require.NoError(t, db.QueryRowContext(ctx, `
			SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock')`).Scan(&waiting))
	}
	assert.True(t, waiting, "the move waits for the sweep's lock")
	require.NoError(t, sweep.Commit())

	select {
	case err := <-moved:
		require.ErrorIs(t, err, site.ErrPruned)
	case <-time.After(10 * time.Second):
		t.Fatal("the move never finished")
	}
	got, err := s.GetByName(ctx, "moving")
	require.NoError(t, err)
	assert.Equal(t, 3, got.Environment(site.EnvDev).Release, "dev still serves what it served")
}
