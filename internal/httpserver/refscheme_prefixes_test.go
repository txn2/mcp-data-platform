package httpserver

import (
	"slices"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/endpoint/ephttp"
	"github.com/txn2/mcp-data-platform/internal/refscheme"
)

// The reference scheme's HTTP boundary rewrites the paths an endpoint is
// served under, so a reference a response gave out reaches the script
// canonical when a browser sends it back (#2788).
func TestTheReferenceSchemeCoversEveryEndpointPrefix(t *testing.T) {
	for _, prefix := range []string{ephttp.PathPrefix, ephttp.EnvironmentPrefix} {
		if !slices.Contains(refscheme.Prefixes, prefix) {
			t.Errorf("refscheme.Prefixes lacks %s, which serves endpoints", prefix)
		}
	}
}
