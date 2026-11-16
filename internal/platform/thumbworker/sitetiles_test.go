package thumbworker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/site/sitetile"
	"github.com/txn2/mcp-data-platform/internal/site/sitetile/tilepage"
)

// fakeSites hands out work and keeps what the worker recorded.
type fakeSites struct {
	work     []sitetile.Work
	recorded map[string]string
	failed   map[string]string
	held     map[string]time.Duration
}

func newFakeSites(work ...sitetile.Work) *fakeSites {
	return &fakeSites{work: work, recorded: map[string]string{}, failed: map[string]string{}, held: map[string]time.Duration{}}
}

func (f *fakeSites) Claim(context.Context, int, time.Duration, int, int) ([]sitetile.Work, error) {
	w := f.work
	f.work = nil
	return w, nil
}

func (f *fakeSites) Record(_ context.Context, w sitetile.Work, key string, _ int) error {
	f.recorded[w.SiteID+"/"+w.Environment] = key
	return nil
}

func (f *fakeSites) RecordFailure(_ context.Context, w sitetile.Work, reason string, _ int) error {
	f.failed[w.SiteID+"/"+w.Environment] = reason
	return nil
}

func (f *fakeSites) Hold(_ context.Context, siteID, env string, h time.Duration) error {
	f.held[siteID+"/"+env] = h
	return nil
}

func siteRelease(withIndex bool) SitePage {
	return func(context.Context, string, int) (headless.Page, error) {
		if !withIndex {
			return headless.Page{}, tilepage.ErrNoDocument
		}
		return headless.Page{Document: []byte("<index.html>")}, nil
	}
}

// TestDrawSite: an environment's release is drawn from its own files and kept
// under the release it is of; a release with no root page is not drawable; a
// release whose files cannot be read is tried again later (#2658).
func TestDrawSite(t *testing.T) {
	d, blobs := &fakeDrawer{}, newBlobs()
	w := worker(d, nil, blobs)
	w.deps.CollectionPrefix = "p"
	sites := newFakeSites(sitetile.Work{SiteID: "s1", Environment: "prod", Release: 4, Attempts: 1})
	w.deps.Sites, w.deps.SitePage = sites, siteRelease(true)

	require.True(t, w.drawBatch(context.Background(), w.claimSites(context.Background())))
	require.Len(t, d.pages, 1)
	assert.Equal(t, "<index.html>", string(d.pages[0].Document))
	assert.Equal(t, "p/sites/s1/prod/4.png", sites.recorded["s1/prod"])
	assert.Contains(t, blobs.objects, bucket+"/p/sites/s1/prod/4.png")

	sites.work = []sitetile.Work{{SiteID: "s2", Environment: "dev", Release: 1, Attempts: 1}}
	w.deps.SitePage = siteRelease(false)
	w.drawBatch(context.Background(), w.claimSites(context.Background()))
	assert.Contains(t, sites.failed["s2/dev"], "no index.html")

	sites.work = []sitetile.Work{{SiteID: "s3", Environment: "qa", Release: 2, Attempts: 1}}
	w.deps.SitePage = func(context.Context, string, int) (headless.Page, error) {
		return headless.Page{}, errors.New("storage down")
	}
	w.drawBatch(context.Background(), w.claimSites(context.Background()))
	assert.Positive(t, sites.held["s3/qa"], "an unreadable release is held and tried again")

	w.deps.Sites = nil
	assert.Nil(t, w.claimSites(context.Background()), "no store draws none")
}
