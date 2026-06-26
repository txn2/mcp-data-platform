// Package connbackfill seeds connection_instances with a credential-free row for
// every file-configured connection, so knowledge-page references of the form
// mcp:connection:(kind,name) — which FK to connection_instances — resolve for
// connections defined only in platform.yaml. It lives outside pkg/platform so
// that package stays within its size budget, depending only on the toolkit
// enumeration (connview) and a raw database handle.
package connbackfill

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/txn2/mcp-data-platform/pkg/connview"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// Run inserts a (kind,name) row for every connection connview enumerates. It is
// insert-only — ON CONFLICT DO NOTHING never overwrites an admin-managed row's
// config, secrets, or created_by — and it writes no secrets: the file config
// remains the source of the real runtime configuration. The connection set is
// exactly what list_connections advertises (via connview), so every reference it
// emits resolves, including datahub, which the admin connection API does not
// manage. A nil db is a no-op (a stateless deployment has nothing to back the
// references). Per-connection errors are logged and never abort the sweep.
func Run(ctx context.Context, db *sql.DB, toolkits []registry.Toolkit) {
	if db == nil {
		return
	}
	for _, c := range connview.Build(ctx, toolkits, nil, nil).Connections {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO connection_instances (kind, name, description, created_by)
			 VALUES ($1, $2, $3, 'system') ON CONFLICT (kind, name) DO NOTHING`,
			c.Kind, c.Name, c.Description); err != nil {
			slog.WarnContext(ctx, "connection backfill failed", "kind", c.Kind, "name", c.Name, "error", err)
		}
	}
}
