package headless

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// streamOf answers IO.read with the document in chunks of size, base64
// encoded, so a test can hold the reader to the whole stream.
func streamOf(doc []byte, size int) func(call) answer {
	at := 0
	return func(call) answer {
		end := min(at+size, len(doc))
		chunk := doc[at:end]
		at = end
		return ok(map[string]any{
			"data": base64.StdEncoding.EncodeToString(chunk), "base64Encoded": true, "eof": at >= len(doc),
		})
	}
}

// PrintPDF prints with the document's backgrounds at its own page size, and
// reads the printed document back as a stream to its end (#1983).
func TestPrintPDF_PrintsBackgroundsAndReadsTheWholeStream(t *testing.T) {
	doc := []byte("%PDF-1.7 " + strings.Repeat("x", 100))
	fb := newFakeBrowser(t, happy(map[string]func(call) answer{
		"Page.printToPDF": func(call) answer { return ok(map[string]any{"stream": "s1"}) },
		"IO.read":         streamOf(doc, 30),
	}))
	got, err := New(fb.endpoint(), nil).PrintPDF(context.Background(), tile())
	if err != nil {
		t.Fatalf("PrintPDF: %v", err)
	}
	if !bytes.Equal(got, doc) {
		t.Fatalf("read %q, want the whole stream", got)
	}
	calls := fb.recorded()
	p := find(t, calls, "Page.printToPDF", "page1").Params
	if bg, _ := p["printBackground"].(bool); !bg || p["preferCSSPageSize"] != any(true) || p["transferMode"] != "ReturnAsStream" {
		t.Errorf("printToPDF params = %v", p)
	}
	if indexOf(calls, "Page.captureScreenshot", "page1") >= 0 {
		t.Error("a PDF render took a screenshot")
	}
	// The stream is the page's: the browser session cannot read it.
	find(t, calls, "IO.read", "page1")
	if c := find(t, calls, "IO.close", "page1"); c.Params["handle"] != "s1" {
		t.Errorf("closed %v, want the stream", c.Params)
	}
	find(t, calls, "Target.disposeBrowserContext", "")
}

func TestPrintPDF_Failures(t *testing.T) {
	cases := map[string]struct {
		overrides map[string]func(call) answer
		want      string
	}{
		"no stream": {map[string]func(call) answer{
			"Page.printToPDF": func(call) answer { return ok(map[string]any{}) },
		}, "no printed document"},
		"refused": {map[string]func(call) answer{
			"Page.printToPDF": func(call) answer { return refused("Printing failed") },
		}, "Printing failed"},
		"empty": {map[string]func(call) answer{
			"Page.printToPDF": func(call) answer { return ok(map[string]any{"stream": "s"}) },
			"IO.read":         func(call) answer { return ok(map[string]any{"data": "", "eof": true}) },
		}, "empty printed document"},
		"bad base64": {map[string]func(call) answer{
			"Page.printToPDF": func(call) answer { return ok(map[string]any{"stream": "s"}) },
			"IO.read":         func(call) answer { return ok(map[string]any{"data": "!!", "base64Encoded": true, "eof": true}) },
		}, "decoding"},
		"read refused": {map[string]func(call) answer{
			"Page.printToPDF": func(call) answer { return ok(map[string]any{"stream": "s"}) },
			"IO.read":         func(call) answer { return refused("bad handle") },
		}, "bad handle"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fb := newFakeBrowser(t, happy(tc.overrides))
			_, err := New(fb.endpoint(), nil).PrintPDF(context.Background(), tile())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// A stream that does not end before MaxPDFBytes is refused rather than read
// into memory without bound.
func TestPrintPDF_RefusesADocumentPastTheCap(t *testing.T) {
	saved := pdfCap
	pdfCap = 1 << 10
	t.Cleanup(func() { pdfCap = saved })
	big := base64.StdEncoding.EncodeToString(make([]byte, 400))
	fb := newFakeBrowser(t, happy(map[string]func(call) answer{
		"Page.printToPDF": func(call) answer { return ok(map[string]any{"stream": "s"}) },
		"IO.read": func(call) answer {
			return ok(map[string]any{"data": big, "base64Encoded": true, "eof": false})
		},
	}))
	_, err := New(fb.endpoint(), nil).PrintPDF(context.Background(), tile())
	if !errors.Is(err, ErrPDFTooLarge) {
		t.Fatalf("err = %v, want ErrPDFTooLarge", err)
	}
}

// A plain-text chunk is read as it is.
func TestPrintPDF_ReadsAPlainChunk(t *testing.T) {
	fb := newFakeBrowser(t, happy(map[string]func(call) answer{
		"Page.printToPDF": func(call) answer { return ok(map[string]any{"stream": "s"}) },
		"IO.read":         func(call) answer { return ok(map[string]any{"data": "%PDF", "eof": true}) },
	}))
	got, err := New(fb.endpoint(), nil).PrintPDF(context.Background(), tile())
	if err != nil || string(got) != "%PDF" {
		t.Fatalf("got %q, %v", got, err)
	}
}
