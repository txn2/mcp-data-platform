package scriptstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Compile-time interface verification.
var _ script.DryRunStore = (*Store)(nil)

// dryRunHistoryPerAuthor is how many accounts of draft runs one author keeps
// per script. The table is trimmed at write rather than swept on a schedule
// because the bound is a property of the authoring loop, not of time: an author
// iterating on a script produces one account per attempt, and the ones worth
// keeping are the recent ones plus whichever version later carries their exact
// source. A generous handful covers both without letting a person's afternoon
// of iteration accumulate forever.
const dryRunHistoryPerAuthor = 20

// dryRunColumns is the column list every script_dry_runs SELECT reads, mirrored
// by scanDryRun so the scan order cannot drift from the query.
const dryRunColumns = `id, script_id, source_sha256, requested_by, status, error,
	log, log_truncated, metrics, outputs, state_written, created_at`

// RecordDryRun stores one account of a draft execution and trims the author's
// older accounts of the same script.
//
// The trim is in the same statement batch rather than in a sweeper because
// there is no other producer: an account is written by exactly one path, and
// bounding it where it is written means the table cannot grow while a sweeper
// is misconfigured or absent.
func (s *Store) RecordDryRun(ctx context.Context, d *script.DryRun) error {
	if d == nil || d.ID == "" {
		return errors.New("a dry-run account needs the run id it executed under")
	}
	metrics, err := json.Marshal(d.Metrics)
	if err != nil {
		return fmt.Errorf("marshal dry-run metrics: %w", err)
	}
	outputs, err := json.Marshal(orEmptyDryRunOutputs(d.Outputs))
	if err != nil {
		return fmt.Errorf("marshal dry-run outputs: %w", err)
	}
	stateWritten, err := nullableJSON(d.StateWritten)
	if err != nil {
		return fmt.Errorf("marshal dry-run state: %w", err)
	}
	return s.withTx(ctx, "record script dry run", func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `
			INSERT INTO script_dry_runs (id, script_id, source_sha256, requested_by,
			                             status, error, log, log_truncated, metrics, outputs,
			                             state_written)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			RETURNING created_at`,
			d.ID, d.ScriptID, d.SourceSHA256, d.RequestedBy, d.Status, d.Error,
			d.Log, d.LogTruncated, metrics, outputs, stateWritten)
		if err := row.Scan(&d.CreatedAt); err != nil {
			return fmt.Errorf("record script dry run: %w", err)
		}
		_, err := tx.ExecContext(ctx, `
			DELETE FROM script_dry_runs
			WHERE script_id = $1 AND requested_by = $2 AND id NOT IN (
				SELECT id FROM script_dry_runs
				WHERE script_id = $1 AND requested_by = $2
				ORDER BY created_at DESC
				LIMIT $3
			)`, d.ScriptID, d.RequestedBy, dryRunHistoryPerAuthor)
		if err != nil {
			return fmt.Errorf("trim script dry runs: %w", err)
		}
		return nil
	})
}

// LatestDryRun returns the newest account of one script's exact source.
//
// Nil is the ordinary answer rather than an error: most versions were never
// dry-run, and the reviewer's question is "did anyone run this", whose honest
// negative is a nil.
func (s *Store) LatestDryRun(ctx context.Context, scriptID string, sourceSHA256 []byte) (*script.DryRun, error) {
	if scriptID == "" || len(sourceSHA256) == 0 {
		return nil, nil //nolint:nilnil // nothing to match is "nobody ran it", not a failure
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+dryRunColumns+`
		FROM script_dry_runs
		WHERE script_id = $1 AND source_sha256 = $2
		ORDER BY created_at DESC
		LIMIT 1`, scriptID, sourceSHA256)
	d, err := scanDryRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // see above
	}
	if err != nil {
		return nil, fmt.Errorf("read script dry run: %w", err)
	}
	return d, nil
}

// scanDryRun materializes one account in dryRunColumns order.
func scanDryRun(row *sql.Row) (*script.DryRun, error) {
	var (
		d                       script.DryRun
		metrics, output, stated []byte
	)
	err := row.Scan(&d.ID, &d.ScriptID, &d.SourceSHA256, &d.RequestedBy, &d.Status,
		&d.Error, &d.Log, &d.LogTruncated, &metrics, &output, &stated, &d.CreatedAt)
	if err != nil {
		// Wrapped rather than returned bare so the caller's sql.ErrNoRows test
		// still matches through it: "nobody ran this" is an ordinary answer and
		// has to stay distinguishable from a read that failed.
		return nil, fmt.Errorf("scan script dry run: %w", err)
	}
	if err := json.Unmarshal(metrics, &d.Metrics); err != nil {
		return nil, fmt.Errorf("decode dry-run metrics: %w", err)
	}
	if err := json.Unmarshal(output, &d.Outputs); err != nil {
		return nil, fmt.Errorf("decode dry-run outputs: %w", err)
	}
	// NULL is a draft that saved nothing; {} is one that saved an empty object.
	if len(stated) > 0 {
		if err := json.Unmarshal(stated, &d.StateWritten); err != nil {
			return nil, fmt.Errorf("decode dry-run state: %w", err)
		}
		if d.StateWritten == nil {
			d.StateWritten = map[string]any{}
		}
	}
	return &d, nil
}

// nullableJSON encodes an object for a nullable JSONB column: nil binds NULL,
// and an empty object binds {}, because the two mean different things to the
// reader of a run.
func nullableJSON(value map[string]any) (any, error) {
	if value == nil {
		return nil, nil //nolint:nilnil // NULL is the value being bound
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller names the column
	}
	return encoded, nil
}

// orEmptyDryRunOutputs normalizes a nil output slice so the column stores [].
func orEmptyDryRunOutputs(outputs []script.DryRunOutput) []script.DryRunOutput {
	if outputs == nil {
		return []script.DryRunOutput{}
	}
	return outputs
}
