package thumbworker

import (
	"bytes"
	"context"
	"encoding/json"
	"html"
	"net/http"
	"path"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/headless"
	"github.com/txn2/mcp-data-platform/internal/portal/viewerlimit"
	"github.com/txn2/mcp-data-platform/internal/thumbtypes"
)

const (
	// A tile is laid out at 400x300 CSS pixels, the size a card shows it at,
	// and stored at tileScale times that: an 800x600 image, so a card on a
	// high-density display is not stretching a picture half its size (#1789).
	tileWidth  = 400
	tileHeight = 300
	tileScale  = 2.0
	// A document that lays itself out at page size -- HTML, JSX -- is drawn at
	// 1280x960, which is how it looks in the viewer's frame, and reduced to the
	// same stored size.
	pageWidth  = 1280
	pageHeight = 960
	pageScale  = tileScale * tileWidth / pageWidth

	// contentPath is where the tile page loads a binary document's own bytes.
	contentPath = "/content"
)

// tileReady is the expression the renderer waits on. The tile entry sets
// window.__tileReady to a promise of "" once the tile is drawn, or of the
// reason it cannot be. The entry is a module that may not have run when the
// renderer first asks, so this waits for the promise to exist; a module that
// fails to load sets it itself, from the script's onerror.
const tileReady = `new Promise(function(resolve){
  var waited = 0;
  (function poll(){
    if (window.__tileReady) {
      window.__tileReady.then(function(v){ resolve(v || ""); }, function(e){ resolve(String((e && e.message) || e)); });
      return;
    }
    waited += 50;
    if (waited > 30000) { resolve("the tile renderer did not start"); return; }
    setTimeout(poll, 50);
  })();
})`

// inProcessPrefixes are the platform routes a tile page loads from: a
// document's declared references, the served slide runtime and the SPA's own
// vendored files, and the viewer bundle's chunks. Each answers without a
// session -- a reference is authorized by the token in its path -- so calling
// them in-process gives the page exactly what a reader's browser gets, and
// nothing else on the platform is reachable.
var inProcessPrefixes = []string{"/portal/refs/", "/portal/vendor/", "/portal/view/_assets/"}

// tileSource is one document to draw one variant of.
type tileSource struct {
	contentType string
	// content is the document's bytes; a text document's references are
	// already rewritten to the platform's reference route.
	content []byte
	name    string
	dark    bool
}

// isBinary reports whether a document reaches the tile page by URL rather than
// inline: a raster image cannot travel as a JSON string.
func isBinary(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.HasPrefix(ct, "image/") && !strings.Contains(ct, "svg")
}

// tilePage is the page a tile is drawn from: the viewer bundle's tile entry
// over one document, at the geometry its family is drawn at.
func (w *Worker) tilePage(src tileSource) headless.Page {
	binary := isBinary(src.contentType)
	data := map[string]any{
		"contentType":  src.contentType,
		"name":         src.name,
		"contentURL":   contentPath,
		"serveFromURL": binary,
	}
	extra := map[string]headless.File{}
	if binary {
		extra[contentPath] = headless.File{Body: src.content, ContentType: src.contentType}
	} else {
		data["content"] = string(src.content)
	}
	width, height, scale := tileWidth, tileHeight, tileScale
	if thumbtypes.DrawnAsDocument(src.contentType) {
		width, height, scale = pageWidth, pageHeight, pageScale
	}
	return headless.Page{
		Document: tileDocument(data, src.dark, w.deps.TileEntryURL, w.deps.TileCSS),
		Files:    w.files(extra),
		Ready:    tileReady,
		Width:    width,
		Height:   height,
		Scale:    scale,
		Dark:     src.dark,
	}
}

// tileDocument is the tile page's HTML. The payload is JSON inside a script
// element; encoding/json escapes <, > and &, so no document content can close
// the element early.
func tileDocument(data map[string]any, dark bool, entryURL, css string) []byte {
	payload, err := json.Marshal(data)
	if err != nil {
		payload = []byte("{}")
	}
	theme := ` data-theme="light"`
	if dark {
		theme = ` class="dark" data-theme="dark"`
	}
	return []byte(strings.Join([]string{
		`<!DOCTYPE html><html lang="en"`, theme, `><head><meta charset="utf-8">`,
		`<style>`, strings.ReplaceAll(css, "</style", `<\/style`), `</style>`,
		`<style>html,body{margin:0;overflow:hidden}</style></head><body>`,
		`<script type="application/json" id="tile-data">`, string(payload), `</script><div id="tile-root"></div>`,
		`<script type="module" src="`, html.EscapeString(entryURL),
		`" onerror="window.__tileReady=Promise.resolve('the tile renderer did not load')"></script>`,
		`</body></html>`,
	}, ""))
}

// files answers a tile page's requests for its own origin: the bytes the page
// was handed, then the platform's public routes under inProcessPrefixes.
func (w *Worker) files(extra map[string]headless.File) func(string) (headless.File, bool) {
	return func(p string) (headless.File, bool) {
		if f, ok := extra[p]; ok {
			return f, true
		}
		clean := path.Clean(p)
		if w.deps.Routes == nil || !servedInProcess(clean) {
			return headless.File{}, false
		}
		return serveInProcess(w.deps.Routes, clean)
	}
}

func servedInProcess(p string) bool {
	for _, prefix := range inProcessPrefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	return false
}

// serveInProcess calls routes for one GET and returns the body when it answers
// 200. It carries no credentials, so only a route that answers anonymously can
// answer it.
//
// It is marked as the platform's own request, which the public viewer's rate
// limiter admits without counting (#1791). Counted, every request the worker
// makes presents the one loopback address and shares one bucket, and the
// reference route in front of a document's files ran it dry partway through a
// document: its light tile loaded every file and its dark tile loaded none.
func serveInProcess(routes http.Handler, p string) (headless.File, bool) {
	ctx, cancel := context.WithTimeout(viewerlimit.InProcess(context.Background()), storageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p, http.NoBody)
	if err != nil {
		return headless.File{}, false
	}
	req.RemoteAddr = "127.0.0.1:0"
	rec := &recorder{header: http.Header{}}
	routes.ServeHTTP(rec, req)
	if rec.code() != http.StatusOK {
		return headless.File{}, false
	}
	return headless.File{Body: rec.body.Bytes(), ContentType: rec.header.Get("Content-Type")}, true
}

// recorder is the in-process response a route writes into.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

// Header is the response header the route sets.
func (r *recorder) Header() http.Header { return r.header }

// WriteHeader records the first status the route writes.
func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

// Write collects the body, implying 200 when no status was written.
func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(p) //nolint:wrapcheck // bytes.Buffer never fails
}

func (r *recorder) code() int {
	if r.status == 0 {
		return http.StatusOK
	}
	return r.status
}
