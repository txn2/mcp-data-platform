package catalogindex

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
)

// Structured-log keys for the best-effort hooks, matching the admin
// handler's spelling so catalog log lines stay greppable across
// packages.
const (
	logKeyCatalogID = "catalog_id"
	logKeySpecName  = "spec_name"
	logKeyError     = "error"
)

// EnqueueBestEffort is the producer-side hook every admin spec write
// path calls after the spec row commits. It records the job row and
// lets the worker / reconciler / reaper drive the actual embedding
// pass off the request path. Failures are logged but do not block the
// spec write: the reconciler picks up any spec whose embedding-row
// count is below operation_count on its next sweep, so a missed
// enqueue still converges. A nil store (file mode / no DB) is the
// documented degraded mode: the data path falls back to lexical.
func EnqueueBestEffort(ctx context.Context, s Store, catalogID, specName string) {
	if s == nil {
		return
	}
	if _, err := s.Enqueue(ctx, SpecKey{CatalogID: catalogID, SpecName: specName}, KindSpecWrite); err != nil {
		slog.Warn("apigateway: enqueue embedding job failed",
			logKeyCatalogID, logsan.SanitizeForLog(catalogID),
			logKeySpecName, logsan.SanitizeForLog(specName), logKeyError, err)
	}
}

// CancelBestEffort is the delete-side counterpart to
// EnqueueBestEffort: after a spec row is removed, its queued index
// jobs are dropped and its open failures resolved so the delete
// leaves no residue pinning the api_catalog index kind to Degraded
// (#998). Best-effort like the enqueue side: a missed cancel
// self-heals through the worker's source-gone path, and a lingering
// failure remains operator-dismissable, so the delete response does
// not depend on it.
func CancelBestEffort(ctx context.Context, s Store, catalogID, specName string) {
	if s == nil {
		return
	}
	if err := s.Cancel(ctx, SpecKey{CatalogID: catalogID, SpecName: specName}); err != nil {
		slog.Warn("apigateway: cancel embedding jobs failed",
			logKeyCatalogID, logsan.SanitizeForLog(catalogID),
			logKeySpecName, logsan.SanitizeForLog(specName), logKeyError, err)
	}
}

// CancelCatalogBestEffort is CancelBestEffort for a whole-catalog
// delete: every spec the cascade removed has its residue cleared by
// source_id prefix, with the same best-effort semantics.
func CancelCatalogBestEffort(ctx context.Context, s Store, catalogID string) {
	if s == nil {
		return
	}
	if err := s.CancelCatalog(ctx, catalogID); err != nil {
		slog.Warn("apigateway: cancel catalog embedding jobs failed",
			logKeyCatalogID, logsan.SanitizeForLog(catalogID), logKeyError, err)
	}
}
