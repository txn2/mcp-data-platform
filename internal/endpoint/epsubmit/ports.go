package epsubmit

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/webhook/whtable"
)

// Querier runs one read statement on a Trino connection and returns its rows.
type Querier func(ctx context.Context, connection, sql string) ([]map[string]any, error)

// Targets is where a source's tables live on a connection (whtable.Tables).
type Targets interface {
	TargetFor(connection string) (whtable.Target, error)
}
