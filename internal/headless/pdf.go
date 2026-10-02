package headless

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
)

const (
	// MaxPDFBytes bounds a printed document. A PDF is read from the renderer
	// in chunks rather than in one protocol message, so this, not the
	// message cap, is what limits it.
	MaxPDFBytes = 256 << 20
	// pdfChunk is how much of the printed document one read asks for.
	pdfChunk = 4 << 20
)

// pdfCap is the cap readStream holds a printed document to: MaxPDFBytes, and
// a smaller one in a test that has to reach it.
var pdfCap = MaxPDFBytes

// ErrPDFTooLarge is a printed document past MaxPDFBytes.
var ErrPDFTooLarge = errors.New("headless: the printed document is larger than the platform returns")

// printPDF prints the page with its backgrounds, at the page size its @page
// rule declares and with no margins of the browser's own, and reads the
// result back as a stream.
func (rs *render) printPDF(ctx context.Context, session string) ([]byte, error) {
	var res struct {
		Stream string `json:"stream"`
	}
	if err := rs.c.call(ctx, session, "Page.printToPDF", map[string]any{
		"printBackground":   true,
		"preferCSSPageSize": true,
		"marginTop":         0,
		"marginBottom":      0,
		"marginLeft":        0,
		"marginRight":       0,
		"transferMode":      "ReturnAsStream",
	}, &res); err != nil {
		return nil, err
	}
	if res.Stream == "" {
		return nil, errors.New("headless: the renderer returned no printed document")
	}
	// The stream belongs to the page's session, and only that session can
	// read or close it.
	defer func() {
		_ = rs.c.call(context.WithoutCancel(ctx), session, "IO.close", map[string]any{"handle": res.Stream}, nil) //nolint:errcheck // the context's disposal releases it too
	}()
	return rs.readStream(ctx, session, res.Stream)
}

// readStream reads an IO stream to its end.
func (rs *render) readStream(ctx context.Context, session, handle string) ([]byte, error) {
	var out []byte
	for {
		var chunk struct {
			Data          string `json:"data"`
			Base64Encoded bool   `json:"base64Encoded"`
			EOF           bool   `json:"eof"`
		}
		if err := rs.c.call(ctx, session, "IO.read", map[string]any{"handle": handle, "size": pdfChunk}, &chunk); err != nil {
			return nil, err
		}
		data := []byte(chunk.Data)
		if chunk.Base64Encoded {
			decoded, err := base64.StdEncoding.DecodeString(chunk.Data)
			if err != nil {
				return nil, fmt.Errorf("headless: decoding the printed document: %w", err)
			}
			data = decoded
		}
		if len(out)+len(data) > pdfCap {
			return nil, ErrPDFTooLarge
		}
		out = append(out, data...)
		if chunk.EOF {
			break
		}
	}
	if len(out) == 0 {
		return nil, errors.New("headless: the renderer returned an empty printed document")
	}
	return out, nil
}
