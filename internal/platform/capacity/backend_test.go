package capacity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func TestDetectBackend_ByHost(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, observability.StorageBackendS3, DetectBackend(ctx, ""))
	assert.Equal(t, observability.StorageBackendS3, DetectBackend(ctx, "https://s3.us-east-1.amazonaws.com"))
	assert.Equal(t, observability.StorageBackendGCS, DetectBackend(ctx, "https://storage.googleapis.com"))
}

func TestDetectBackend_ByServerHeader(t *testing.T) {
	for server, want := range map[string]string{
		"SeaweedFS 30GB 3.88": observability.StorageBackendSeaweedFS,
		"nginx":               observability.StorageBackendOther,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Server", server)
			w.WriteHeader(http.StatusForbidden)
		}))
		assert.Equal(t, want, DetectBackend(context.Background(), srv.URL), server)
		srv.Close()
	}
}

func TestDetectBackend_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	assert.Equal(t, observability.StorageBackendOther, DetectBackend(context.Background(), url))
	assert.Equal(t, observability.StorageBackendOther, DetectBackend(context.Background(), "::not a url"))
}

func TestEndpoint(t *testing.T) {
	toolkits := map[string]any{"s3": map[string]any{
		"default": "b",
		"instances": map[string]any{
			"a": map[string]any{"endpoint": "http://a:9000"},
			"b": map[string]any{"endpoint": "http://b:8333"},
		},
	}}
	got, ok := Endpoint(toolkits, "a")
	assert.True(t, ok)
	assert.Equal(t, "http://a:9000", got)
	got, ok = Endpoint(toolkits, "")
	assert.True(t, ok)
	assert.Equal(t, "http://b:8333", got, "an empty name is the default connection")
	_, ok = Endpoint(toolkits, "missing")
	assert.False(t, ok)
}
