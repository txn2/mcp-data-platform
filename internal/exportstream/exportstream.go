// Package exportstream is the plumbing an export streams a body through
// without holding it whole: a JSON array written item by item into a pipe a
// storage upload reads from, and a reader that bounds that upload. Extracted
// from pkg/toolkits/apigateway, whose api_export page walk is its first user,
// for that package's size budget (#2057).
package exportstream

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrConsumerStopped marks a write that failed because the reader of the
// stream went away (the storage upload failed or was capped). The reader's
// own error is the one to report; this one says the writer is not the cause.
var ErrConsumerStopped = errors.New("walk output consumer stopped")

// JSONArrayWriter streams one JSON array: it opens the array on the first
// write, separates items with commas, and closes it on Close, so memory holds
// one batch of items at a time however many there are.
type JSONArrayWriter struct {
	w      io.Writer
	opened bool
	count  int
}

// NewJSONArrayWriter writes the array to w.
func NewJSONArrayWriter(w io.Writer) *JSONArrayWriter {
	return &JSONArrayWriter{w: w}
}

// Write appends items to the array.
func (a *JSONArrayWriter) Write(items []json.RawMessage) error {
	if err := a.open(); err != nil {
		return err
	}
	for _, it := range items {
		if a.count > 0 {
			if _, err := io.WriteString(a.w, ","); err != nil {
				return consumerError(err)
			}
		}
		if _, err := a.w.Write(it); err != nil {
			return consumerError(err)
		}
		a.count++
	}
	return nil
}

func (a *JSONArrayWriter) open() error {
	if a.opened {
		return nil
	}
	a.opened = true
	if _, err := io.WriteString(a.w, "["); err != nil {
		return consumerError(err)
	}
	return nil
}

// Close finishes the array. A writer that was handed nothing still writes an
// empty array.
func (a *JSONArrayWriter) Close() error {
	if err := a.open(); err != nil {
		return err
	}
	if _, err := io.WriteString(a.w, "]"); err != nil {
		return consumerError(err)
	}
	return nil
}

// consumerError classifies a write failure on the stream. A closed pipe means
// the reader stopped first, and its error is the one to report.
func consumerError(err error) error {
	if errors.Is(err, io.ErrClosedPipe) {
		return ErrConsumerStopped
	}
	return fmt.Errorf("writing merged page: %w", err)
}

// CappedReader bounds a stream at a byte count. Once more than that has been
// read it returns an error (so the S3 transfer manager aborts the incomplete
// multipart upload) and Exceeded reports true, which a caller checks to tell
// an over-cap body from a transient storage error. A cap <= 0 disables it.
// The over-cap error text is internal; the caller substitutes the sentence
// its own caller reads.
type CappedReader struct {
	r        io.Reader
	limit    int64
	n        int64
	exceeded bool
}

// NewCappedReader bounds r at limit bytes.
func NewCappedReader(r io.Reader, limit int64) *CappedReader {
	return &CappedReader{r: r, limit: limit}
}

func (c *CappedReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.limit > 0 && c.n > c.limit {
		c.exceeded = true
		return n, fmt.Errorf("export body exceeded cap of %d bytes", c.limit)
	}
	return n, err //nolint:wrapcheck // transparent pass-through of the wrapped reader's error
}

// Exceeded reports whether the stream ran past the cap.
func (c *CappedReader) Exceeded() bool { return c.exceeded }
