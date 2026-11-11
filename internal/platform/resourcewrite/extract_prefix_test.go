package resourcewrite_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	s3client "github.com/txn2/mcp-s3/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/bucketsource"
	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/internal/unarchive"
)

// mediaBucket is a bucket of media under a prefix; grown is what an object
// holds by the time it is read, when it was replaced after the listing.
type mediaBucket struct {
	objects map[string]string
	grown   map[string]string
	order   []string
	getErr  error
	reads   int
}

func (b *mediaBucket) ListObjects(_ context.Context, _, _, _ string, _ int32, _ string) (*s3client.ListObjectsOutput, error) { //nolint:revive // argument-limit: the upstream s3client interface fixes the arity
	out := &s3client.ListObjectsOutput{}
	for _, k := range b.order {
		out.Objects = append(out.Objects, s3client.ObjectInfo{Key: k, Size: int64(len(b.objects[k]))})
	}
	return out, nil
}

func (b *mediaBucket) GetObjectRange(_ context.Context, _, key string, offset, length int64) (*s3client.ObjectContent, error) { //nolint:revive // argument-limit: the upstream s3client interface fixes the arity
	b.reads++
	if b.getErr != nil {
		return nil, b.getErr
	}
	ct := ""
	if strings.HasSuffix(key, ".svg") {
		ct = "image/svg+xml"
	}
	body, ok := b.grown[key]
	if !ok {
		body = b.objects[key]
	}
	end := min(offset+length, int64(len(body)))
	return &s3client.ObjectContent{Key: key, Body: []byte(body[offset:end]), ContentType: ct}, nil
}

type mediaClients struct{ b *mediaBucket }

func (m mediaClients) ReadClient(string, string) (bucketsource.Client, error) { return m.b, nil }

func newPrefixFixture(t *testing.T, lim unarchive.Limits, b *mediaBucket) *extractFixture {
	t.Helper()
	xf := newExtractFixture(t, lim, 0)
	x := resourcewrite.NewExtractor(resourcewrite.ExtractorDeps{
		Lander: xf.lander, Ranges: xf.blobs, Limits: lim, Buckets: &bucketsource.Access{Clients: mediaClients{b}},
	})
	require.NotNil(t, x)
	xf.extractor = x
	return xf
}

func media() *mediaBucket {
	return &mediaBucket{
		objects: map[string]string{"drop/": "", "drop/logo.svg": `<svg xmlns="http://www.w3.org/2000/svg"/>`, "drop/2026/q1/chart.csv": "a,b\n1,2\n"},
		order:   []string{"drop/", "drop/logo.svg", "drop/2026/q1/chart.csv"},
	}
}

func prefixReq() resourcewrite.PrefixExtraction {
	return resourcewrite.PrefixExtraction{
		Source: bucketsource.Source{Connection: "assets", Bucket: "web-media", Prefix: "drop"},
		Path:   "sites/media",
	}
}

func TestExtractPrefixWritesEachObjectUnderItsFolders(t *testing.T) {
	b := media()
	xf := newPrefixFixture(t, unarchive.Limits{}, b)
	out, err := xf.extractor.ExtractPrefix(context.Background(), prefixReq(), analyst())
	require.NoError(t, err)
	assert.Equal(t, "bucket prefix", out.Format)
	require.Len(t, out.Members, 2, "the folder marker is passed over")
	assert.Equal(t, "logo.svg", out.Members[0].Member)
	assert.Equal(t, "sites/media", out.Members[0].Landing.Path)
	assert.Equal(t, "image/svg+xml", out.Members[0].Landing.ContentType, "the type the object was stored with is its declaration")
	assert.Equal(t, "sites/media/2026/q1", out.Members[1].Landing.Path, "an object's own folders become folders beneath the target")
	assert.Equal(t, "a,b\n1,2\n", xf.contentAt(t, out.Members[1].Landing.URI))
	assert.Equal(t, 2, b.reads, "each object is read once")
}

