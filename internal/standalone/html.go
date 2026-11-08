package standalone

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// html inlines what an HTML document loads: a script as an inline script, a
// stylesheet as a style element, an image, a font or a media file as a data:
// URI, a quoted URL in an inline script as script literals are, and a download
// link to a declared file as the file itself.
func (s *session) html(ctx context.Context, doc []byte) ([]byte, error) {
	root, err := html.Parse(bytes.NewReader(doc))
	if err == nil {
		s.walk(ctx, root)
		var out bytes.Buffer
		if err = html.Render(&out, root); err == nil {
			return out.Bytes(), nil
		}
	}
	return nil, fmt.Errorf("the document could not be read or written: %w", err)
}

// walk visits every element below n. The children are listed before they are
// visited, since a visit may replace the node it is on.
func (s *session) walk(ctx context.Context, n *html.Node) {
	var kids []*html.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		kids = append(kids, c)
	}
	for _, c := range kids {
		if c.Type == html.ElementNode {
			s.element(ctx, c)
		}
		s.walk(ctx, c)
	}
}

// Attribute names the export reads.
const (
	attrSrc    = "src"
	attrHref   = "href"
	attrSrcset = "srcset"
	attrStyle  = "style"
)

// mediaAttrs are the attributes that name a file an element shows.
var mediaAttrs = map[atom.Atom][]string{
	atom.Img:    {attrSrc},
	atom.Source: {attrSrc},
	atom.Video:  {attrSrc, "poster"},
	atom.Audio:  {attrSrc},
	atom.Track:  {attrSrc},
	atom.Input:  {attrSrc},
	atom.Image:  {attrHref, "xlink:href"},
}

// elementHandlers are the elements whose content or kind decides what the
// export does with them.
var elementHandlers = map[atom.Atom]func(*session, context.Context, *html.Node){
	atom.Script: (*session).script,
	atom.Style:  (*session).styleElement,
	atom.Link:   (*session).link,
	atom.Iframe: (*session).frame,
	atom.Embed:  (*session).frame,
	atom.Object: (*session).frame,
	atom.A:      (*session).anchor,
}

func (s *session) element(ctx context.Context, n *html.Node) {
	if v, ok := attr(n, attrStyle); ok {
		setAttr(n, attrStyle, s.css(ctx, s.in.Origin, v, 0))
	}
	if handle, ok := elementHandlers[n.DataAtom]; ok {
		handle(s, ctx, n)
	}
	for _, name := range mediaAttrs[n.DataAtom] {
		s.inlineAttr(ctx, n, name)
	}
	if v, ok := attr(n, attrSrcset); ok && (n.DataAtom == atom.Img || n.DataAtom == atom.Source) {
		setAttr(n, attrSrcset, s.srcset(ctx, v))
	}
}

// inlineAttr replaces a URL attribute with the data: URI of what it names.
func (s *session) inlineAttr(ctx context.Context, n *html.Node, name string) {
	v, ok := attr(n, name)
	if !ok {
		return
	}
	if data := s.inlineAsset(ctx, s.in.Origin, v); data != "" {
		setAttr(n, name, data)
	}
}

// srcset inlines each candidate of a srcset list.
func (s *session) srcset(ctx context.Context, v string) string {
	parts := strings.Split(v, ",")
	for i, part := range parts {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		if data := s.inlineAsset(ctx, s.in.Origin, fields[0]); data != "" {
			fields[0] = data
		}
		parts[i] = strings.Join(fields, " ")
	}
	return strings.Join(parts, ", ")
}

// scriptClose and styleClose are the end tags a raw-text element's content
// must not contain.
var (
	scriptClose = regexp.MustCompile(`(?i)</script`)
	styleClose  = regexp.MustCompile(`(?i)</style`)
)

// script inlines a script's source, or the quoted URLs in an inline one.
func (s *session) script(ctx context.Context, n *html.Node) {
	src, ok := attr(n, attrSrc)
	if !ok {
		if t := textOf(n); t != "" && isJavaScript(n) {
			setText(n, s.literals(ctx, t))
		}
		return
	}
	f, ok := s.get(ctx, s.in.Origin, src)
	if !ok {
		return
	}
	removeAttr(n, attrSrc)
	removeAttr(n, "integrity")
	removeAttr(n, "crossorigin")
	setText(n, scriptClose.ReplaceAllString(string(f.body), `<\/script`))
}

