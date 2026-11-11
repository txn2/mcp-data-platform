package sitewire

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	s3client "github.com/txn2/mcp-s3/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/bucketsource"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// bucketClient is one bucket of a site's source tree; grown is what an
// object holds by the time it is read, when someone replaced it after the
// listing.
type bucketClient struct {
	objects map[string]string
	grown   map[string]string
	getErr  error
}

func (b bucketClient) ListObjects(_ context.Context, _, _, _ string, _ int32, _ string) (*s3client.ListObjectsOutput, error) { //nolint:revive // argument-limit: the upstream s3client interface fixes the arity
	out := &s3client.ListObjectsOutput{}
	for _, k := range []string{"src/", "src/index.html", "src/_layouts/base.html", "src/css/site.css"} {
		if body, ok := b.objects[k]; ok || k == "src/" {
			out.Objects = append(out.Objects, s3client.ObjectInfo{Key: k, Size: int64(len(body))})
		}
	}
	return out, nil
}

func (b bucketClient) GetObjectRange(_ context.Context, _, key string, offset, length int64) (*s3client.ObjectContent, error) { //nolint:revive // argument-limit: the upstream s3client interface fixes the arity
	if b.getErr != nil {
		return nil, b.getErr
	}
	body, ok := b.grown[key]
	if !ok {
		body = b.objects[key]
	}
	end := min(offset+length, int64(len(body)))
	return &s3client.ObjectContent{Key: key, Body: []byte(body[offset:end])}, nil
}

type bucketClients struct {
	client bucketClient
	err    error
}

func (c bucketClients) ReadClient(string, string) (bucketsource.Client, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.client, nil
}

func bucketSources(cfg siteconfig.Config, clients bucketClients) sources {
	return sources{live: siteconfig.NewLive(cfg), buckets: &bucketsource.Access{Clients: clients}}
}

var siteTree = map[string]string{"src/index.html": "<h1>home</h1>", "src/_layouts/base.html": "{{ content }}", "src/css/site.css": "body{}"}

func TestBucket_ASourceTreeIsTheReleasesFiles(t *testing.T) {
	s := bucketSources(siteconfig.Config{}, bucketClients{client: bucketClient{objects: siteTree}})
	src := bucketsource.Source{Connection: "assets", Bucket: "web-site", Prefix: "src"}
	require.NoError(t, s.CheckBucket(context.Background(), src))
	files, err := s.Bucket(context.Background(), src)
	require.NoError(t, err)
	got := map[string]string{}
	for _, f := range files {
		got[f.Path] = string(f.Body)
	}
	assert.Equal(t, map[string]string{"index.html": "<h1>home</h1>", "_layouts/base.html": "{{ content }}", "css/site.css": "body{}"}, got)
}

func TestBucket_TheReleaseLimitsApplyBeforeAnythingIsRead(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  siteconfig.Config
		want string
	}{
		"too many files": {siteconfig.Config{MaxFiles: 2}, "sites.max_release_files"},
		"a file too big": {siteconfig.Config{MaxFileBytes: 10}, "sites.max_file_bytes"},
		"too much":       {siteconfig.Config{MaxReleaseBytes: 20}, "sites.max_release_bytes"},
	} {
		t.Run(name, func(t *testing.T) {
			s := bucketSources(tc.cfg, bucketClients{client: bucketClient{objects: siteTree, getErr: errors.New("must not be read")}})
			_, err := s.Bucket(context.Background(), bucketsource.Source{Connection: "assets", Bucket: "web-site", Prefix: "src"})
			var refused *sitesvc.RefusedError
			require.ErrorAs(t, err, &refused)
			assert.Contains(t, refused.Msg, tc.want)
			assert.Contains(t, refused.Msg, "nothing was published")
		})
	}
}

// Objects replaced by larger ones after the listing are held to the release's
// limits as they are read: one past sites.max_file_bytes, or all of them past
// sites.max_release_bytes, refuse the release.
func TestBucket_TheReleaseLimitsHoldAsObjectsAreRead(t *testing.T) {
	src := bucketsource.Source{Connection: "assets", Bucket: "web-site", Prefix: "src"}
	for name, tc := range map[string]struct {
		cfg  siteconfig.Config
		want string
	}{
		"a file grew past its limit":  {siteconfig.Config{MaxFileBytes: 20}, "sites.max_file_bytes"},
		"the files grew past the sum": {siteconfig.Config{MaxReleaseBytes: 40}, "sites.max_release_bytes"},
	} {
		t.Run(name, func(t *testing.T) {
			grown := map[string]string{"src/index.html": strings.Repeat("x", 30)}
			s := bucketSources(tc.cfg, bucketClients{client: bucketClient{objects: siteTree, grown: grown}})
			_, err := s.Bucket(context.Background(), src)
			var refused *sitesvc.RefusedError
			require.ErrorAs(t, err, &refused)
			assert.Contains(t, refused.Msg, tc.want)
			assert.Contains(t, refused.Msg, "grew after the prefix was listed")
		})
	}
}

func TestBucket_Refusals(t *testing.T) {
	ctx := context.Background()
	src := bucketsource.Source{Connection: "assets", Bucket: "finance"}
	_, err := sources{live: siteconfig.NewLive(siteconfig.Config{})}.Bucket(ctx, src)
	require.ErrorContains(t, err, "no S3 connections")

	s := bucketSources(siteconfig.Config{}, bucketClients{err: errors.New(`connection "assets" is limited to buckets whose name starts with "web-"`)})
	err = s.CheckBucket(ctx, src)
	var refused *sitesvc.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Msg, "limited to buckets")

	s = bucketSources(siteconfig.Config{}, bucketClients{client: bucketClient{objects: siteTree, getErr: errors.New("reset")}})
	_, err = s.Bucket(ctx, bucketsource.Source{Connection: "assets", Bucket: "web-site", Prefix: "src"})
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Msg, "the object store answered")
}

func TestRefusal_LeavesOtherFailuresAsTheyAre(t *testing.T) {
	plain := errors.New("down")
	assert.Equal(t, plain, refusal(plain))
}
