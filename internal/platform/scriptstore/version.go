package scriptstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// versionColumns is the column list read by every script_versions SELECT,
// mirrored by scanVersion so the scan order cannot drift from the query.
const versionColumns = `id, script_id, version, display_name, description,
	source_code, params, tags, author, author_roles, status, approved_by,
	approved_at, grants, created_at`

// versionSelect is the base SELECT for the version columns.
const versionSelect = "SELECT " + versionColumns + " FROM script_versions"

// scanVersion reads one row in versionColumns order into a Version.
func scanVersion(sc rowScanner) (*script.Version, error) {
	v := &script.Version{}
	var paramsJSON, grantsJSON []byte
	err := sc.Scan(&v.ID, &v.ScriptID, &v.Version, &v.DisplayName, &v.Description,
		&v.Source, &paramsJSON, pq.Array(&v.Tags), &v.Author, pq.Array(&v.AuthorRoles),
		&v.Status, &v.ApprovedBy, &v.ApprovedAt, &grantsJSON, &v.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("scanning script version row: %w", err)
	}
	if err := json.Unmarshal(paramsJSON, &v.Params); err != nil {
		return nil, fmt.Errorf("unmarshal version params: %w", err)
	}
	if err := json.Unmarshal(grantsJSON, &v.Grants); err != nil {
		return nil, fmt.Errorf("unmarshal version grants: %w", err)
	}
	if v.Params == nil {
		v.Params = []script.Param{}
	}
	if v.Tags == nil {
		v.Tags = []string{}
	}
	if v.AuthorRoles == nil {
		v.AuthorRoles = []string{}
	}
	return v, nil
}

// versionInsert carries one script_versions INSERT: the snapshot source, its
// author, the row status, and an optional approval stamp bound to the row.
type versionInsert struct {
	ScriptID   string
	Version    int
	Snapshot   *script.Script
	Author     script.Author
	Status     string
	ApprovedBy string
	ApprovedAt *time.Time
}

