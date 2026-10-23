package imapmail

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/txn2/mcp-data-platform/internal/untrusted"
)

// HTMLToText reads an HTML body as the text a person reading the message would
// see, and nothing else: scripts, styles, comments and the document head are
// dropped, and so is every element a sender hid from the reader (the hidden
// attribute, aria-hidden, display:none, visibility:hidden, zero font size,
// zero opacity, zero width or height), which is where an instruction meant
// only for a model would be put. Images become their alt text; a link keeps
// its text and, when the address differs from it, the address in brackets.
// Nothing is fetched.
func HTMLToText(body string) string {
	c := &converter{z: html.NewTokenizer(strings.NewReader(body))}
	for {
		switch tt := c.z.Next(); tt {
		case html.ErrorToken:
			return c.w.done()
		case html.TextToken:
			if c.skip == 0 {
				c.w.text(string(c.z.Text()))
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			c.start(c.z.Token(), tt == html.StartTagToken)
		case html.EndTagToken:
			c.end(c.z.Token())
		case html.CommentToken, html.DoctypeToken:
		}
	}
}

// converter walks one document's tokens.
type converter struct {
	z *html.Tokenizer
	w textWriter
	// open counts the elements started and not yet ended, so a stray end
	// tag is ignored.
	open int
	// skip counts the open elements inside a hidden one; while it is above
	// zero, text is discarded.
	skip int
}

func (c *converter) start(tok html.Token, opens bool) {
	hidden := c.skip > 0 || hiddenElement(tok)
	if !hidden {
		c.w.open(tok)
	}
	if !opens || voidElement(tok.DataAtom) {
		return
	}
	c.open++
	if hidden {
		c.skip++
	}
}

func (c *converter) end(tok html.Token) {
	if c.open == 0 {
		return
	}
	c.open--
	if c.skip > 0 {
		c.skip--
		return
	}
	c.w.close(tok)
}

// newline separates lines of the text written.
const newline = "\n"

// textWriter accumulates the visible text with block breaks.
type textWriter struct {
	b strings.Builder
	// space records whitespace seen after the last text written, so the
	// next text is separated from it only when the source separated them.
	space bool
	// href holds the address of each open link, innermost last.
	href []string
}

func (w *textWriter) text(raw string) {
	s := strings.Join(strings.Fields(raw), " ")
	if s == "" {
		w.space = w.space || raw != ""
		return
	}
	cur := w.b.String()
	lead := w.space || strings.TrimLeft(raw, " \t\r\n") != raw
	if lead && cur != "" && !strings.HasSuffix(cur, newline) && !strings.HasSuffix(cur, " ") {
		_ = w.b.WriteByte(' ')
	}
	_, _ = w.b.WriteString(s)
	w.space = strings.TrimRight(raw, " \t\r\n") != raw
}

func (w *textWriter) newline(n int) {
	cur := w.b.String()
	trail := len(cur) - len(strings.TrimRight(cur, newline))
	for i := trail; i < n && cur != ""; i++ {
		_ = w.b.WriteByte('\n')
	}
}

// blocks are the elements that start and end a paragraph.
var blocks = map[atom.Atom]bool{ //nolint:gochecknoglobals // fixed table
	atom.P: true, atom.Div: true, atom.Table: true, atom.Ul: true, atom.Ol: true, atom.Blockquote: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true, atom.Pre: true,
}

func (w *textWriter) open(tok html.Token) {
	switch {
	case tok.DataAtom == atom.Br:
		_ = w.b.WriteByte('\n')
	case blocks[tok.DataAtom] || tok.DataAtom == atom.Hr:
		w.newline(2)
	case tok.DataAtom == atom.Tr:
		w.newline(1)
	case tok.DataAtom == atom.Li:
		w.newline(1)
		_, _ = w.b.WriteString("- ")
	default:
		w.inline(tok)
	}
}

// inline handles the elements that add to the line: a table cell's
// separator, an image's alt text, and the start of a link.
func (w *textWriter) inline(tok html.Token) {
	switch tok.DataAtom {
	case atom.Td, atom.Th:
		w.text(" | ")
	case atom.Img:
		if alt := attr(tok, "alt"); alt != "" {
			w.text("[image: " + alt + "]")
		}
	case atom.A:
		w.href = append(w.href, attr(tok, "href"))
	}
}

func (w *textWriter) close(tok html.Token) {
	switch {
	case blocks[tok.DataAtom]:
		w.newline(2)
	case tok.DataAtom == atom.A && len(w.href) > 0:
		link := w.href[len(w.href)-1]
		w.href = w.href[:len(w.href)-1]
		if link != "" && !strings.HasPrefix(link, "#") && !strings.HasSuffix(w.b.String(), link) {
			_, _ = w.b.WriteString(" [" + link + "]")
		}
	}
}

func (w *textWriter) done() string {
	return strings.TrimSpace(untrusted.Clean(w.b.String()))
}

func attr(tok html.Token, name string) string {
	for _, a := range tok.Attr {
		if strings.EqualFold(a.Key, name) {
			return a.Val
		}
	}
	return ""
}

// dropped are the elements whose content is never text a reader sees.
var dropped = map[atom.Atom]bool{ //nolint:gochecknoglobals // fixed table
	atom.Script: true, atom.Style: true, atom.Head: true, atom.Title: true, atom.Template: true,
	atom.Noscript: true, atom.Object: true, atom.Embed: true, atom.Iframe: true, atom.Svg: true, atom.Math: true,
}

// hiddenStyle matches the inline declarations that hide an element.
var hiddenStyle = regexp.MustCompile(`(?i)(display\s*:\s*none|visibility\s*:\s*hidden|font-size\s*:\s*0(\.0*)?(px|pt|em|rem|%)?\s*(;|$|!)|opacity\s*:\s*0(\.0*)?\s*(;|$|!)|(^|[;\s])(max-)?(width|height)\s*:\s*0(px)?\s*(;|$|!)|clip\s*:\s*rect\(\s*0)`)

func hiddenElement(tok html.Token) bool {
	if dropped[tok.DataAtom] {
		return true
	}
	for _, a := range tok.Attr {
		switch strings.ToLower(a.Key) {
		case "hidden":
			return true
		case "aria-hidden":
			if strings.EqualFold(a.Val, "true") {
				return true
			}
		case "style":
			if hiddenStyle.MatchString(a.Val) {
				return true
			}
		}
	}
	return false
}

func voidElement(a atom.Atom) bool {
	switch a {
	case atom.Area, atom.Base, atom.Br, atom.Col, atom.Embed, atom.Hr, atom.Img, atom.Input,
		atom.Link, atom.Meta, atom.Param, atom.Source, atom.Track, atom.Wbr:
		return true
	}
	return false
}
