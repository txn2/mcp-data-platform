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
	attrDataSrc  = "data-src"
	attrSrcset   = "srcset"
	attrDataSet  = "data-srcset"
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
	// MaxPages and MaxBytes bound this copy, the crawler's own bounds when 0.
	MaxPages int
	MaxBytes int64
	// Progress is told after each address how many pages were copied and how
	// many addresses are known, and returns true to stop; nil copies to the end.
	Progress func(sitesvc.CopyStats) (stop bool)
}

// ErrStopped is what a copy that was asked to stop returns.
var ErrStopped = sitesvc.ErrStopped

// Result is what a crawl found.
type Result struct {
	Files     []sitesvc.InputFile
	Pages     int
	Redirects int
	Skipped   []site.Skipped
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
	skipped   []site.Skipped
	pages     int
	total     int64
	maxBytes  int64
}

// Crawl walks the site from req.Start, staying on its host.
func (c *Crawler) Crawl(ctx context.Context, req Request) (*Result, error) {
	root, err := checkRequest(req)
	if err != nil {
		return nil, err
	}
	r := &run{c: c, req: req, root: root, seen: map[string]bool{}, maxBytes: cmp.Or(req.MaxBytes, c.MaxBytes)}
	maxPages := cmp.Or(req.MaxPages, c.MaxPages)
	r.enqueue(root.Path)
	for _, p := range wellKnownFiles {
		r.enqueue(p)
	}
	for len(r.queue) > 0 && r.pages < maxPages {
		next := r.queue[0]
		r.queue = r.queue[1:]
		if err := r.fetch(ctx, next); err != nil {
			return nil, err
		}
		if req.Progress != nil && req.Progress(sitesvc.CopyStats{Pages: r.pages, Known: len(r.seen), Bytes: r.total, Redirects: len(r.redirects), Skipped: len(r.skipped)}) {
			return nil, ErrStopped
		}
	}
	return r.result(), nil
}

