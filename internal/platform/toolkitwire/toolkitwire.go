// Package toolkitwire hands the platform's own dependencies to the toolkits
// that take them, by what each toolkit implements rather than by its kind: the
// metrics recorder to every toolkit that records its own (S3, the api gateway,
// postgres), and the platform's database to the postgres toolkits, whose every
// connection checks it did not reach that database (#2400).
//
// It is a seam of pkg/platform, extracted when that package reached its size
// budget; the platform calls it with its registry's toolkits.
package toolkitwire

import (
	"database/sql"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// metricsAware is implemented by toolkits that record their own metrics.
type metricsAware interface {
	SetMetrics(*observability.Metrics)
}

// Metrics pushes m into every toolkit that records its own metrics. Each
// toolkit's SetMetrics decides what it installs for a disabled recorder, and
// is idempotent against the same recorder.
func Metrics(toolkits []registry.Toolkit, m *observability.Metrics) {
	for _, tk := range toolkits {
		if ma, ok := tk.(metricsAware); ok {
			ma.SetMetrics(m)
		}
	}
}

// PostgresGuard installs, on every postgres toolkit, the check each new
// database connection passes: that it did not reach the platform's database
// (postgreskit.OwnDatabaseGuard). The platform runs it before any tool is
// registered, so no statement runs on a connection the check has not seen. A
// deployment with no database has nothing to protect and installs nothing.
func PostgresGuard(toolkits []registry.Toolkit, platformDB *sql.DB) {
	if platformDB == nil {
		return
	}
	guard := postgreskit.OwnDatabaseGuard(platformDB)
	for _, tk := range toolkits {
		if pg, ok := tk.(*postgreskit.Toolkit); ok {
			pg.SetConnectGuard(guard)
		}
	}
}
