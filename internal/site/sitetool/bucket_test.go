package sitetool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/bucketsource"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// bucketSources is memSources that also reads a bucket prefix.
type bucketSources struct {
	*memSources
	asked []bucketsource.Source
	err   error
}

func (b *bucketSources) CheckBucket(_ context.Context, src bucketsource.Source) error {
	b.asked = append(b.asked, src)
	return b.err
}

func (b *bucketSources) Bucket(_ context.Context, src bucketsource.Source) ([]sitesvc.InputFile, error) {
	b.asked = append(b.asked, src)
	return b.files, b.err
}

// publish from_bucket publishes the objects under a prefix (#2593).
func TestPublish_FromABucketPrefix(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	src := &bucketSources{memSources: f.sources}
	f.sources.files = []sitesvc.InputFile{{Path: "index.html", Body: []byte("<h1>from the bucket</h1>")}}
	f.tool.Sources = src
	from := &bucketsource.Source{Connection: "assets", Bucket: "web-site", Prefix: "src/"}

	out := call(t, f, admin(), Input{Command: CmdPublish, Site: siteName, FromBucket: from})
	rel, _ := out["release"].(map[string]any)
	assert.Equal(t, float64(1), rel["number"])
	assert.Equal(t, "s3://web-site/src/ on connection assets", rel["source"], "the release says where it came from")
	require.Len(t, src.asked, 1)
	assert.Equal(t, *from, src.asked[0])

	assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdPublish, Site: siteName, FromBucket: from, Archive: "mcp:resource:x"}),
		"names one source")
	src.err = &sitesvc.RefusedError{Msg: "nothing was published: connection \"assets\" is not one your persona may use"}
	assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdPublish, Site: siteName, FromBucket: from}), "not one your persona may use")

	f.tool.Sources = f.sources
	assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdPublish, Site: siteName, FromBucket: from}), "cannot publish from a bucket")
}

// A source the platform reads is read only for a caller who may publish to the
// site: someone who does not manage it is refused, and nothing is read for
// them, from a bucket, an archive or a folder.
func TestPublish_NothingIsReadForACallerWhoMayNotPublish(t *testing.T) {
	f := newFixture()
	createSite(t, f)
	src := &bucketSources{memSources: f.sources}
	f.sources.files = []sitesvc.InputFile{{Path: "index.html", Body: []byte("<h1>secret</h1>")}}
	f.tool.Sources = src
	stranger := ctxAs("stranger@example.com", false, "")
	for name, in := range map[string]Input{
		"bucket":  {FromBucket: &bucketsource.Source{Connection: "assets", Bucket: "web-site", Prefix: "src/"}},
		"archive": {Archive: "mcp:resource:site.zip"},
		"folder":  {Folder: "web/build", Scope: "global"},
	} {
		in.Command, in.Site = CmdPublish, siteName
		assert.Contains(t, refusal(t, f, stranger, in), "is changed by its owner", name)
	}
	assert.Empty(t, src.asked, "no bucket was read")
	assert.Empty(t, f.sources.archive, "no archive was read")
	assert.Zero(t, f.sources.folderCalls, "no folder was read")
	assert.Empty(t, f.store.releases, "nothing was published")

	assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdPublish, Site: siteName, Environment: "prod", Archive: "mcp:resource:site.zip"}),
		"dev", "an environment people use is refused before the archive is read")
	assert.Empty(t, f.sources.archive)
}