// SourceOf is the address a release records it was copied from: the scheme,
// host and path, since a query or fragment can hold what should not be kept.
func SourceOf(start string) string {
	u, err := url.Parse(start)
	if err != nil {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

// checkRequest refuses a start that is not an http(s) address and a form map
// entry that is not a page path and an endpoint name.
func checkRequest(req Request) (*url.URL, error) {
	root, err := url.Parse(req.Start)
	if err != nil || (root.Scheme != "http" && root.Scheme != "https") || root.Host == "" {
		return nil, &sitesvc.RefusedError{Msg: "url: an http(s) address"}
	}
	if root.User != nil {
		return nil, &sitesvc.RefusedError{Msg: "url: an address holding a user name or password cannot be copied"}
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

// skip records an address that could not be copied, and why.
func (r *run) skip(p, reason string) {
	r.skipped = append(r.skipped, site.Skipped{Address: p, Reason: reason})
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
		r.skip(p, "the address cannot be requested")
		return nil //nolint:nilerr // a path that cannot be requested is reported, not fatal
	}
	res, err := r.c.Client.Do(req) // #nosec G704 -- the client's transport is the egress guard's
	if err != nil {
		// The error names what the guard resolved, so it stays out of the reason.
		r.skip(p, "the address did not answer, or is not one a copy may reach")
		return nil //nolint:nilerr // an address that does not answer is reported, not fatal
	}
	defer func() { _ = res.Body.Close() }()
	switch {
	case res.StatusCode >= http.StatusMultipleChoices && res.StatusCode < http.StatusBadRequest:
		r.redirected(p, res)
		return nil
	case res.StatusCode != http.StatusOK:
		r.skip(p, fmt.Sprintf("answered %d", res.StatusCode))
		return nil
	}
	return r.store(p, res)
}

// redirected records a rule for an address that redirected, and walks where
// it went when that stays on the site.
func (r *run) redirected(p string, res *http.Response) {
	loc, err := res.Location()
	if err != nil {
		r.skip(p, "redirected with no usable address")
		return
	}
	if len(r.redirects) >= redirectsMax {
		r.skip(p, "the site has more redirects than a release can hold")
		return
	}
	target := loc.String()
	if loc.Host == r.root.Host {
		target = cmp.Or(loc.Path, pathSep)
		r.enqueue(target)
	}
	rule := site.Redirect{From: p, To: target, Status: res.StatusCode}
	if site.ValidateRedirect(rule) != nil {
		r.skip(p, "redirected somewhere a release cannot point")
		return
	}
	r.redirects = append(r.redirects, rule)
}

func (r *run) store(p string, res *http.Response) error {
	remaining := r.maxBytes - r.total
	body, err := io.ReadAll(io.LimitReader(res.Body, remaining+1))
	if err != nil {
		r.skip(p, "the answer broke off")
		return nil //nolint:nilerr // a body that breaks off is reported, not fatal
	}
	if int64(len(body)) > remaining {
		return &sitesvc.RefusedError{Msg: fmt.Sprintf("the site holds more than the %d bytes sites.crawl.max_bytes allows", r.maxBytes)}
	}
	r.total += int64(len(body))
	r.pages++
	isHTML := strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "text/html")
	name := filePath(p, isHTML)
	// A path a release cannot hold is reported, not fatal: one .htaccess link
	// on a page must not stop the whole copy.
	if _, err := sitesvc.CleanPath(name); err != nil {
		r.skip(p, "a release cannot hold a file at this path")
		return nil //nolint:nilerr // a path a release cannot hold is reported, not fatal
	}
	if isHTML {
		body = r.page(p, body)
	} else if isStylesheet(res.Header.Get("Content-Type"), name) {
		r.cssRefs(p, string(body))
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

// linkAttrs are the attributes that name another resource. An <a> names a
// page; every other one names a file the page loads (#2589), including the
// candidates of a srcset and the attributes lazy loading holds the real
// address in while src holds a placeholder.
var linkAttrs = map[atom.Atom][]string{
	atom.A: {"href"}, atom.Link: {"href", "imagesrcset"}, atom.Script: {attrSrc},
	atom.Img:    {attrSrc, attrSrcset, attrDataSrc, attrDataSet, "data-lazy-src"},
	atom.Source: {attrSrc, attrSrcset, attrDataSrc, attrDataSet},
	atom.Video:  {attrSrc, "poster", attrDataSrc}, atom.Audio: {attrSrc}, atom.Iframe: {attrSrc, attrDataSrc},
}

// srcsetAttrs hold a list of candidates rather than one address.
var srcsetAttrs = map[string]bool{attrSrcset: true, attrDataSet: true, "imagesrcset": true}

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
	if n.DataAtom == atom.Style {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.TextNode {
				r.cssRefs(page, c.Data)
			}
		}
	}
	attrs := linkAttrs[n.DataAtom]
	resource := n.DataAtom != atom.A
	for _, a := range n.Attr {
		switch {
		case a.Key == "style":
			r.cssRefs(page, a.Val)
		case !slices.Contains(attrs, a.Key):
		case srcsetAttrs[a.Key]:
			for _, ref := range srcsetURLs(a.Val) {
				r.follow(page, ref, true)
			}
		default:
			r.follow(page, a.Val, resource)
		}
	}
}

// follow queues a link on the same host, by its path alone, resolved against
// base, the page or stylesheet it appears in. A page link with a query is not
// walked: /search?q=1 and filter links are endless. A file a page loads is
// walked by its path with the query dropped (#2589): /site.css?ver=1.2 is the
// file /site.css, and the page keeps its link, which answers with that file.
func (r *run) follow(base, ref string, resource bool) {
	u, err := r.root.ResolveReference(&url.URL{Path: base}).Parse(strings.TrimSpace(ref))
	if err != nil || u.Host != r.root.Host || (u.Scheme != "http" && u.Scheme != "https") || (u.RawQuery != "" && !resource) {
		return
	}
	r.enqueue(u.Path)
}

// isStylesheet reports whether a fetched file is CSS, by its type or, when a
// server labels it loosely, its name.
func isStylesheet(contentType, name string) bool {
	ct, _, _ := strings.Cut(strings.ToLower(contentType), ";")
	return strings.TrimSpace(ct) == "text/css" || strings.EqualFold(path.Ext(name), ".css")
}

// cssComment, cssURL and cssImport read the references a stylesheet makes:
// url(...) in any of its three quotings, and @import of a quoted address.
var (
	cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssURL     = regexp.MustCompile(`(?i)url\(\s*(?:"([^"]*)"|'([^']*)'|([^)"'\s]*))\s*\)`)
	cssImport  = regexp.MustCompile(`(?i)@import\s+(?:"([^"]*)"|'([^']*)')`)
)

// cssRefs walks the files a stylesheet, a <style> element or a style
// attribute names (fonts, background images, imported stylesheets), resolved
// against base, the address the CSS was read from (#2589).
func (r *run) cssRefs(base, css string) {
	css = cssComment.ReplaceAllString(css, "")
	for _, re := range []*regexp.Regexp{cssURL, cssImport} {
		for _, m := range re.FindAllStringSubmatch(css, -1) {
			if ref := firstNonEmpty(m[1:]); ref != "" {
				r.follow(base, ref, true)
			}
		}
	}
}

func firstNonEmpty(values []string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// srcsetURLs are the addresses of a srcset's candidates, read as the HTML
// standard reads them: an address runs to the next whitespace (so a data: URL
// holding a comma stays whole), and its descriptors run to the next comma
// outside parentheses.
func srcsetURLs(srcset string) []string {
	var out []string
	for pos := 0; pos < len(srcset); {
		for pos < len(srcset) && (isHTMLSpace(srcset[pos]) || srcset[pos] == ',') {
			pos++
		}
		start := pos
		for pos < len(srcset) && !isHTMLSpace(srcset[pos]) {
			pos++
		}
		ref := strings.TrimRight(srcset[start:pos], ",")
		if ref != "" {
			out = append(out, ref)
		}
		if strings.HasSuffix(srcset[start:pos], ",") {
			continue
		}
		pos = skipDescriptors(srcset, pos)
	}
	return out
}

// skipDescriptors moves past a candidate's descriptors to the comma that ends
// it, a comma inside parentheses not counting.
func skipDescriptors(s string, pos int) int {
	depth := 0
	for ; pos < len(s); pos++ {
		switch s[pos] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				return pos + 1
			}
		}
	}
	return pos
}

func isHTMLSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}
