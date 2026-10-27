//go:build integration

package helpers

import (
	"os"
	"testing"
)

// EnvStandalone is set by `make e2e-standalone`, which runs this package
// with none of the e2e services up (#2342).
const EnvStandalone = "E2E_STANDALONE"

// NeedsServices skips t under `make e2e-standalone`: a test that reads the
// PostgreSQL, Trino or SeaweedFS `make e2e-up` starts calls it first. Every
// other test in the package runs in `make verify` and CI's test job, so a
// change that breaks one fails there rather than in the E2E Nightly.
func NeedsServices(t *testing.T) {
	t.Helper()
	if os.Getenv(EnvStandalone) == "1" {
		t.Skip("needs the e2e services (make e2e-up); the E2E Nightly's Real-dependency E2E job runs it")
	}
}
