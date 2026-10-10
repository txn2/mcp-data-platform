package capacity

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/internal/platform/toolkitcfg"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// backendProbeTimeout bounds the one request that asks a self-hosted store
// which server it is.
const backendProbeTimeout = 5 * time.Second

// Endpoint is the endpoint of the S3 connection named conn in the toolkits
// configuration, or of the default connection when conn is empty. Empty when
// there is no such connection or it names no endpoint (AWS).
func Endpoint(toolkits map[string]any, conn string) (endpoint string, found bool) {
	cfg := toolkitcfg.S3Config(toolkits, conn)
	if cfg == nil {
		return "", false
	}
	return cfg.Endpoint, true
}

// DetectBackend names the object store an endpoint is: no endpoint is AWS S3,
// an amazonaws.com or googleapis.com host names itself, and anything else is
// asked once, by the Server header its root answers with (SeaweedFS sends
// one). A store that does not answer, or answers as something else, is other.
func DetectBackend(ctx context.Context, endpoint string) string {
	if endpoint == "" {
		return observability.StorageBackendS3
	}
	host := endpoint
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		host = u.Host
	}
	switch host = strings.ToLower(host); {
	case strings.Contains(host, "amazonaws.com"):
		return observability.StorageBackendS3
	case strings.Contains(host, "googleapis.com"):
		return observability.StorageBackendGCS
	}
	return backendFromServer(serverHeader(ctx, endpoint))
}

// backendFromServer reads a Server header.
func backendFromServer(server string) string {
	switch s := strings.ToLower(server); {
	case strings.Contains(s, "seaweedfs"):
		return observability.StorageBackendSeaweedFS
	default:
		return observability.StorageBackendOther
	}
}

// serverHeader is the Server header an unauthenticated GET of the endpoint's
// root answers with, or empty when it does not answer.
func serverHeader(ctx context.Context, endpoint string) string {
	ctx, cancel := context.WithTimeout(ctx, backendProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return ""
	}
	resp, err := outbound.NewClient(outbound.Options{Kind: outbound.KindStorage}).Do(req)
	if err != nil {
		return ""
	}
	_ = resp.Body.Close()
	return resp.Header.Get("Server")
}
