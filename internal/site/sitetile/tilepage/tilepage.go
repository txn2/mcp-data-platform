// Package tilepage builds the page a site environment's tile is drawn from
// (#2658): the release's root page and every file of the release beside it,
// read in process from where the site keeps them. Nothing is fetched from any
// hostname: a request for another origin, a custom domain among them, is
// answered not found. No address of the environment is opened to anyone, so
// drawing a private environment opens nothing.
package tilepage

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// The page a tile is drawn at: a desktop page, reduced to a tile.
const (
	pageWidth  = 1280
	pageHeight = 960
	pageScale  = 0.625
)

// ready waits for what is in the picture and nothing else (#2677): the fonts,
// then every image whose box intersects the 1280x960 frame the tile is
// captured from. An image outside the frame cannot change a pixel of the tile,
// and a loading="lazy" one there is never fetched, so its decode() never
// settles; waiting on it held every such page to the render deadline. An
// image inside the frame is fetched by the browser on its own, lazy or not,
// so nothing about the page is rewritten.
const ready = `(document.fonts ? document.fonts.ready : Promise.resolve()).then(function(){
  return Promise.all(Array.prototype.filter.call(document.images, function(i){
    var r = i.getBoundingClientRect();
    return r.width > 0 && r.height > 0 && r.bottom > 0 && r.right > 0 && r.top < innerHeight && r.left < innerWidth;
  }).map(function(i){ return i.decode().catch(function(){}); }));
}).then(function(){ return ""; })`

// slash separates a path's segments.
const slash = "/"

// indexFile is the file a folder's address serves.
const indexFile = "index.html"

// ErrNoDocument is a release with no page at its root to draw.
var ErrNoDocument = errors.New("the release has no index.html at its root to draw")

// Objects reads a site's stored objects.
type Objects interface {
	GetObject(ctx context.Context, bucket, key string) ([]byte, string, error)
}

// Releases reads a release and its files.
type Releases interface {
	Release(ctx context.Context, siteID string, number int) (*site.Release, error)
	Files(ctx context.Context, releaseID string) ([]site.File, error)
}

// Release is where a site's releases and their files are read from.
type Release struct {
	Store   Releases
	Objects Objects
	Bucket  string
}

// Page is the page release number of the site is drawn from.
func (r Release) Page(ctx context.Context, siteID string, number int) (headless.Page, error) {
	if r.Store == nil || r.Objects == nil || r.Bucket == "" {
		return headless.Page{}, errors.New("this deployment keeps no site files")
	}
	rel, err := r.Store.Release(ctx, siteID, number)
	if err != nil {
		return headless.Page{}, fmt.Errorf("reading release %d: %w", number, err)
	}
	files, err := r.Store.Files(ctx, rel.ID)
	if err != nil {
		return headless.Page{}, fmt.Errorf("listing release %d's files: %w", number, err)
	}
	byPath := make(map[string]site.File, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}
	return pageOf(ctx, byPath, func(ctx context.Context, f site.File) ([]byte, error) {
		data, _, err := r.Objects.GetObject(ctx, r.Bucket, sitesvc.ObjectKey(siteID, f, ""))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f.Path, err)
		}
		return data, nil
	})
}

// pageOf is the page an environment's release is drawn from: its root page,
// every file of the release beside it, and nothing from anywhere else.
func pageOf(ctx context.Context, files map[string]site.File, read func(ctx context.Context, f site.File) ([]byte, error)) (headless.Page, error) {
	root, ok := files[indexFile]
	if !ok {
		return headless.Page{}, ErrNoDocument
	}
	doc, err := read(ctx, root)
	if err != nil {
		return headless.Page{}, fmt.Errorf("reading %s: %w", indexFile, err)
	}
	return headless.Page{
		Document: doc,
		Files: func(p string, _ http.Header) (headless.File, bool) {
			f, ok := files[filePath(p)]
			if !ok {
				return headless.File{}, false
			}
			body, err := read(ctx, f)
			if err != nil {
				return headless.File{Status: http.StatusServiceUnavailable}, true
			}
			return headless.File{Body: body, ContentType: f.ContentType}, true
		},
		// Every other origin, the site's own domains included, is not
		// fetched: the tile is the release, drawn the same whatever domains
		// the environment has.
		Substitute: func(_ *url.URL, own bool) (headless.File, bool) {
			if own {
				return headless.File{}, false
			}
			return headless.File{Status: http.StatusNotFound}, true
		},
		Ready: ready, Width: pageWidth, Height: pageHeight, Scale: pageScale,
	}, nil
}

// filePath is the release path a request path names: a folder's index.html,
// the path itself otherwise.
func filePath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimPrefix(path.Clean(slash+p), slash)
	if p == "" || p == "." {
		return indexFile
	}
	if strings.HasSuffix(p, slash) || !strings.Contains(path.Base(p), ".") {
		return strings.TrimSuffix(p, slash) + slash + indexFile
	}
	return p
}
