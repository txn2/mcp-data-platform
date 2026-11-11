package sitehttp

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/siteprune"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
)

// sweepStore is the release sweep's store for the routes' tests.
type sweepStore struct {
	sweeps []sitestore.Sweep
}

func (*sweepStore) SiteRefs(context.Context) ([]sitestore.SiteRef, error) {
	return []sitestore.SiteRef{{ID: siteID, Name: siteName}}, nil
}

func (*sweepStore) GetByName(_ context.Context, name string) (*site.Site, error) {
	if name != siteName {
		return nil, site.ErrNotFound
	}
	return &site.Site{ID: siteID, Name: siteName}, nil
}

func (*sweepStore) Prune(context.Context, string, bool) (*sitestore.PruneResult, error) {
	return &sitestore.PruneResult{Releases: []int{1, 2}, Objects: []site.File{{SHA256: "aa"}}, Bytes: 12}, nil
}

func (*sweepStore) StoredFiles(context.Context, string) ([]site.File, error) { return nil, nil }

// HoldSweep holds nothing: no publish runs beside these routes' sweeps.
func (*sweepStore) HoldSweep(context.Context, string) (*sitestore.Hold, error) {
	return &sitestore.Hold{}, nil
}

func (s *sweepStore) RecordSweep(_ context.Context, w *sitestore.Sweep) error {
	s.sweeps = append(s.sweeps, *w)
	return nil
}

func (s *sweepStore) Sweeps(context.Context, int) ([]sitestore.Sweep, error) { return s.sweeps, nil }

