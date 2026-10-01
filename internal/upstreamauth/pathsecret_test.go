package upstreamauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPathSecretIsSentAndNeverQuoted sends a request through a connection
// with a path secret: the receiver sees the secret on the path, the caller's
// request keeps the path without it, and a failed dial's error does not
// quote it.
func TestPathSecretIsSentAndNeverQuoted(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c, err := Parse("api", "apigateway", srv.URL, map[string]any{"path_secret": "/T000/B000/XXXX/"})
	require.NoError(t, err)
	require.NoError(t, c.Validate())
	client := NewHTTPClient(c)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/hooks/orders/", strings.NewReader("{}"))
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, "/hooks/orders/T000/B000/XXXX", got)
	assert.Equal(t, "/hooks/orders/", req.URL.Path, "the caller's request is not changed")

	dead, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://127.0.0.1:1/hooks", strings.NewReader("{}"))
	deadResp, err := client.Do(dead)
	if deadResp != nil {
		_ = deadResp.Body.Close()
	}
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "XXXX", "an error never quotes the secret")

	for _, bad := range []string{"a?b", "a#b", "a b", "a\nb"} {
		c.PathSecret = bad
		assert.Error(t, c.Validate(), bad)
	}
}
