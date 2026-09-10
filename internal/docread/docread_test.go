package docread_test

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/docread"
)

// stubPDF stands in for the WebAssembly extractor so this package's tests
// exercise the dispatch rule without paying for a module compile.
type stubPDF struct {
	text string
	err  error
}

// ExtractText models the real extractor's contract: it stops once it has
// produced what was asked for, which is what makes the caller's budget
// arithmetic testable.
func (s stubPDF) ExtractText(_ context.Context, _ []byte, limit int) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if len(s.text) > limit {
		return s.text[:limit], nil
	}
	return s.text, nil
}

// zipOf builds an archive whose members are the given name/content pairs, in
// the order given.
func zipOf(t *testing.T, members ...[2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, m := range members {
		w, err := zw.Create(m[0])
		if err != nil {
			t.Fatalf("creating %s: %v", m[0], err)
		}
		if _, err = w.Write([]byte(m[1])); err != nil {
			t.Fatalf("writing %s: %v", m[0], err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("closing the archive: %v", err)
	}
	return buf.Bytes()
}

// pngBytes is the shortest valid PNG: the signature plus an IHDR chunk, which
// is all http.DetectContentType looks at.
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)

func TestReadTextualContentComesBackAsText(t *testing.T) {
	r := docread.New(nil)
	got := r.Read(context.Background(), "text/markdown", "notes.md", []byte("# Notes\nbody"), 1<<20)
	if got.Form != docread.FormText || !strings.Contains(got.Text, "# Notes") {
		t.Fatalf("markdown did not read as text: %+v", got)
	}
}

func TestReadAnImageIsHandedOverAsAPicture(t *testing.T) {
	got := docread.New(nil).Read(context.Background(), "image/png", "chart.png", pngBytes, 1<<20)
	if got.Form != docread.FormImage {
		t.Fatalf("a PNG did not read as an image: %+v", got)
	}
	if got.Text != "" {
		t.Fatalf("an image carried text: %q", got.Text)
	}
}

func TestReadAnUnknownBinaryFallsToItsBytes(t *testing.T) {
	got := docread.New(nil).Read(context.Background(), "application/octet-stream", "thing.bin", []byte{0x00, 0x01, 0x02, 0xff}, 1<<20)
	if got.Form != docread.FormOpaque {
		t.Fatalf("an unreadable family did not fall to its bytes: %+v", got)
	}
}

func TestReadWithNoBudgetFallsToItsBytes(t *testing.T) {
	got := docread.New(nil).Read(context.Background(), "text/plain", "a.txt", []byte("hello"), 0)
	if got.Form != docread.FormOpaque {
		t.Fatalf("a zero budget produced %+v", got)
	}
}

func TestReadAPDFUsesTheBoundExtractor(t *testing.T) {
	r := docread.New(stubPDF{text: "page one text"})
	got := r.Read(context.Background(), "application/pdf", "report.pdf", []byte("%PDF-1.5 ..."), 1<<20)
	if got.Form != docread.FormText || got.Text != "page one text" {
		t.Fatalf("the extractor's text did not reach the result: %+v", got)
	}
}

// A PDF is recognized by its signature, not by what the uploading client
// called it, because a client that sniffs rather than reads the extension
// stores one as application/octet-stream.
func TestReadAPDFIsRecognizedByItsSignature(t *testing.T) {
	r := docread.New(stubPDF{text: "found anyway"})
	got := r.Read(context.Background(), "application/octet-stream", "report", []byte("%PDF-1.7 ..."), 1<<20)
	if got.Form != docread.FormText || got.Text != "found anyway" {
		t.Fatalf("a PDF declared as bytes was not recognized: %+v", got)
	}
}

func TestReadAPDFWithNoExtractorFallsToItsBytes(t *testing.T) {
	got := docread.New(nil).Read(context.Background(), "application/pdf", "report.pdf", []byte("%PDF-1.5 ..."), 1<<20)
	if got.Form != docread.FormOpaque {
		t.Fatalf("a PDF with no extractor did not fall to its bytes: %+v", got)
	}
	if got.Note == "" {
		t.Fatal("the reader did not say why the PDF was served as bytes")
	}
}

func TestReadAPDFThatFailsToParseFallsToItsBytes(t *testing.T) {
	r := docread.New(stubPDF{err: errors.New("damaged")})
	got := r.Read(context.Background(), "application/pdf", "report.pdf", []byte("%PDF-1.5 ..."), 1<<20)
	if got.Form != docread.FormOpaque || got.Note == "" {
		t.Fatalf("a damaged PDF did not fall to its bytes with a reason: %+v", got)
	}
}

// A scan is a PDF of pictures. It parses, yields nothing, and must not be
// reported as an empty document: its bytes are the only useful answer.
func TestReadAPDFWithNoTextFallsToItsBytes(t *testing.T) {
	r := docread.New(stubPDF{text: "   \n  "})
	got := r.Read(context.Background(), "application/pdf", "scan.pdf", []byte("%PDF-1.5 ..."), 1<<20)
	if got.Form != docread.FormOpaque || !strings.Contains(got.Note, "scanned") {
		t.Fatalf("a scan did not fall to its bytes with a reason: %+v", got)
	}
}

func TestReadARealPDFEndToEnd(t *testing.T) {
	body, err := os.ReadFile("testdata/objstream.pdf")
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	// The stub is not used here: the fixture proves the dispatch reaches an
	// extractor with the real bytes, and internal/pdftext proves the real
	// extractor reads them.
	r := docread.New(stubPDF{text: "Quarterly revenue summary for the northeast region"})
	got := r.Read(context.Background(), "", "report.pdf", body, 1<<20)
	if got.Form != docread.FormText || !strings.Contains(got.Text, "Quarterly") {
		t.Fatalf("the fixture did not read as text: %+v", got)
	}
}

// A PDF whose text fills the budget exactly must be reported as cut. An
// extractor stops once it has produced what it was asked for, so asking it for
// exactly the budget would make a document that fills it look like one that
// ends there, and the reader would act on a prefix believing it was the whole.
func TestReadAPDFThatFillsTheBudgetIsReportedAsTruncated(t *testing.T) {
	r := docread.New(stubPDF{text: strings.Repeat("p", 500)})
	got := r.Read(context.Background(), "application/pdf", "report.pdf", []byte("%PDF-1.5"), 100)
	if !got.Truncated {
		t.Fatalf("a PDF cut at the budget was not reported as truncated: %d bytes", len(got.Text))
	}
	if len(got.Text) != 100 {
		t.Fatalf("the budget was not applied: %d bytes", len(got.Text))
	}
}

// A PDF whose text ends exactly at the budget is NOT truncated: nothing was
// left out, and saying otherwise would send a reader after a rest that does
// not exist.
func TestReadAPDFThatEndsAtTheBudgetIsNotTruncated(t *testing.T) {
	r := docread.New(stubPDF{text: strings.Repeat("p", 100)})
	got := r.Read(context.Background(), "application/pdf", "report.pdf", []byte("%PDF-1.5"), 100)
	if got.Truncated {
		t.Fatalf("a PDF that ended at the budget was reported as truncated")
	}
	if len(got.Text) != 100 {
		t.Fatalf("text = %d bytes, want the whole document", len(got.Text))
	}
}

func TestReadTruncatesTextAtTheLimit(t *testing.T) {
	got := docread.New(nil).Read(context.Background(), "text/plain", "a.txt", []byte(strings.Repeat("x", 100)), 10)
	if !got.Truncated || len(got.Text) != 10 {
		t.Fatalf("the limit was not applied: %d bytes, truncated=%v", len(got.Text), got.Truncated)
	}
}

// A cut that lands inside a multi-byte rune must not leave the broken half
// behind: the text is handed to a model, and a lone continuation byte is not
// valid UTF-8 in a JSON string.
func TestReadTruncatesOnARuneBoundary(t *testing.T) {
	got := docread.New(nil).Read(context.Background(), "text/plain", "a.txt", []byte(strings.Repeat("é", 20)), 5)
	if !strings.HasSuffix(got.Text, "é") || len(got.Text) != 4 {
		t.Fatalf("the cut split a rune: %q (%d bytes)", got.Text, len(got.Text))
	}
}

// A caller that must pay a blob read before it can look at the bytes asks
// this first. It answers on the declaration alone, so it is generous where
// the declaration says nothing.
func TestMayHoldText(t *testing.T) {
	cases := map[string]bool{
		"text/markdown":            true,
		"image/svg+xml":            true, // an image family, but textual
		"application/pdf":          true,
		"application/zip":          true,
		"application/octet-stream": true, // says nothing; only the bytes can
		"":                         true,
		"image/png":                false,
		"image/jpeg":               false,
		"audio/mpeg":               false,
		"video/mp4":                false,
		"font/woff2":               false,
	}
	for ct, want := range cases {
		if got := docread.MayHoldText(ct); got != want {
			t.Errorf("MayHoldText(%q) = %v, want %v", ct, got, want)
		}
	}
}
