// Package sitecrawl copies an existing site into the files of a release
// (#2518), so a site moved off a CMS keeps its URLs: every page is filed at
// the path it answered at, an address that redirected becomes a rule of the
// release's redirect map, and the sitemap and robots file come along.
//
// Everything fetched is untrusted and everything fetched from is chosen by a
// caller, so every request goes through the egress guard, every redirect is
// recorded rather than followed by the client, and a run is bounded by pages
// and bytes. A form named in the form map is replaced by a frame of the
// platform form it becomes, built from a checked endpoint name and never from
// anything on the page.
package sitecrawl

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/txn2/mcp-data-platform/internal/egressguard"
	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

const (
	fetchTimeout = 30 * time.Second
	indexFile    = "index.html"
	redirectsMax = 5000
	attrSrc      = "src"
	pathSep      = "/"
)

var endpointName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// wellKnownFiles are fetched whether or not a page links them.
var wellKnownFiles = []string{"/sitemap.xml", "/robots.txt", "/favicon.ico"}

// Request is one crawl.
type Request struct {
	Start string
	// FormMap names, for a page path, the platform form endpoint that replaces
	// the page's <form> elements.
	FormMap map[string]string
	// PortalURL is the platform's address the frames point at.
	PortalURL string
}

// Result is what a crawl found.
type Result struct {
	Files     []sitesvc.InputFile
	Pages     int
	Redirects int
	Skipped   []string
}

// Crawler fetches through the egress guard.
type Crawler struct {
	Client   *http.Client
	MaxPages int
	MaxBytes int64
}

// New builds a crawler from the configuration.
func New(cfg siteconfig.CrawlConfig) (*Crawler, error) {
	guard, err := egressguard.New(cfg.AllowPrivateCIDRs)
	if err != nil {
		return nil, fmt.Errorf("building the egress guard: %w", err)
	}
	limits := siteconfig.Config{Crawl: cfg}.Limits().Crawl
	return &Crawler{
		Client:   outbound.NewClient(outbound.Options{Kind: outbound.KindSite, Base: guard.Transport(), Timeout: fetchTimeout}),
		MaxPages: limits.MaxPages, MaxBytes: limits.MaxBytes,
	}, nil
}

// run is one crawl's state.
type run struct {
	c         *Crawler
	req       Request
	root      *url.URL
	seen      map[string]bool
	queue     []string
	files     []sitesvc.InputFile
	redirects []site.Redirect
	skipped   []string
	pages     int
	total     int64
}

// Crawl walks the site from req.Start, staying on its host.
func (c *Crawler) Crawl(ctx context.Context, req Request) (*Result, error) {
	root, err := checkRequest(req)
	if err != nil {
		return nil, err
	}
	r := &run{c: c, req: req, root: root, seen: map[string]bool{}}
	r.enqueue(root.Path)
	for _, p := range wellKnownFiles {
		r.enqueue(p)
	}
	for len(r.queue) > 0 && r.pages < c.MaxPages {
		next := r.queue[0]
		r.queue = r.queue[1:]
		if err := r.fetch(ctx, next); err != nil {
			return nil, err
		}
	}
	return r.result(), nil
}

// checkRequest refuses a start that is not an http(s) address and a form map
// entry that is not a page path and an endpoint name.
func checkRequest(req Request) (*url.URL, error) {
	root, err := url.Parse(req.Start)
	if err != nil || (root.Scheme != "http" && root.Scheme != "https") || root.Host == "" {
		return nil, &sitesvc.RefusedError{Msg: fmt.Sprintf("url %q: an http(s) address", req.Start)}
	}
	for page, ep := range req.FormMap {
		if !endpointName.MatchString(ep) || !strings.HasPrefix(page, pathSep) {
			return nil, &sitesvc.RefusedError{Msg: fmt.Sprintf("form_map %q: %q is not a page path and form endpoint name", page, ep)}
		}
	}
	return root, nil
}

func (r *run) result() *Result {
	files := r.files
	if len(r.redirects) > 0 {
		var b strings.Builder
		for _, rule := range r.redirects {
			fmt.Fprintf(&b, "%s %s %d\n", rule.From, rule.To, rule.Status)
		}
		files = append(files, sitesvc.InputFile{Path: "_redirects", Body: []byte(b.String())})
	}
	return &Result{Files: files, Pages: r.pages, Redirects: len(r.redirects), Skipped: r.skipped}
}

func (r *run) enqueue(p string) {
	if p == "" {
		p = pathSep
	}
	if !r.seen[p] {
		r.seen[p] = true
		r.queue = append(r.queue, p)
	}
}

func (r *run) fetch(ctx context.Context, p string) error {
	target := r.root.ResolveReference(&url.URL{Path: p})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), http.NoBody) // #nosec G107 -- through the egress guard
	if err != nil {
		r.skipped = append(r.skipped, p)
		return nil //nolint:nilerr // a path that cannot be requested is reported, not fatal
	}
	res, err := r.c.Client.Do(req) // #nosec G704 -- the client's transport is the egress guard's
	if err != nil {
		r.skipped = append(r.skipped, p)
		return nil //nolint:nilerr // an address that does not answer is reported, not fatal
	}
	defer func() { _ = res.Body.Close() }()
	switch {
	case res.StatusCode >= http.StatusMultipleChoices && res.StatusCode < http.StatusBadRequest:
		r.redirected(p, res)
		return nil
	case res.StatusCode != http.StatusOK:
		r.skipped = append(r.skipped, p)
		return nil
	}
	return r.store(p, res)
}

