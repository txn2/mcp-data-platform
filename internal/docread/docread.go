// Package docread renders a stored file's bytes into what a reader that can
// only read text, or only look at a picture, is able to use.
//
// The platform stores whatever a person uploads. Everything downstream of that
// -- the search `fetch` reference, the content index -- could until now only
// use a file whose media type passed contenttype.IsTextual, so a PDF, a
// presentation and a spreadsheet each resolved to their metadata row and
// nothing else (#1657). This package is the one answer to "what does this file
// read as", and every surface that hands a stored file to a model asks it.
//
// # The rule
//
// One file yields exactly one Form, chosen by what the bytes are rather than
// by what the writer declared:
//
//   - Text, when the family is textual, when a PDF extractor is bound and the
//     bytes are a PDF, or when the bytes are a zip container whose members
//     carry text (which is what a .docx, .xlsx, .pptx, .odt, .ods and .odp are).
//   - Image, for a raster picture, which a model looks at directly and no
//     amount of extraction improves.
//   - Opaque, when nothing here reads the family. The bytes are not text and
//     not a picture, so they travel as themselves and the caller's own tools
//     open them.
//
// Opaque is the floor, never a refusal: a file whose bytes exist always
// renders as something. That is the whole point -- before this, an unreadable
// family degraded to metadata, and an agent handed metadata concludes the file
// cannot be opened at all.
//
// # Bounds
//
// Every path is bounded by the caller's limit, in bytes of rendered text. The
// package holds no opinion about what that limit should be; the fetch surface
// spends resource.MaxInlineContentBytes and the index spends
// resource.MaxContentIndexBytes, and each says so at its call site.
package docread

