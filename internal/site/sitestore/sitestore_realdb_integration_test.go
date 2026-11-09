//go:build integration

package sitestore

import (
	"context"
	"database/sql"
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
		ID: NewSiteID(), Name: name, Description: "d", OwnerEmail: realOwner, CreatedBy: realOwner,
		Environments: []site.Environment{{Name: site.EnvDefault}, {Name: realProd, RequiresApproval: true}},
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
	require.NoError(t, s.RecordRelease(ctx, r))
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
	assert.Zero(t, got.Environment(site.EnvDefault).Release, "no release yet reads as 0")
	assert.True(t, got.Environment(realProd).RequiresApproval)

	_, err = s.Get(ctx, "site_none")
	require.ErrorIs(t, err, site.ErrNotFound)

	require.NoError(t, s.SetEnvironment(ctx, st.ID, "qa", true, realOwner))
	require.NoError(t, s.SetEnvironment(ctx, st.ID, "qa", false, "other@example.com"))
	got, err = s.Get(ctx, st.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Environment("qa"))
	assert.False(t, got.Environment("qa").RequiresApproval, "a second write changes the rule")
	assert.Equal(t, "other@example.com", got.Environment("qa").UpdatedBy)

	assert.False(t, got.InternalView, "a site is private until someone opens it")
	got.Description, got.OwnerEmail, got.InternalView = "new", "new@example.com", true
	require.NoError(t, s.Update(ctx, got))
	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "new@example.com", list[0].OwnerEmail)
	assert.True(t, list[0].InternalView)
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
	require.NoError(t, s.RecordRelease(ctx, third))
	assert.Equal(t, 3, third.Number, "the row numbers the release, so two publishes never share one")
	require.ErrorIs(t, s.RecordRelease(ctx, &site.Release{
		ID: NewReleaseID(), SiteID: "site_none", Number: 1, ManifestSHA256: "x", Redirects: []site.Redirect{}, CreatedBy: realOwner,
	}), site.ErrNotFound)

	rels, err := s.Releases(ctx, st.ID)
	require.NoError(t, err)
	require.Len(t, rels, 3)
	assert.Equal(t, 3, rels[0].Number, "newest first")

	files, err := s.Files(ctx, one.ID)
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Equal(t, "a.png", files[0].Path, "in path order")
	assert.Equal(t, int64(8), files[1].GzipSize)

	require.NoError(t, s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: site.EnvDefault, After: 0, To: 1, Action: site.ActionPublish, Actor: realOwner}))
	require.ErrorIs(t, s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: site.EnvDefault, After: 0, To: 2, Action: site.ActionPublish, Actor: realOwner}),
		site.ErrStale, "the move decided against 0 lost to the move already recorded")
	require.NoError(t, s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: site.EnvDefault, After: 1, To: 2, Action: site.ActionPublish, Actor: realOwner}))
	require.NoError(t, s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: realProd, After: 0, To: 2, Action: site.ActionPromote, Actor: realOwner}))
	require.ErrorIs(t, s.Point(ctx, site.Pointer{SiteID: st.ID, Environment: "qa", To: 1, Action: site.ActionPromote, Actor: realOwner}), site.ErrNoEnvironment)

	after, err := s.Get(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, after.Environment(site.EnvDefault).Release)

	dev, err := s.Deployments(ctx, st.ID, site.EnvDefault, 0)
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
	assert.Equal(t, site.EnvDefault, route.Environment)
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
		Hostname: "www.example.com", SiteID: st.ID, Environment: site.EnvDefault, CreatedBy: realOwner,
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
	assert.False(t, route.Private)
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
		{Hostname: "default.example.com", SiteID: st.ID, Environment: site.EnvDefault, CreatedBy: realOwner},
		{Hostname: "blog.example.com", SiteID: other.ID, Environment: realProd, CreatedBy: realOwner},
	} {
		require.NoError(t, s.AddDomain(ctx, d))
	}

	got, err := s.ApplicationHosts(ctx, app.ID, "pages.example.net")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"www.example.com"}, got, "a part that is not the default environment has no own address")

	require.NoError(t, apps.AddPart(ctx, app.ID, application.Part{Kind: application.KindSite, ID: "blog/default", AddedBy: realOwner}))
	got, err = s.ApplicationHosts(ctx, app.ID, "pages.example.net")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"www.example.com", "blog.pages.example.net"}, got)

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

