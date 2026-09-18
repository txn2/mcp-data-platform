package platform

import (
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// TestBlobReadCeiling. The read deadline on both S3 clients is sized from the
// largest object the platform reads in one piece, and that is one number
// across the two surfaces: a table registration reads a managed resource and a
// portal asset through their separate clients and caps both at the
// managed-resource ceiling (#1634, #1773).
func TestBlobReadCeiling(t *testing.T) {
	const mb = 1 << 20

	if got := blobReadCeiling(&Config{}); got != resource.MaxUploadBytes {
		t.Errorf("a deployment configuring neither gets the resource default: got %d", got)
	}

	cfg := &Config{}
	cfg.Resources.Managed.MaxUploadBytes = 250 * mb
	cfg.Portal.MaxContentSize = 10 * mb
	if got := blobReadCeiling(cfg); got != 250*mb {
		t.Errorf("the resources ceiling is the larger: got %d", got)
	}

	cfg.Portal.MaxContentSize = 400 * mb
	if got := blobReadCeiling(cfg); got != 400*mb {
		t.Errorf("and the asset ceiling when THAT is the larger: got %d", got)
	}
}