import (
	"bytes"
	"context"
	"strings"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// Form names what a file's bytes were rendered into. It is what tells a caller
// which field of Result to read and, for a caller building MCP content, which
// kind of content block the file belongs in.
type Form string

const (
	// FormText means Text holds readable text drawn out of the file.
	FormText Form = "text"
	// FormImage means the bytes are a picture, to be handed over as they are
	// for a model to look at. Text is empty.
	FormImage Form = "image"
	// FormOpaque means nothing here reads this family. The bytes are handed
	// over as they are for the caller's own tools to open. Text is empty.
	FormOpaque Form = "opaque"
)

// Result is what one file's bytes read as.
type Result struct {
	// Form is which of the three renderings happened.
	Form Form
	// Text is the readable text, for FormText only.
	Text string
	// Note records what the rendering left out, in one line, and is empty when
	// nothing was. It is written for the reader of the Text, not for a log: a
	// model that knows a deck's last four slides were dropped for budget asks
	// for them, where one that silently received twelve of sixteen does not.
	Note string
	// Truncated reports that the limit, rather than the end of the file, ended
	// the text.
	Truncated bool
}

// PDFTextExtractor draws the text out of a PDF. It is an interface rather than
// a direct dependency because the only implementation that reads a modern PDF
// carries a WebAssembly runtime (see internal/pdftext), and this package, its
// tests and every caller that never meets a PDF should not pay for it.
//
// A Reader built with no extractor renders a PDF as FormOpaque, which is the
// honest degradation: the bytes still reach the caller, and their own reader
// opens them.
type PDFTextExtractor interface {
	// ExtractText returns the document's text, stopping once it has produced
	// limit bytes. An error means the bytes could not be read as a PDF.
	ExtractText(ctx context.Context, body []byte, limit int) (string, error)
}

// Reader renders stored files. The zero value is usable and renders a PDF as
// opaque; New binds an extractor that reads one.
type Reader struct {
	pdf PDFTextExtractor
}

// New builds a Reader over an optional PDF text extractor. A nil extractor is
// allowed and is what a deployment gets when PDF text is unavailable.
func New(pdf PDFTextExtractor) *Reader {
	return &Reader{pdf: pdf}
}

// MayHoldText reports whether a file of this declared media type could yield
// text under any reading. It exists for a caller that must decide whether the
// file is worth fetching at all: the content index pulls a whole object out of
// blob storage to read it, and a picture, a sound or a video will not repay
// that however it is read.
//
// It answers on the declaration alone, so it is deliberately generous. A
// generic or absent type -- which is what an uploading client sends when it
// sniffs rather than reads the extension -- is a maybe, because the bytes
// underneath could be anything, and only Read, holding them, can say. Being
// wrong in that direction costs one blob read; being wrong the other way would
// leave a PDF unsearchable.
func MayHoldText(mimeType string) bool {
	norm := contenttype.Normalize(mimeType)
	if contenttype.IsTextual(norm) {
		return true
	}
	for _, family := range []string{"image/", "audio/", "video/", "font/"} {
		if strings.HasPrefix(norm, family) {
			return false
		}
	}
	return true
}

// pdfMagic is the file signature every PDF opens with. The bytes are consulted
// rather than the declared type because a PDF uploaded with no Content-Type,
// or with a generic one, is still a PDF.
var pdfMagic = []byte("%PDF-")

// Read renders body, whose declared media type is mimeType and whose filename
// is name, into at most limit bytes of text.
//
// mimeType and name are hints, not authority. Where the bytes carry a
// signature -- a PDF header, a zip's local file header -- that signature
// decides, because a stored type is whatever the uploading client claimed and
// the two disagree often enough to matter: a .pptx arrives as
// application/zip from any client that sniffs rather than reads the extension.
//
// A limit at or below zero renders nothing and reports FormOpaque, so a caller
// with no budget still gets the bytes rather than an error.
func (r *Reader) Read(ctx context.Context, mimeType, name string, body []byte, limit int) Result {
	if len(body) == 0 || limit <= 0 {
		return Result{Form: FormOpaque}
	}

	// A container's own signature outranks everything, including a specific
	// declaration, because that is the one thing that cannot be wrong: a
	// .pptx stored as application/zip and a PDF stored as octet-stream are
	// both routine, and neither is readable off its declared type.
	switch {
	case bytes.HasPrefix(body, pdfMagic):
		return r.readPDF(ctx, body, limit)
	case isZipContainer(body):
		return readArchive(body, limit)
	}

	// Otherwise resolve the type through the platform's own detector, which
	// weighs the declaration, the filename and the bytes together, rather than
	// trusting the declaration alone. It is what recovers a notes.md stored as
	// octet-stream, and it resolves SVG -- textual and an image both -- in
	// favor of reading it, because the textual test comes first.
	resolved := contenttype.DetectFile(mimeType, name, body)
	switch {
	case contenttype.IsTextual(resolved):
		return textResult(string(body), limit)
	case resolved == contenttype.PDF:
		return r.readPDF(ctx, body, limit)
	case contenttype.IsImage(resolved):
		return Result{Form: FormImage}
	default:
		return Result{Form: FormOpaque}
	}
}

// readPDF renders a PDF's text, or falls back to opaque when there is no
// extractor bound or the document could not be read. A file that fails to
// parse is not an error the caller should see: the bytes are still a PDF, and
// handing them over unrendered is strictly better than refusing the fetch.
func (r *Reader) readPDF(ctx context.Context, body []byte, limit int) Result {
	if r.pdf == nil {
		return Result{Form: FormOpaque, Note: "no PDF text extractor is configured; the file is served as bytes"}
	}
	// One byte past the budget, deliberately. An extractor stops once it has
	// produced what was asked for, so asking for exactly the budget makes a
	// document that fills it indistinguishable from one that ends there, and
	// the cut would go unreported. Asking for one more byte makes the
	// overshoot the evidence.
	text, err := r.pdf.ExtractText(ctx, body, limit+1)
	if err != nil {
		return Result{Form: FormOpaque, Note: "this PDF's text could not be extracted; the file is served as bytes"}
	}
	if strings.TrimSpace(text) == "" {
		return Result{Form: FormOpaque, Note: "this PDF holds no extractable text (it is likely scanned images); the file is served as bytes"}
	}
	return textResult(text, limit)
}

// textResult bounds text to limit and reports whether the cut happened, taking
// care not to leave a partial rune at the end.
func textResult(text string, limit int) Result {
	if len(text) <= limit {
		return Result{Form: FormText, Text: text}
	}
	return Result{Form: FormText, Text: truncate(text, limit), Truncated: true}
}

// truncate cuts s to at most limit bytes on a rune boundary. At most
// utf8.UTFMax-1 bytes can belong to the rune the cut lands inside, so the
// backtrack is bounded rather than a scan of the prefix.
func truncate(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	for i := 0; i < utf8.UTFMax-1 && s != ""; i++ {
		r, size := utf8.DecodeLastRuneInString(s)
		if r != utf8.RuneError || size != 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}
