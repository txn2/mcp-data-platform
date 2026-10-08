package secretref

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// closeCounter is a base transport that counts CloseIdleConnections, which
// a session-login connection signs out through.
type closeCounter struct {
	http.RoundTripper
	closed int
}

func (c *closeCounter) CloseIdleConnections() { c.closed++ }

// TestTransportRedactsWhatComesBack: a response to a request whose context
// recorded a value comes back with it redacted from headers and body, and a
// request that recorded none is passed through as sent.
func TestTransportRedactsWhatComesBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Echo", "tok-123456")
		_, _ = io.WriteString(w, `{"got":"tok-123456"}`)
	}))
	t.Cleanup(srv.Close)
	base := &closeCounter{RoundTripper: http.DefaultTransport}
	client := &http.Client{Transport: Transport(base)}

	type answer struct {
		echo   string
		length int64
		body   string
	}
	get := func(ctx context.Context) answer {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return answer{echo: resp.Header.Get("X-Echo"), length: resp.ContentLength, body: string(body)}
	}

	ctx, r := WithRedactor(context.Background())
	r.Add("token", "tok-123456")
	if a := get(ctx); a.body != `{"got":"[REDACTED:token]"}` || a.echo != "[REDACTED:token]" || a.length != -1 {
		t.Errorf("redacted response = %+v", a)
	}
	if a := get(context.Background()); a.body != `{"got":"tok-123456"}` {
		t.Errorf("a request with no secret was changed: %+v", a)
	}

	client.CloseIdleConnections()
	if base.closed != 1 {
		t.Error("CloseIdleConnections did not reach the base transport")
	}
	if rt, ok := Transport(nil).(redactingTransport); !ok || rt.Unwrap() != http.DefaultTransport {
		t.Error("a nil base is not the default transport")
	}
	if rt, ok := Transport(nopTransport{}).(redactingTransport); ok {
		rt.CloseIdleConnections() // a base with no idle connections to close
	}
}

// nopTransport has no CloseIdleConnections.
type nopTransport struct{}

func (nopTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, http.ErrNotSupported
}