// redirected records a rule for an address that redirected, and walks where
// it went when that stays on the site.
func (r *run) redirected(p string, res *http.Response) {
	loc, err := res.Location()
	if err != nil || len(r.redirects) >= redirectsMax {
		r.skipped = append(r.skipped, p)
		return
	}
	target := loc.String()
	if loc.Host == r.root.Host {
		target = cmp.Or(loc.Path, pathSep)
		r.enqueue(target)
	}
	rule := site.Redirect{From: p, To: target, Status: res.StatusCode}
	if site.ValidateRedirect(rule) != nil {
		r.skipped = append(r.skipped, p)
		return
	}
	r.redirects = append(r.redirects, rule)
}

func (r *run) store(p string, res *http.Response) error {
	remaining := r.c.MaxBytes - r.total
	body, err := io.ReadAll(io.LimitReader(res.Body, remaining+1))
	if err != nil {
		r.skipped = append(r.skipped, p)
		return nil //nolint:nilerr // a body that breaks off is reported, not fatal
	}
	if int64(len(body)) > remaining {
		return &sitesvc.RefusedError{Msg: fmt.Sprintf("the site holds more than the %d bytes sites.crawl.max_bytes allows", r.c.MaxBytes)}
	}
	r.total += int64(len(body))
	r.pages++
	isHTML := strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "text/html")
	name := filePath(p, isHTML)
	// A path a release cannot hold is reported, not fatal: one .htaccess link
	// on a page must not stop the whole copy.
	if _, err := sitesvc.CleanPath(name); err != nil {
		r.skipped = append(r.skipped, p)
		return nil //nolint:nilerr // a path a release cannot hold is reported, not fatal
	}
	if isHTML {
		body = r.page(p, body)
	}
	r.files = append(r.files, sitesvc.InputFile{Path: name, Body: body})
	return nil
}

// filePath is the file a URL path is stored as, so that it answers at the
// same address: a directory's index for a path ending in a slash, and
// name.html for a page with no extension.
func filePath(p string, isHTML bool) string {
	name := strings.TrimPrefix(p, pathSep)
	switch {
	case name == "":
		return indexFile
	case strings.HasSuffix(name, pathSep):
		return name + indexFile
	case isHTML && path.Ext(name) == "":
		return name + ".html"
	}
	return name
}

// linkAttrs are the attributes that name another resource.
var linkAttrs = map[atom.Atom][]string{
	atom.A: {"href"}, atom.Link: {"href"}, atom.Script: {attrSrc}, atom.Img: {attrSrc}, atom.Source: {attrSrc},
	atom.Video: {attrSrc, "poster"}, atom.Audio: {attrSrc}, atom.Iframe: {attrSrc},
}

// page reads a page's links and, for a page in the form map, replaces its
// forms. A page that cannot be parsed is stored as it came.
func (r *run) page(p string, body []byte) []byte {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return body
	}
	endpoint := r.req.FormMap[p]
	changed := false
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			if c.Type == html.ElementNode {
				r.links(p, c)
				if c.DataAtom == atom.Form && endpoint != "" {
					n.InsertBefore(formFrame(r.req.PortalURL, endpoint), c)
					n.RemoveChild(c)
					changed = true
					c = next
					continue
				}
			}
			walk(c)
			c = next
		}
	}
	walk(doc)
	if !changed {
		return body
	}
	var out bytes.Buffer
	if err := html.Render(&out, doc); err != nil {
		return body
	}
	return out.Bytes()
}

// formFrame is a frame of the platform form. endpoint passed endpointName.
func formFrame(portalURL, endpoint string) *html.Node {
	return &html.Node{Type: html.ElementNode, DataAtom: atom.Iframe, Data: "iframe", Attr: []html.Attribute{
		{Key: attrSrc, Val: strings.TrimSuffix(portalURL, pathSep) + "/forms/" + endpoint},
		{Key: "title", Val: "Form"},
		{Key: "style", Val: "width:100%;border:0;min-height:480px"},
	}}
}

func (r *run) links(page string, n *html.Node) {
	attrs, ok := linkAttrs[n.DataAtom]
	if !ok {
		return
	}
	for _, a := range n.Attr {
		if slices.Contains(attrs, a.Key) {
			r.follow(page, a.Val)
		}
	}
}

// follow queues a link on the same host, by its path alone, resolved against
// the page it appears on.
func (r *run) follow(page, ref string) {
	u, err := r.root.ResolveReference(&url.URL{Path: page}).Parse(strings.TrimSpace(ref))
	if err != nil || u.Host != r.root.Host || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" {
		return
	}
	r.enqueue(u.Path)
}
