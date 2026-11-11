package sitehttp

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/bucketsource"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// bucketSources is memSources that also reads a bucket prefix, as the caller.
type bucketSources struct {
	*memSources
	bmu      sync.Mutex
	checked  []bucketsource.Source
	read     []string
	checkErr error
}

func (b *bucketSources) CheckBucket(_ context.Context, src bucketsource.Source) error {
	b.bmu.Lock()
	defer b.bmu.Unlock()
	b.checked = append(b.checked, src)
	return b.checkErr
}

func (b *bucketSources) Bucket(ctx context.Context, src bucketsource.Source) ([]sitesvc.InputFile, error) {
	b.bmu.Lock()
	defer b.bmu.Unlock()
	b.read = append(b.read, callerOf(ctx)+" "+src.String())
	return b.files, b.err
}

// A publish from a bucket prefix runs as a job, read as the caller (#2593).
func TestPortal_PublishFromABucketPrefix(t *testing.T) {
	f := newFixture(t)
	src := &bucketSources{memSources: f.sources}
	f.rt.Sources = src
	body := map[string]any{"from_bucket": map[string]any{"connection": "assets", "bucket": "web-site", "prefix": "src/"}, "note": "from the bucket"}
	rec := f.statusIs(t, http.StatusAccepted, route{http.MethodPost, siteBase + "/publish", body}, userManager)
	job := decodeBody[site.Job](t, rec)
	assert.Equal(t, "s3://web-site/src/ on connection assets", job.Source)
	done := awaitJob(t, f)
	assert.Equal(t, site.JobDone, done.Status)
	require.Len(t, src.checked, 1, "the bucket is checked before the job starts")
	src.bmu.Lock()
	assert.Equal(t, []string{"u-mo/mo@example.com/analyst s3://web-site/src/ on connection assets"}, src.read, "read as the caller")
	src.bmu.Unlock()
}

func TestPortal_PublishFromABucketTheCallerCannotRead(t *testing.T) {
	f := newFixture(t)
	src := &bucketSources{memSources: f.sources, checkErr: &sitesvc.RefusedError{Msg: "nothing was published: connection \"finance\" is not one your persona may use"}}
	f.rt.Sources = src
	body := map[string]any{"from_bucket": map[string]any{"connection": "finance", "bucket": "books"}}
	rec := f.statusIs(t, http.StatusUnprocessableEntity, route{http.MethodPost, siteBase + "/publish", body}, userOwner)
	assert.Contains(t, rec.Body.String(), "not one your persona may use")
	assert.Empty(t, src.read, "no job reads a refused bucket")

	rec = f.statusIs(t, http.StatusUnprocessableEntity, route{
		http.MethodPost, siteBase + "/publish",
		map[string]any{"from_bucket": map[string]any{"connection": "a", "bucket": "b"}, "archive": "x"},
	}, userOwner)
	assert.Contains(t, rec.Body.String(), "one source")

	f.rt.Sources = f.sources
	rec = f.statusIs(t, http.StatusUnprocessableEntity, route{http.MethodPost, siteBase + "/publish", body}, userOwner)
	assert.Contains(t, rec.Body.String(), "cannot publish from a bucket")
}

// Someone who may not publish to the site is refused before the bucket is
// checked, so a refusal never says anything of a connection or bucket.
func TestPortal_PublishFromABucketChecksTheSiteFirst(t *testing.T) {
	f := newFixture(t)
	src := &bucketSources{memSources: f.sources, checkErr: &sitesvc.RefusedError{Msg: `limited to buckets whose name starts with "portal-"`}}
	f.rt.Sources = src
	body := map[string]any{"from_bucket": map[string]any{"connection": "finance", "bucket": "books"}}
	for _, u := range []*portal.User{userStranger, userViewer} {
		rec := f.statusIs(t, http.StatusForbidden, route{http.MethodPost, siteBase + "/publish", body}, u)
		assert.NotContains(t, rec.Body.String(), "portal-")
	}
	assert.Empty(t, src.checked, "the bucket is never looked at for someone who may not publish")
	assert.Empty(t, src.read)
}
