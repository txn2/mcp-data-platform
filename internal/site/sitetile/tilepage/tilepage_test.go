package tilepage

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

func files() map[string]site.File {
	return map[string]site.File{
		"index.html":      {Path: "index.html", ContentType: "text/html"},
		"css/site.css":    {Path: "css/site.css", ContentType: "text/css"},
		"docs/index.html": {Path: "docs/index.html", ContentType: "text/html"},
		"broken.png":      {Path: "broken.png", ContentType: "image/png"},
	}
}

func read(_ context.Context, f site.File) ([]byte, error) {
	if f.Path == "broken.png" {
		return nil, errors.New("gone")
	}
	return []byte("<" + f.Path + ">"), nil
}

// TestPage: the release's root page is the document, its files answer its own
// origin, and every other origin is answered not found without a fetch.
func TestPage(t *testing.T) {
	p, err := pageOf(context.Background(), files(), read)
	require.NoError(t, err)
	assert.Equal(t, "<index.html>", string(p.Document))
	f, ok := p.Files("/css/site.css?v=3", nil)
	require.True(t, ok)
	assert.Equal(t, "text/css", f.ContentType)
	f, ok = p.Files("/docs/", nil)
	require.True(t, ok)
	assert.Equal(t, "<docs/index.html>", string(f.Body))
	_, ok = p.Files("/missing.js", nil)
	assert.False(t, ok)
	f, ok = p.Files("/broken.png", nil)
	require.True(t, ok)
	assert.Equal(t, http.StatusServiceUnavailable, f.Status)

	other, _ := url.Parse("https://www.example.com/logo.png")
	f, ok = p.Substitute(other, false)
	require.True(t, ok, "another origin, a custom domain included, is never fetched")
	assert.Equal(t, http.StatusNotFound, f.Status)
	_, ok = p.Substitute(other, true)
	assert.False(t, ok, "the page's own origin is its files'")

	_, err = pageOf(context.Background(), map[string]site.File{"a.css": {Path: "a.css"}}, read)
	require.ErrorIs(t, err, ErrNoDocument)
	_, err = pageOf(context.Background(), map[string]site.File{"index.html": {Path: "index.html"}},
		func(context.Context, site.File) ([]byte, error) { return nil, errors.New("gone") })
	require.Error(t, err)
}

func TestFilePath(t *testing.T) {
	for in, want := range map[string]string{
		"/": "index.html", "": "index.html", "/a/b/": "a/b/index.html", "/a/b": "a/b/index.html",
		"/a/b.css": "a/b.css", "/../../etc/x.html": "etc/x.html", "/x.js#frag": "x.js",
	} {
		assert.Equal(t, want, filePath(in), in)
	}
}

// releases is one release of one file.
type releases struct{ err error }

func (r releases) Release(_ context.Context, _ string, n int) (*site.Release, error) {
	if r.err != nil {
		return nil, r.err
	}
	return &site.Release{ID: "rel", Number: n}, nil
}

func (releases) Files(context.Context, string) ([]site.File, error) {
	return []site.File{{Path: "index.html", SHA256: "abc", ContentType: "text/html"}}, nil
}

type objects map[string][]byte

func (o objects) GetObject(_ context.Context, _, key string) (data []byte, contentType string, err error) {
	if b, ok := o[key]; ok {
		return b, "", nil
	}
	return nil, "", errors.New("no object")
}

// TestReleasePage: a release's page is read from where the site keeps its
// files; a deployment with nowhere to read them, a missing release and a
// missing root page refuse.
func TestReleasePage(t *testing.T) {
	_, err := Release{}.Page(context.Background(), "s1", 1)
	require.Error(t, err)
	objs := objects{}
	r := Release{Store: releases{}, Objects: objs, Bucket: "portal"}
	_, err = r.Page(context.Background(), "s1", 1)
	require.Error(t, err, "the root page's object is missing")
	objs[sitesvc.ObjectKey("s1", site.File{Path: "index.html", SHA256: "abc", ContentType: "text/html"}, "")] = []byte("<h1>home</h1>")
	p, err := r.Page(context.Background(), "s1", 1)
	require.NoError(t, err)
	assert.Equal(t, "<h1>home</h1>", string(p.Document))
	_, err = Release{Store: releases{err: errors.New("gone")}, Objects: objs, Bucket: "portal"}.Page(context.Background(), "s1", 1)
	require.Error(t, err)
}