func TestAdmin_Retention(t *testing.T) {
	f := newAdmin(t)
	cols := []string{"mode", "run_by", "scope", "sites", "releases", "rewritten", "files", "bytes", "orphans", "orphan_bytes", "skipped", "refused", "failed", "started_at", "finished_at"}
	f.mock.ExpectQuery(regexp.QuoteMeta("FROM site_retention_sweeps")).
		WillReturnRows(sqlmock.NewRows(cols).AddRow("delete", "", "", 4, 9, 1, 12, 4096, 2, 10, 0, 0, 0, stamp, stamp))
	rec := f.do(t, http.MethodGet, adminRetention, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeBody[RetentionView](t, rec)
	assert.Equal(t, siteconfig.DefaultKeepReleases, got.KeepDefault)
	assert.Equal(t, siteconfig.ModeDelete, got.Mode)
	assert.Equal(t, "24h0m0s", got.OrphanGrace)
	require.Len(t, got.Sweeps, 1)
	assert.Equal(t, 9, got.Sweeps[0].Releases)

	f.mock.ExpectQuery(regexp.QuoteMeta("FROM site_retention_sweeps")).WillReturnError(errors.New("down"))
	rec = f.do(t, http.MethodGet, adminRetention, "")
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestAdmin_RunSweep(t *testing.T) {
	f := newAdmin(t)
	store := &sweepStore{}
	f.rt.Prune = &siteprune.Runner{Store: store, Config: f.rt.Live.Get}

	rec := f.do(t, http.MethodPost, adminRetention+"/run", `{"mode":"report","site":"`+siteName+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeBody[siteprune.Report](t, rec)
	assert.Equal(t, 2, got.Releases)
	assert.Equal(t, 1, got.Files)
	assert.Equal(t, int64(12), got.Bytes)
	require.Len(t, store.sweeps, 1)
	assert.Equal(t, adminEmail, store.sweeps[0].RunBy, "who asked is recorded")
	assert.Equal(t, siteName, store.sweeps[0].Scope)

	for name, tc := range map[string]struct {
		body   string
		status int
	}{
		"an unknown mode": {`{"mode":"sometimes"}`, http.StatusBadRequest},
		"no such site":    {`{"mode":"report","site":"nope"}`, http.StatusNotFound},
		"bad JSON":        {`{`, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.do(t, http.MethodPost, adminRetention+"/run", tc.body)
			assert.Equal(t, tc.status, rec.Code, rec.Body.String())
		})
	}
}

// TestPortal_ReleaseRoutesNameOnlyWhatTheCallerMayKnow: someone who does not
// manage a site sees, on its releases, history and jobs, the environments they
// can open and nothing of the private ones, what keeps a release only by those
// environments, and no release's or job's source; a manager sees all of it.
func TestPortal_ReleaseRoutesNameOnlyWhatTheCallerMayKnow(t *testing.T) {
	f := newFixture(t)
	f.release(1, site.EnvDev)
	f.store.releases[siteID][0].Source = "s3://lake/site/ on connection lake"
	f.store.jobs["sjob_dev"] = &site.Job{ID: "sjob_dev", SiteID: siteID, Environment: site.EnvDev, Source: "s3://lake/site/", Status: site.JobRunning, UpdatedAt: time.Now()}

	rels := decodeBody[ReleaseList](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, siteBase + "/releases", nil}, userStranger))
	require.Len(t, rels.Releases, 1)
	assert.Empty(t, rels.Releases[0].ServedIn, "a private environment is not named to a stranger")
	assert.NotNil(t, rels.Releases[0].ServedIn)
	assert.Empty(t, rels.Releases[0].Source)
	one := decodeBody[ReleaseDetail](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, siteBase + "/releases/1", nil}, userStranger))
	assert.Empty(t, one.ServedIn)
	assert.Empty(t, one.Source)
	deps := decodeBody[DeploymentList](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, siteBase + "/deployments", nil}, userStranger))
	assert.NotNil(t, deps.Deployments)
	assert.Empty(t, deps.Deployments)
	jobs := decodeBody[JobList](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, siteBase + "/jobs", nil}, userStranger))
	assert.NotNil(t, jobs.Jobs)
	assert.Empty(t, jobs.Jobs)
	f.statusIs(t, http.StatusNotFound, route{http.MethodGet, siteBase + "/jobs/sjob_dev", nil}, userStranger)

	rels = decodeBody[ReleaseList](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, siteBase + "/releases", nil}, userManager))
	assert.Equal(t, []string{site.EnvDev}, rels.Releases[0].ServedIn)
	assert.Contains(t, rels.Releases[0].Source, "lake")
	deps = decodeBody[DeploymentList](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, siteBase + "/deployments", nil}, userManager))
	assert.Len(t, deps.Deployments, 1)
	job := decodeBody[site.Job](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, siteBase + "/jobs/sjob_dev", nil}, userManager))
	assert.Equal(t, "s3://lake/site/", job.Source)

	// A public environment is anyone's to know of.
	f.store.sites[siteID].Environment(site.EnvDev).Public = true
	rels = decodeBody[ReleaseList](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, siteBase + "/releases", nil}, userStranger))
	assert.Equal(t, []string{site.EnvDev}, rels.Releases[0].ServedIn)
	jobs = decodeBody[JobList](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, siteBase + "/jobs", nil}, userStranger))
	require.Len(t, jobs.Jobs, 1)
	assert.Empty(t, jobs.Jobs[0].Source)

	for _, path := range []string{"/releases", "/releases/1", "/compare?to=1", "/deployments", "/jobs", "/jobs/sjob_dev"} {
		f.statusIs(t, http.StatusUnauthorized, route{http.MethodGet, siteBase + path, nil}, nil)
	}
}

// TestEnvScope_KeptByReasons: what keeps a release is said to someone who does
// not manage the site only by the environments they can open.
func TestEnvScope_KeptByReasons(t *testing.T) {
	reasons := []string{site.EnvDev, "application shop", "approval", envProd}
	assert.Equal(t, reasons, envScope{manage: true}.names(reasons))
	assert.Equal(t, []string{envProd}, envScope{open: map[string]bool{envProd: true}}.names(reasons))
	assert.Equal(t, []string{}, envScope{}.names(reasons))
	view := envScope{open: map[string]bool{envProd: true}}.releaseView(&site.Site{}, site.Release{Number: 3, Pruned: true, Source: "x"}, reasons)
	assert.Equal(t, []string{}, view.KeptBy, "a removed release is kept by nothing")
	assert.Empty(t, view.Source)
}
