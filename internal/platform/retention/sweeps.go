package retention

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/portalpurge"
)

// Config is what Assemble builds the sweeps from. Each *Days is already
// resolved by Days: 0 turns its sweep off.
type Config struct {
	DB *sql.DB
	// Objects deletes the portal's stored objects; nil deletes rows only.
	Objects portalpurge.ObjectDeleter
	// Bucket is the portal bucket, where collection mosaics are stored.
	Bucket string
	// Portal is false where the portal is off, which purges nothing of it.
	Portal                                  bool
	DeletedDays, ArchivedDays, ProducerDays int
	Every                                   time.Duration
}

// Assemble builds the loop over every sweep cfg turns on. It is nil without
// a database.
func Assemble(cfg Config) *Loop {
	if cfg.DB == nil {
		return nil
	}
	var sweeps []Sweep
	if cfg.Portal {
		sweeps = append(sweeps, PortalDeleted(portalpurge.NewPurger(cfg.DB, cfg.Objects, cfg.Bucket), cfg.DeletedDays)...)
	}
	sweeps = append(sweeps, MemoryArchived(cfg.DB, cfg.ArchivedDays)...)
	sweeps = append(sweeps, OrphanedProducers(cfg.DB, cfg.ProducerDays)...)
	sweeps = append(sweeps, SupersededGraphQL(cfg.DB)...)
	return New(cfg.DB, cfg.Every, sweeps...)
}

// Advisory lock keys, one per sweep. They sit beside the call catalog's
// (4713210001) and are distinct from it and from each other.
const (
	lockPortalDeleted     int64 = 4713210101
	lockMemoryArchived    int64 = 4713210102
	lockOrphanedProducers int64 = 4713210103
	lockGraphQLSuperseded int64 = 4713210104
)

// PortalDeleted is the sweep that purges portal items deleted more than days
// ago, with their objects. Nothing when days is 0.
func PortalDeleted(p *portalpurge.Purger, days int) []Sweep {
	if p == nil || days <= 0 {
		return nil
	}
	return []Sweep{{Name: "portal_deleted", LockKey: lockPortalDeleted, Run: func(ctx context.Context) (int64, error) {
		res, err := p.Purge(ctx, cutoff(days))
		return int64(res.Total()), err
	}}}
}

// archivedMemoryQuery deletes memory records archived before $1. Recall
// never reads an archived record; it is kept only so a deletion can be
// examined for the retention window.
const archivedMemoryQuery = `DELETE FROM memory_records WHERE status = 'archived' AND updated_at < $1`

// MemoryArchived is the sweep that deletes memory records archived more than
// days ago. Nothing when days is 0.
func MemoryArchived(db *sql.DB, days int) []Sweep {
	if days <= 0 {
		return nil
	}
	return []Sweep{{Name: "memory_archived", LockKey: lockMemoryArchived, Run: exec(db, archivedMemoryQuery, days)}}
}

// orphanedProducersQuery deletes producer rows whose asset or resource no
// longer exists and that were last written before $1. content_producers holds
// no foreign key, because a target is one of two tables; a resource delete
// removes the resource row only, so its producers outlive it. The age keeps a
// row written a moment before its target's row is from being taken for an
// orphan.
const orphanedProducersQuery = `
	DELETE FROM content_producers cp
	 WHERE cp.last_write_at < $1
	   AND ((cp.target_kind = 'asset' AND NOT EXISTS (SELECT 1 FROM portal_assets a WHERE a.id = cp.target_id))
	     OR (cp.target_kind = 'resource' AND NOT EXISTS (SELECT 1 FROM resources r WHERE r.id = cp.target_id)))`

// OrphanedProducers is the sweep that deletes producer rows whose file is
// gone. Nothing when days is 0.
func OrphanedProducers(db *sql.DB, days int) []Sweep {
	if days <= 0 {
		return nil
	}
	return []Sweep{{Name: "orphaned_producers", LockKey: lockOrphanedProducers, Run: exec(db, orphanedProducersQuery, days)}}
}

// supersededGraphQLQuery deletes the operation embeddings of every schema a
// connection no longer holds. graphql_connection_schemas keeps one row per
// connection, its current schema, and nothing reads another hash's vectors;
// the embeddings were keyed by hash, so each schema change left the previous
// schema's behind. A connection with no stored schema row is not touched.
const supersededGraphQLQuery = `
	DELETE FROM graphql_operation_embeddings e
	 USING graphql_connection_schemas s
	 WHERE s.connection = e.connection AND s.schema_hash <> e.schema_hash`

// SupersededGraphQL is the sweep that deletes embeddings of superseded
// GraphQL schemas. It takes no age: a superseded schema's vectors are never
// read again.
func SupersededGraphQL(db *sql.DB) []Sweep {
	return []Sweep{{Name: "graphql_superseded_embeddings", LockKey: lockGraphQLSuperseded, Run: func(ctx context.Context) (int64, error) {
		return execQuery(ctx, db, supersededGraphQLQuery)
	}}}
}

// exec runs a delete taking the retention cutoff as its only argument.
func exec(db *sql.DB, query string, days int) func(context.Context) (int64, error) {
	return func(ctx context.Context) (int64, error) {
		return execQuery(ctx, db, query, cutoff(days))
	}
}

func execQuery(ctx context.Context, db *sql.DB, query string, args ...any) (int64, error) {
	res, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("retention delete: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// cutoff is the instant days before now.
func cutoff(days int) time.Time {
	return time.Now().UTC().AddDate(0, 0, -days)
}
