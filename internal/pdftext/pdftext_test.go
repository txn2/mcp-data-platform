package pdftext_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/pdftext"
)

// fixture is a PDF 1.5 built with a cross-reference stream and an object
// stream -- the layout of a document produced by an ordinary word processor or
// a browser's print-to-PDF -- carrying one line of known text.
const fixture = "../docread/testdata/objstream.pdf"

const fixtureText = "Quarterly revenue summary for the northeast region"

// twoPageFixture is the same layout over two pages, which is what makes the
// page loop's join and its budget break reachable.
const twoPageFixture = "../docread/testdata/twopage.pdf"

// newExtractor builds an extractor and closes it when the case ends.
func newExtractor(t *testing.T) *pdftext.Extractor {
	t.Helper()
	e := pdftext.New()
	t.Cleanup(func() {
		if closeErr := e.Close(); closeErr != nil {
			t.Errorf("closing the extractor: %v", closeErr)
		}
	})
	return e
}

func TestExtractTextReadsAModernPDF(t *testing.T) {
	body, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	got, err := newExtractor(t).ExtractText(context.Background(), body, 1<<20)
	if err != nil {
		t.Fatalf("extracting: %v", err)
	}
	if !strings.Contains(got, fixtureText) {
		t.Fatalf("the document's text was not returned: %q", got)
	}
}

// Pages are joined by a blank line, or the last line of one page runs into the
// first line of the next and the two read as one sentence.
func TestExtractTextJoinsPages(t *testing.T) {
	body, err := os.ReadFile(twoPageFixture)
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	got, err := newExtractor(t).ExtractText(context.Background(), body, 1<<20)
	if err != nil {
		t.Fatalf("extracting: %v", err)
	}
	if !strings.Contains(got, "First page") || !strings.Contains(got, "Second page") {
		t.Fatalf("both pages did not come back: %q", got)
	}
	if !strings.Contains(got, "\n\n") {
		t.Fatalf("the pages were not separated: %q", got)
	}
}

// The bound is per page: a page is read whole and no further page is started
// once the budget is spent, so a two-page document under a one-byte budget
// returns its first page and stops.
func TestExtractTextStopsAtTheLimit(t *testing.T) {
	body, err := os.ReadFile(twoPageFixture)
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	got, err := newExtractor(t).ExtractText(context.Background(), body, 1)
	if err != nil {
		t.Fatalf("extracting: %v", err)
	}
	if strings.Contains(got, "Second page") {
		t.Fatalf("the limit did not stop the read after the first page: %q", got)
	}
	if !strings.Contains(got, "First page") {
		t.Fatalf("the first page was not read: %q", got)
	}
}

// A caller whose context is already done gets what was read so far and no
// further page, rather than a document read to the end regardless.
func TestExtractTextStopsOnACanceledContext(t *testing.T) {
	body, err := os.ReadFile(twoPageFixture)
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := newExtractor(t).ExtractText(ctx, body, 1<<20)
	if err != nil {
		// Taking an instance may itself fail on a canceled context, which is
		// the same refusal from one step earlier.
		return
	}
	if strings.Contains(got, "Second page") {
		t.Fatalf("a canceled context read on past the first page: %q", got)
	}
}

func TestExtractTextRejectsBytesThatAreNotAPDF(t *testing.T) {
	if _, err := newExtractor(t).ExtractText(context.Background(), []byte("not a pdf at all"), 1<<20); err == nil {
		t.Fatal("bytes that are not a PDF were accepted")
	}
}

func TestExtractTextOnANilExtractorIsUnavailableRatherThanAPanic(t *testing.T) {
	var e *pdftext.Extractor
	_, err := e.ExtractText(context.Background(), []byte("%PDF-1.5"), 1<<20)
	if !errors.Is(err, pdftext.ErrUnavailable) {
		t.Fatalf("a nil extractor returned %v", err)
	}
	if closeErr := e.Close(); closeErr != nil {
		t.Fatalf("closing a nil extractor: %v", closeErr)
	}
}

// Closing an extractor that never read a document must not compile a module
// just to tear it down, and closing twice must not fail.
func TestCloseAnUnusedExtractor(t *testing.T) {
	e := pdftext.New()
	if err := e.Close(); err != nil {
		t.Fatalf("closing an unused extractor: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("closing twice: %v", err)
	}
	if _, err := e.ExtractText(context.Background(), []byte("%PDF-1.5"), 1<<20); !errors.Is(err, pdftext.ErrUnavailable) {
		t.Fatalf("a closed extractor returned %v", err)
	}
}

func TestExtractTextWithNoBudgetReturnsNothing(t *testing.T) {
	got, err := newExtractor(t).ExtractText(context.Background(), []byte("%PDF-1.5"), 0)
	if err != nil || got != "" {
		t.Fatalf("a zero budget produced %q, %v", got, err)
	}
}
