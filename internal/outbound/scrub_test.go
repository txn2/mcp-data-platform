package outbound

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/secretref"
)

func TestScrubURL(t *testing.T) {
	ctx, r := secretref.WithRedactor(context.Background())
	r.Add("path-secret", "p4th-s3cret")
	u, err := url.Parse("https://user:pw@api.example.com/v1/p4th-s3cret/items?page=2&api_key=k3y")
	require.NoError(t, err)
	got := ScrubURL(ctx, u)
	assert.Equal(t, "https://api.example.com/v1/"+secretref.Redaction("path-secret")+"/items?api_key=REDACTED&page=REDACTED", got)
	assert.Empty(t, ScrubURL(ctx, nil))
	plain, _ := url.Parse("https://api.example.com/v1")
	assert.Equal(t, "https://api.example.com/v1", ScrubURL(context.Background(), plain))
}

// A failed request through a client the chain built names no credential its
// URL carried, in the error http.Client.Do returns or in the span (#2358).
func TestAFailedRequestShowsNoURLCredential(t *testing.T) {
	sr := recordingTracer(t)
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	client := NewClient(Options{Kind: KindAPI, Connection: "c"})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/x?api_key=k3y-2358", http.NoBody)
	require.NoError(t, err)
	resp, err := client.Do(req) // #nosec G704 -- a closed local port this test chose
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	err = ScrubError(req.Context(), err)
	assert.NotContains(t, err.Error(), "k3y-2358")
	var ue *url.Error
	require.ErrorAs(t, err, &ue, "the error keeps its type")

	spans := sr.Ended()
	require.NotEmpty(t, spans)
	for _, s := range spans {
		for _, a := range s.Attributes() {
			assert.NotContains(t, a.Value.String(), "k3y-2358", "span attribute %s", a.Key)
		}
	}
}

func TestScrubErrorLeavesOtherErrors(t *testing.T) {
	plain := errors.New("no URL here")
	assert.Same(t, plain, ScrubError(context.Background(), plain))
	assert.NoError(t, ScrubError(context.Background(), nil))
	bad := fmt.Errorf("wrapped: %w", &url.Error{Op: "Get", URL: "http://[::1", Err: errors.New("x")})
	assert.Contains(t, ScrubError(context.Background(), bad).Error(), "[unparsed URL]")
}
