package notifyrender

import (
	"bytes"
	htmltemplate "html/template"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// markdown converts a channel document's body to HTML for its email.
//
// It is goldmark with the GFM table and strikethrough extensions and the
// default renderer. The default renderer does not emit raw HTML -- an HTML
// block or inline tag in the source is written as an omission comment -- and
// drops a link whose destination is a script URL, so a sender's markdown
// cannot put markup of its own into the message. html.WithUnsafe is never set
// here, and this is the one place a document body becomes HTML.
var markdown = goldmark.New(goldmark.WithExtensions(extension.Table, extension.Strikethrough))

// markdownHTML renders source to the HTML an email item shows. The result is
// typed template.HTML so the template writes it unescaped; it is safe to do so
// because the renderer above is the only thing that produces it.
func markdownHTML(source string) htmltemplate.HTML {
	if strings.TrimSpace(source) == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := markdown.Convert([]byte(source), &buf); err != nil {
		// goldmark writes into a bytes.Buffer, which cannot fail. Were it to,
		// no HTML is returned and the template shows the item's Body,
		// escaped, in its place.
		return ""
	}
	return htmltemplate.HTML(buf.String()) // #nosec G203 -- goldmark's default renderer emits no raw HTML from the source
}

// firstParagraph returns a markdown body's first paragraph: what a digest or
// a bulletin shows of each document, with the link carrying the rest.
func firstParagraph(source string) string {
	head, _, _ := strings.Cut(strings.TrimSpace(source), "\n\n")
	return strings.TrimSpace(head)
}
