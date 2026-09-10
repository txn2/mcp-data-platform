// Package pdftext draws the readable text out of a PDF.
//
// It exists because no pure-Go library reads the PDFs people actually upload.
// Both candidates were measured against real documents before this package was
// written, and both failed on the same 384 KB report: rsc.io/pdf panicked with
// "malformed PDF: reading at offset 0: stream not present", and
// github.com/dslipak/pdf spun at full CPU for over three minutes before it was
// killed. On the documents they did read, rsc.io/pdf emitted no spaces between
// text runs, which is not text a reader can use.
// github.com/pdfcpu/pdfcpu decodes content streams but extracts no text at
// all.
//
// What does work is PDFium, Chrome's PDF engine, compiled to WebAssembly and
// run under wazero. It reads the same file in milliseconds with word spacing
// and line breaks intact. It costs a one-time module compile at startup and
// about ten megabytes of binary, and it is cgo-free, so the static build is
// unaffected.
//
// # Lifetime
//
// The module compile costs about a second and happens once, on the first PDF
// a deployment is asked to read, not at startup: a deployment that never meets
// one never pays for it. Close releases the pool. A deployment builds one
// Extractor and shares it, because the compile is the expensive part and every
// extraction after it is cheap.
package pdftext

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	pdfium "github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/references"
	"github.com/klippa-app/go-pdfium/requests"
	"github.com/klippa-app/go-pdfium/webassembly"
)

// Pool sizing and timeouts.
const (
	// maxInstances bounds how many extractions run at once. Each instance is a
	// live WebAssembly linear memory holding a whole document, so this is a
	// memory ceiling as much as a concurrency one; extraction is milliseconds
	// per page, so a small pool queues rather than starves.
	maxInstances = 4
	// instanceWait bounds how long a caller queues for an instance before
	// giving up. A fetch that waited longer than this has already failed the
	// person who asked for it.
	instanceWait = 30 * time.Second
)

// ErrUnavailable reports that no extractor is usable: a nil one, or one that
// has been closed. A caller that never built an extractor takes the same path
// as one whose document could not be parsed.
var ErrUnavailable = errors.New("pdf text extraction is unavailable")

// Extractor reads text out of PDF documents. It is safe for concurrent use:
// each extraction takes an instance from the pool for its duration.
//
// The zero value is not usable; build one with New.
type Extractor struct {
	once sync.Once
	pool pdfium.Pool
	err  error

	mu     sync.Mutex
	closed bool
}

// New returns an Extractor. It does no work: the PDFium module is compiled on
// the first document, so a deployment that is never asked to read a PDF never
// pays the second it costs.
func New() *Extractor {
	return &Extractor{}
}

// ready compiles the module on first use and returns the pool. Every later
// call returns what the first produced, including its error: a compile that
// failed once fails the same way for the life of the process, and retrying it
// per document would turn one failure into a repeated second of work.
func (e *Extractor) ready() (pdfium.Pool, error) {
	e.once.Do(func() {
		size := min(max(runtime.GOMAXPROCS(0), 1), maxInstances)
		e.pool, e.err = webassembly.Init(webassembly.Config{
			MinIdle: 1, MaxIdle: size, MaxTotal: size,
		})
		if e.err != nil {
			e.err = fmt.Errorf("initializing the pdf text extractor: %w", e.err)
		}
	})
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, ErrUnavailable
	}
	return e.pool, e.err
}

// Close releases the pool and every instance in it. It is safe to call on an
// Extractor that never compiled its module, and safe to call twice.
func (e *Extractor) Close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.pool == nil {
		e.closed = true
		return nil
	}
	e.closed = true
	if err := e.pool.Close(); err != nil {
		return fmt.Errorf("closing the pdf text extractor: %w", err)
	}
	return nil
}

// ExtractText returns the document's text, page by page in document order,
// stopping once it has produced limit bytes. Pages are separated by a blank
// line, which is what keeps the last line of one page from running into the
// first line of the next.
//
// An error means the bytes could not be read as a PDF. A PDF that parses but
// holds no text -- a scan, a deck of images -- is not an error: it returns the
// empty string, and the caller decides what an empty document means.
func (e *Extractor) ExtractText(ctx context.Context, body []byte, limit int) (string, error) {
	if e == nil {
		return "", ErrUnavailable
	}
	if limit <= 0 {
		return "", nil
	}
	pool, err := e.ready()
	if err != nil {
		return "", err
	}

	inst, err := e.instance(ctx, pool)
	if err != nil {
		return "", err
	}
	defer func() { _ = inst.Close() }()

	doc, err := inst.OpenDocument(&requests.OpenDocument{File: &body})
	if err != nil {
		return "", fmt.Errorf("opening the pdf: %w", err)
	}
	defer func() {
		_, _ = inst.FPDF_CloseDocument(&requests.FPDF_CloseDocument{Document: doc.Document})
	}()

	count, err := inst.FPDF_GetPageCount(&requests.FPDF_GetPageCount{Document: doc.Document})
	if err != nil {
		return "", fmt.Errorf("counting the pdf's pages: %w", err)
	}
	return readPages(ctx, inst, doc.Document, count.PageCount, limit), nil
}

// instance takes one pooled instance, bounded by instanceWait and by the
// caller's own context, whichever ends first.
func (*Extractor) instance(ctx context.Context, pool pdfium.Pool) (pdfium.Pdfium, error) {
	ctx, cancel := context.WithTimeout(ctx, instanceWait)
	defer cancel()
	inst, err := pool.GetInstanceWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("waiting for a pdf extractor instance: %w", err)
	}
	return inst, nil
}

// readPages accumulates page text up to limit. A page whose text cannot be
// read is skipped rather than failing the document: one damaged page in a
// hundred is not a reason to return nothing.
func readPages(ctx context.Context, inst pdfium.Pdfium, doc references.FPDF_DOCUMENT, pages, limit int) string {
	var out strings.Builder
	for i := range pages {
		if ctx.Err() != nil || out.Len() >= limit {
			break
		}
		page, err := inst.GetPageText(&requests.GetPageText{
			Page: requests.Page{ByIndex: &requests.PageByIndex{Document: doc, Index: i}},
		})
		if err != nil || page.Text == "" {
			continue
		}
		if out.Len() > 0 {
			// strings.Builder never fails; discarded here so the loop reads as
			// the accumulation it is.
			_, _ = out.WriteString("\n\n")
		}
		_, _ = out.WriteString(page.Text)
	}
	return out.String()
}