// isJavaScript reports whether a script element holds JavaScript (not a JSON
// island, an import map or a template).
func isJavaScript(n *html.Node) bool {
	t, ok := attr(n, "type")
	if !ok {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "", "module", "text/javascript", "application/javascript":
		return true
	}
	return false
}

func (s *session) styleElement(ctx context.Context, n *html.Node) {
	if t := textOf(n); t != "" {
		setText(n, styleClose.ReplaceAllString(s.css(ctx, s.in.Origin, t, 0), `<\/style`))
	}
}

// link inlines a stylesheet as a style element and an icon as a data: URI.
func (s *session) link(ctx context.Context, n *html.Node) {
	rel, _ := attr(n, "rel")
	rels := strings.Fields(strings.ToLower(rel))
	switch {
	case slices.Contains(rels, "stylesheet"):
		s.stylesheet(ctx, n)
	case slices.Contains(rels, "icon"), slices.Contains(rels, "apple-touch-icon"):
		s.inlineAttr(ctx, n, attrHref)
	}
}

func (s *session) stylesheet(ctx context.Context, n *html.Node) {
	href, _ := attr(n, attrHref)
	f, ok := s.get(ctx, s.in.Origin, href)
	if !ok {
		return
	}
	style := &html.Node{Type: html.ElementNode, Data: "style", DataAtom: atom.Style}
	if media, ok := attr(n, "media"); ok {
		style.Attr = append(style.Attr, html.Attribute{Key: "media", Val: media})
	}
	style.AppendChild(&html.Node{
		Type: html.TextNode,
		Data: styleClose.ReplaceAllString(s.css(ctx, f.u, string(f.body), 0), `<\/style`),
	})
	n.Parent.InsertBefore(style, n)
	n.Parent.RemoveChild(n)
}

// frame names a framed page on another address: a page cannot be carried.
func (s *session) frame(_ context.Context, n *html.Node) {
	for _, name := range []string{attrSrc, "data"} {
		if v, ok := attr(n, name); ok {
			if _, where := s.resolve(s.in.Origin, v); where == placeExternal || where == placeOther || where == placeOwn {
				s.notice(v, "a framed page, which needs the network")
			}
		}
	}
}

// anchor carries a download link to a declared file as the file itself, so
// it still downloads offline. Any other link is navigation and stays.
func (s *session) anchor(ctx context.Context, n *html.Node) {
	href, ok := attr(n, attrHref)
	if !ok {
		return
	}
	u, where := s.resolve(s.in.Origin, href)
	if where != placeOwn || !isRef(u) {
		return
	}
	if _, isDownload := attr(n, "download"); !isDownload && u.Query().Get("download") != "1" {
		return
	}
	got := s.fetch(ctx, u, where)
	if got.err != nil {
		s.notice(displayURL(u, where), "could not be fetched: "+errorText(got.err))
		return
	}
	setAttr(n, attrHref, dataURI(got.contentType, got.body))
}

// Node helpers.

func attr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if strings.EqualFold(attrKey(a), key) {
			return a.Val, true
		}
	}
	return "", false
}

func attrKey(a html.Attribute) string {
	if a.Namespace != "" {
		return a.Namespace + ":" + a.Key
	}
	return a.Key
}

func setAttr(n *html.Node, key, val string) {
	for i, a := range n.Attr {
		if strings.EqualFold(attrKey(a), key) {
			n.Attr[i].Val = val
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

func removeAttr(n *html.Node, key string) {
	kept := n.Attr[:0]
	for _, a := range n.Attr {
		if !strings.EqualFold(attrKey(a), key) {
			kept = append(kept, a)
		}
	}
	n.Attr = kept
}

func textOf(n *html.Node) string {
	var parts []string
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			parts = append(parts, c.Data)
		}
	}
	return strings.Join(parts, "")
}

func setText(n *html.Node, text string) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		c = next
	}
	n.AppendChild(&html.Node{Type: html.TextNode, Data: text})
}
