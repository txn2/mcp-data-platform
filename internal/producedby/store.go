package producedby

import (
	"context"
	"database/sql"
	"fmt"
)

// recordStmt folds one write into the producer's row for this target.
//
// created is OR-ed rather than assigned: the producer that created a file and
// modified it later is still its creator, and a modification must never demote
// it. The label is refreshed on every write so a renamed script displays under
// its current name, but an empty one never overwrites a label already there --
// a session carries none, and losing a script's name to a write that did not
// know it would leave a deleted script unnameable.
//
// The count is a bind rather than a literal 1 because one write is sometimes
// recorded twice: an asset's create and its version 1 are the two halves of one
// save, and the second advances the version without counting again.
const recordStmt = `
	INSERT INTO content_producers
		(target_kind, target_id, producer_kind, producer_id, producer_label,
		 created, first_write_at, last_write_at, write_count, last_version)
	VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW(), $7, $8)
	ON CONFLICT (target_kind, target_id, producer_kind, producer_id) DO UPDATE SET
		producer_label = COALESCE(NULLIF(EXCLUDED.producer_label, ''), content_producers.producer_label),
		created        = content_producers.created OR EXCLUDED.created,
		last_write_at  = NOW(),
		write_count    = content_producers.write_count + EXCLUDED.write_count,
		last_version   = GREATEST(content_producers.last_version, EXCLUDED.last_version)`

// producerColumns is the projection both listings read, in scan order.
const producerColumns = `target_kind, target_id, producer_kind, producer_id, producer_label,
	created, first_write_at, last_write_at, write_count, last_version`

const listByTargetStmt = `
	SELECT ` + producerColumns + `
	FROM content_producers
	WHERE target_kind = $1 AND target_id = $2
	ORDER BY last_write_at DESC`

const listByProducerStmt = `
	SELECT ` + producerColumns + `
	FROM content_producers
	WHERE producer_kind = $1 AND producer_id = $2
	ORDER BY last_write_at DESC
	LIMIT $3`

type postgresStore struct{ db *sql.DB }

// NewPostgres returns the PostgreSQL-backed store, or nil when db is nil so a
// deployment without a database records nothing rather than failing writes.
func NewPostgres(db *sql.DB) Store {
	if db == nil {
		return nil
	}
	return &postgresStore{db: db}
}

func (s *postgresStore) Record(ctx context.Context, w Write) error { //nolint:revive // interface impl
	count := 1
	if w.Uncounted {
		count = 0
	}
	_, err := s.db.ExecContext(ctx, recordStmt,
		w.TargetKind, w.TargetID, w.Producer.Kind, w.Producer.ID, w.Producer.Label,
		w.Created, count, w.Version,
	)
	if err != nil {
		return fmt.Errorf("recording producer: %w", err)
	}
	return nil
}

func (s *postgresStore) ListByTarget(ctx context.Context, targetKind, targetID string) ([]Row, error) { //nolint:revive // interface impl
	rows, err := s.db.QueryContext(ctx, listByTargetStmt, targetKind, targetID)
	if err != nil {
		return nil, fmt.Errorf("listing producers of %s %s: %w", targetKind, targetID, err)
	}
	return scanRows(rows)
}

func (s *postgresStore) ListByProducer(ctx context.Context, producerKind, producerID string, limit int) ([]Row, error) { //nolint:revive // interface impl
	if limit <= 0 {
		limit = DefaultProducerLimit
	}
	rows, err := s.db.QueryContext(ctx, listByProducerStmt, producerKind, producerID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing what %s %s produced: %w", producerKind, producerID, err)
	}
	return scanRows(rows)
}

// scanRows drains a producer projection into rows, closing it either way.
func scanRows(rows *sql.Rows) ([]Row, error) {
	defer rows.Close() //nolint:errcheck // read-only cursor
	out := []Row{}
	for rows.Next() {
		var r Row
		if err := rows.Scan(
			&r.TargetKind, &r.TargetID, &r.Producer.Kind, &r.Producer.ID, &r.Producer.Label,
			&r.Created, &r.FirstWriteAt, &r.LastWriteAt, &r.WriteCount, &r.LastVersion,
		); err != nil {
			return nil, fmt.Errorf("scanning producer row: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading producer rows: %w", err)
	}
	return out, nil
}