// insertVersionRow snapshots the versioned fields of ins.Snapshot as one
// immutable script_versions row within the caller's transaction.
//
// The author's roles are part of the snapshot, not metadata about it: they are
// the authority ceiling an approval of this row may bind (script.Author).
func insertVersionRow(ctx context.Context, tx *sql.Tx, ins versionInsert) error {
	paramsJSON, err := json.Marshal(ins.Snapshot.Params)
	if err != nil {
		return fmt.Errorf("marshal version params: %w", err)
	}
	tags := ins.Snapshot.Tags
	if tags == nil {
		tags = []string{}
	}
	roles := ins.Author.Roles
	if roles == nil {
		roles = []string{}
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO script_versions (script_id, version, display_name, description,
		                             source_code, params, tags, author, author_roles,
		                             status, approved_by, approved_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		ins.ScriptID, ins.Version, ins.Snapshot.DisplayName, ins.Snapshot.Description,
		ins.Snapshot.Source, paramsJSON, pq.Array(tags), ins.Author.Email, pq.Array(roles),
		ins.Status, ins.ApprovedBy, ins.ApprovedAt)
	if err != nil {
		return fmt.Errorf("insert script version: %w", err)
	}
	return nil
}

// lockScript locks and returns the full script row for the transaction, or an
// error when the script does not exist.
func lockScript(ctx context.Context, tx *sql.Tx, id string) (*script.Script, error) {
	sc, err := scanScript(tx.QueryRowContext(ctx, scriptSelect+` WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("script %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("lock script: %w", err)
	}
	return sc, nil
}

// nextVersionNumber allocates the next version number for a script. Callers
// hold the script row lock, which serializes allocation. GREATEST guards a row
// whose live version somehow exceeds its history.
func nextVersionNumber(ctx context.Context, tx *sql.Tx, scriptID string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `
		SELECT GREATEST(COALESCE(MAX(sv.version), 0),
		                (SELECT version FROM scripts WHERE id = $1)) + 1
		  FROM script_versions sv WHERE sv.script_id = $1`, scriptID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("next version number: %w", err)
	}
	return n, nil
}

// requireUngated re-validates the review gate against the row as locked in this
// transaction. Callers decide gating from an earlier unlocked read; if the
// script gained an approved version between that read and the write (an edit
// racing an approval), applying the edit here would swap the source out from
// under a live approval — exactly what the gate exists to prevent — so the
// write is rejected as a conflict for the caller to re-read and retry.
func requireUngated(locked, sc *script.Script) error {
	if script.RequiresReview(locked, sc) {
		return fmt.Errorf("script %s was approved while this edit was in flight; re-read and retry (source or parameter changes to an approved script require review): %w",
			sc.ID, script.ErrVersionConflict)
	}
	return nil
}

// UpdateWithVersion persists sc like Update and, when any versioned snapshot
// field changed against the stored row, records a new applied version authored
// by author and advances sc.Version to it. The review gate is re-validated
// under the row lock (see requireUngated).
func (s *Store) UpdateWithVersion(ctx context.Context, sc *script.Script, author script.Author) error {
	normalizeSlices(sc)
	return s.withTx(ctx, "update script with version", func(tx *sql.Tx) error {
		before, err := lockScript(ctx, tx, sc.ID)
		if err != nil {
			return err
		}
		if err := requireUngated(before, sc); err != nil {
			return err
		}
		if script.SnapshotChanged(before, sc) {
			n, err := nextVersionNumber(ctx, tx, sc.ID)
			if err != nil {
				return err
			}
			sc.Version = n
			if err := insertVersionRow(ctx, tx, versionInsert{
				ScriptID: sc.ID, Version: n, Snapshot: sc,
				Author: author, Status: script.VersionStatusApplied,
			}); err != nil {
				return err
			}
		}
		return updateTx(ctx, tx, sc)
	})
}

// CreateDraftVersion snapshots proposed's versioned fields as a new draft
// version of the script without touching the live row, returning the new
// version number. The approved version keeps executing until the draft is
// approved.
func (s *Store) CreateDraftVersion(ctx context.Context, scriptID string, proposed *script.Script, author script.Author) (int, error) {
	var n int
	err := s.withTx(ctx, "create draft version", func(tx *sql.Tx) error {
		if _, err := lockScript(ctx, tx, scriptID); err != nil {
			return err
		}
		var err error
		if n, err = nextVersionNumber(ctx, tx, scriptID); err != nil {
			return err
		}
		return insertVersionRow(ctx, tx, versionInsert{
			ScriptID: scriptID, Version: n, Snapshot: proposed,
			Author: author, Status: script.VersionStatusDraft,
		})
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// ListVersions returns every version of the script, newest first.
func (s *Store) ListVersions(ctx context.Context, scriptID string) ([]script.Version, error) {
	rows, err := s.db.QueryContext(ctx,
		versionSelect+` WHERE script_id = $1 ORDER BY version DESC`, scriptID)
	if err != nil {
		return nil, fmt.Errorf("list script versions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []script.Version{}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate script versions: %w", err)
	}
	return out, nil
}

// GetVersion returns one version with its full source, or nil, nil when the
// script has no such version.
func (s *Store) GetVersion(ctx context.Context, scriptID string, version int) (*script.Version, error) {
	v, err := scanVersion(s.db.QueryRowContext(ctx,
		versionSelect+` WHERE script_id = $1 AND version = $2`, scriptID, version))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // VersionStore contract: nil, nil means not found
	}
	if err != nil {
		return nil, fmt.Errorf("get script version: %w", err)
	}
	return v, nil
}

// GetVersionByID returns one version by id, or nil, nil when no such version
// exists. It is the runner's read: the execution gate stores an id, and only an
// id identifies one immutable snapshot for the life of the script.
func (s *Store) GetVersionByID(ctx context.Context, id string) (*script.Version, error) {
	v, err := scanVersion(s.db.QueryRowContext(ctx, versionSelect+` WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // VersionStore contract: nil, nil means not found
	}
	if err != nil {
		return nil, fmt.Errorf("get script version by id: %w", err)
	}
	return v, nil
}
