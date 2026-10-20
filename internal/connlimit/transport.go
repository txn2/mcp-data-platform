package connlimit

import (
	"fmt"
	"io"
	"net/http"
)

// Transport is base with every request taking one of the connection's limit
// slots before it is sent and giving it back once its response body is read
// to the end or closed, which is when the upstream is done with the call. A
// limit of zero or less is base unchanged: an unlimited connection, the
// default.
func Transport(base http.RoundTripper, kind, connection string, limit int) http.RoundTripper {
	if limit <= 0 {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &limited{base: base, kind: kind, connection: connection, limit: limit}
}

// limited is the transport Transport builds.
type limited struct {
	base       http.RoundTripper
	kind       string
	connection string
	limit      int
}

// RoundTrip waits for a slot, under the request's own deadline, then sends.
func (t *limited) RoundTrip(req *http.Request) (*http.Response, error) {
	slot, _, err := Default().Acquire(req.Context(), t.kind, t.connection, t.limit)
	if err != nil {
		return nil, err
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		slot.Release()
		return nil, err //nolint:wrapcheck // the base transport's own failure, as the client reports it
	}
	if resp.Body == nil || resp.Body == http.NoBody {
		slot.Release()
		return resp, nil
	}
	resp.Body = &releasingBody{ReadCloser: resp.Body, slot: slot}
	return resp, nil
}

// releasingBody gives the slot back when the body is read to its end or
// closed, whichever comes first; the slot gives itself back once.
type releasingBody struct {
	io.ReadCloser
	slot *Slot
}

// Read reads the body, giving the slot back at its end.
func (b *releasingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err == io.EOF {
		b.release()
	}
	return n, err //nolint:wrapcheck // the body's own read, passed through
}

// Close closes the body and gives the slot back.
func (b *releasingBody) Close() error {
	err := b.ReadCloser.Close()
	b.release()
	return err //nolint:wrapcheck // as above
}

func (b *releasingBody) release() { b.slot.Release() }

// Ceiling bounds a connection's max_concurrent_calls. Each call holding a
// slot holds a database connection for its length, so the limit is for an
// upstream that takes a few calls at once, not a throughput setting.
const Ceiling = 32

// Parse reads a configured max_concurrent_calls, absent or nil being
// unlimited. A value that is not a whole number is refused rather than read
// as unlimited: a limit an operator set and the platform silently ignored is
// an upstream flooded by the calls the limit was set to hold back.
func Parse(raw any) (int, error) {
	switch v := raw.(type) {
	case nil:
		return 0, nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		if v == float64(int64(v)) {
			return int(v), nil
		}
	}
	return 0, fmt.Errorf("max_concurrent_calls must be a whole number, got %v", raw)
}