// A grant is one row per person and site, stored in the form it is matched in,
// and SiteAccess reads it fresh with the site's owner and internal view.
func TestRealDB_GrantsAndSiteAccess(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	st := realSite(ctx, t, s, "marketing")

	access, err := s.SiteAccess(ctx, st.ID, "pat@client.example")
	require.NoError(t, err)
	assert.Equal(t, &site.Access{OwnerEmail: realOwner}, access, "no grant reads as no level")

	require.NoError(t, s.SetGrant(ctx, st.ID, site.Grant{Email: "pat@client.example", Level: site.LevelView, GrantedBy: realOwner}))
	require.NoError(t, s.SetGrant(ctx, st.ID, site.Grant{Email: "mo@example.com", Level: site.LevelManage, GrantedBy: realOwner}))

	access, err = s.SiteAccess(ctx, st.ID, "  PAT@Client.Example ")
	require.NoError(t, err)
	assert.Equal(t, site.LevelView, access.Level, "matched however the address is written")

	level, err := s.GrantLevel(ctx, st.ID, "mo@example.com")
	require.NoError(t, err)
	assert.Equal(t, site.LevelManage, level)
	level, err = s.GrantLevel(ctx, st.ID, "none@example.com")
	require.NoError(t, err)
	assert.Empty(t, level)

	grants, err := s.Grants(ctx, st.ID)
	require.NoError(t, err)
	require.Len(t, grants, 2)
	assert.Equal(t, "mo@example.com", grants[0].Email, "managers list first")
	assert.False(t, grants[0].CreatedAt.IsZero())

	require.NoError(t, s.SetGrant(ctx, st.ID, site.Grant{Email: "mo@example.com", Level: site.LevelView, GrantedBy: "admin@example.com"}))
	level, err = s.GrantLevel(ctx, st.ID, "mo@example.com")
	require.NoError(t, err)
	assert.Equal(t, site.LevelView, level, "a second write changes the level")
	grants, err = s.Grants(ctx, st.ID)
	require.NoError(t, err)
	require.Len(t, grants, 2, "still one row a person")

	require.Error(t, s.SetGrant(ctx, st.ID, site.Grant{Email: "UP@example.com", Level: site.LevelView, GrantedBy: realOwner}),
		"the table holds an address in the form it is matched in")
	require.Error(t, s.SetGrant(ctx, st.ID, site.Grant{Email: "x@example.com", Level: "owner", GrantedBy: realOwner}),
		"the table holds only the two levels")

	require.NoError(t, s.RemoveGrant(ctx, st.ID, "pat@client.example"))
	require.ErrorIs(t, s.RemoveGrant(ctx, st.ID, "pat@client.example"), site.ErrNoGrant)
	access, err = s.SiteAccess(ctx, st.ID, "pat@client.example")
	require.NoError(t, err)
	assert.Empty(t, access.Level, "removing someone takes effect on the next read")

	st.InternalView = true
	require.NoError(t, s.Update(ctx, st))
	access, err = s.SiteAccess(ctx, st.ID, "pat@client.example")
	require.NoError(t, err)
	assert.True(t, access.InternalView)

	_, err = s.SiteAccess(ctx, "site_none", "pat@client.example")
	require.ErrorIs(t, err, site.ErrNotFound)

	require.NoError(t, s.Delete(ctx, st.ID))
	grants, err = s.Grants(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, []site.Grant{}, grants, "a site's grants go with it")
}

// A job is recorded running, reports progress while reading a stop asked for on
// another replica, and records how it ended.
func TestRealDB_Jobs(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	st := realSite(ctx, t, s, "marketing")

	j := &site.Job{ID: NewJobID(), SiteID: st.ID, Kind: site.JobCrawl, Phase: "Copying pages", Environment: site.EnvDefault, Source: "https://old.example.com/", CreatedBy: realOwner}
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

	nilSkipped := &site.Job{ID: NewJobID(), SiteID: st.ID, Kind: site.JobPublish, Environment: site.EnvDefault, CreatedBy: realOwner}
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