func TestExtractPrefixRefusesBeforeWritingAnything(t *testing.T) {
	ctx := context.Background()
	t.Run("past a limit", func(t *testing.T) {
		b := media()
		xf := newPrefixFixture(t, unarchive.Limits{MaxMembers: 1}, b)
		_, err := xf.extractor.ExtractPrefix(ctx, prefixReq(), analyst())
		require.ErrorIs(t, err, resourcewrite.ErrExtractRefused)
		assert.Contains(t, err.Error(), "resources.managed.extract.max_members")
		assert.Zero(t, b.reads, "nothing is read past a limit")
	})
	t.Run("an empty prefix", func(t *testing.T) {
		xf := newPrefixFixture(t, unarchive.Limits{}, &mediaBucket{objects: map[string]string{}})
		_, err := xf.extractor.ExtractPrefix(ctx, prefixReq(), analyst())
		require.ErrorIs(t, err, resourcewrite.ErrExtractRefused)
		assert.Contains(t, err.Error(), "no objects under")
	})
	t.Run("an address already taken", func(t *testing.T) {
		xf := newPrefixFixture(t, unarchive.Limits{}, media())
		_, err := xf.extractor.ExtractPrefix(ctx, prefixReq(), analyst())
		require.NoError(t, err)
		_, err = xf.extractor.ExtractPrefix(ctx, prefixReq(), analyst())
		require.ErrorIs(t, err, resourcewrite.ErrExtractRefused, "without replace, a second extraction writes nothing")
		req := prefixReq()
		req.Replace = true
		out, err := xf.extractor.ExtractPrefix(ctx, req, analyst())
		require.NoError(t, err)
		assert.False(t, out.Members[0].Landing.Created, "replace records the next version")
	})
	t.Run("no connection named", func(t *testing.T) {
		xf := newPrefixFixture(t, unarchive.Limits{}, media())
		req := prefixReq()
		req.Source.Connection = ""
		_, err := xf.extractor.ExtractPrefix(ctx, req, analyst())
		require.ErrorIs(t, err, resourcewrite.ErrExtractRefused)
	})
	t.Run("a deployment that reads no bucket", func(t *testing.T) {
		xf := newExtractFixture(t, unarchive.Limits{}, 0)
		_, err := xf.extractor.ExtractPrefix(ctx, prefixReq(), analyst())
		require.ErrorContains(t, err, "cannot read a bucket")
	})
	t.Run("an object that grew after the listing", func(t *testing.T) {
		b := media()
		b.grown = map[string]string{"drop/logo.svg": strings.Repeat("x", 4096)}
		xf := newPrefixFixture(t, unarchive.Limits{MaxMemberBytes: 100}, b)
		out, err := xf.extractor.ExtractPrefix(ctx, prefixReq(), analyst())
		require.ErrorIs(t, err, resourcewrite.ErrExtractRefused)
		assert.Contains(t, err.Error(), "resources.managed.extract.max_member_bytes")
		assert.Empty(t, out.Members, "the grown object is not written")
	})
	t.Run("objects that grew past the total", func(t *testing.T) {
		b := media()
		b.grown = map[string]string{"drop/2026/q1/chart.csv": strings.Repeat("y", 60)}
		xf := newPrefixFixture(t, unarchive.Limits{MaxTotalBytes: 80}, b)
		out, err := xf.extractor.ExtractPrefix(ctx, prefixReq(), analyst())
		require.ErrorIs(t, err, resourcewrite.ErrExtractRefused)
		assert.Contains(t, err.Error(), "resources.managed.extract.max_total_bytes")
		assert.Len(t, out.Members, 1, "what was written before the total was passed is reported")
	})
	t.Run("a read that fails part way", func(t *testing.T) {
		b := media()
		b.getErr = errors.New("reset")
		xf := newPrefixFixture(t, unarchive.Limits{}, b)
		out, err := xf.extractor.ExtractPrefix(ctx, prefixReq(), analyst())
		require.ErrorContains(t, err, "the object store answered")
		assert.Empty(t, out.Members)
	})
}
