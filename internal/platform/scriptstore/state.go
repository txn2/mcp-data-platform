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
var _ script.StateStore = (*Store)(nil)

// stateColumns is the column list every script_state SELECT reads, mirrored by
// scanState so the scan order cannot drift from the query.
const stateColumns = `script_id, state, revision, run_id, updated_by, updated_at`

// scanState reads one row in stateColumns order.
func scanState(sc rowScanner) (*script.State, error) {
	st := &script.State{}
	var value []byte
	if err := sc.Scan(&st.ScriptID, &value, &st.Revision, &st.RunID, &st.UpdatedBy, &st.UpdatedAt); err != nil {
		return nil, fmt.Errorf("scanning script state row: %w", err)
	}
	if err := json.Unmarshal(value, &st.Value); err != nil {
		return nil, fmt.Errorf("unmarshal script state: %w", err)
	}
	if st.Value == nil {
		st.Value = map[string]any{}
	}
	return st, nil
}

// GetState returns the script's state, or the empty state at revision 0 for a
// script that has never saved any and never had it reset. The absent row IS
// revision 0: a run's compare-and-set against it is an insert.
func (s *Store) GetState(ctx context.Context, scriptID string) (*script.State, error) {
	st, err := scanState(s.db.QueryRowContext(ctx,
		`SELECT `+stateColumns+` FROM script_state WHERE script_id = $1`, scriptID))
	if errors.Is(err, sql.ErrNoRows) {
		return script.EmptyState(scriptID), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get script state: %w", err)
	}
	return st, nil
}

// SetState replaces the state unconditionally and moves the revision forward,
// recording the person who did it. It is the owner's and the administrator's
// reset; a run never comes through here, because a run's write is predicated
// on the revision it read and belongs to the transaction that finishes the run.
func (s *Store) SetState(ctx context.Context, scriptID string, value map[string]any, by string) (*script.State, error) {
	if err := script.ValidateState(value); err != nil {
		return nil, fmt.Errorf("set script state: %w", err)
	}
	encoded, err := json.Marshal(orEmptyParams(value))
	if err != nil {
		return nil, fmt.Errorf("marshal script state: %w", err)
	}
	st, err := scanState(s.db.QueryRowContext(ctx, `
		INSERT INTO script_state (script_id, state, revision, run_id, updated_by)
		VALUES ($1, $2, 1, '', $3)
		ON CONFLICT (script_id) DO UPDATE
		   SET state = EXCLUDED.state, revision = script_state.revision + 1,
		       run_id = '', updated_by = EXCLUDED.updated_by, updated_at = NOW()
		RETURNING `+stateColumns,
		scriptID, encoded, by))
	if err != nil {
		return nil, fmt.Errorf("set script state: %w", err)
	}
	return st, nil
}

// runStateWrite is what Finish needs to apply a run's staged state: which
// script, what the run read, and what it staged.
type runStateWrite struct {
	scriptID string
	runID    string
	read     int64
	value    map[string]any
}

// writeRunState applies one run's state write inside the transaction that
// finishes the run, predicated on the revision the run read.
//
// It is one statement: an insert for a script whose state row does not exist
// yet (revision 0, the only case an insert can land), and otherwise an update
// guarded by the revision. A guard that fails updates nothing and returns no
// row, which is the conflict; the current row is then read to name the writer,
// so the run's failure says who moved the state rather than only that it
// moved.
func writeRunState(ctx context.Context, tx *sql.Tx, w runStateWrite) (int64, error) {
	encoded, err := json.Marshal(orEmptyParams(w.value))
	if err != nil {
		return 0, fmt.Errorf("marshal run state: %w", err)
	}
	var revision int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO script_state (script_id, state, revision, run_id, updated_by)
		VALUES ($1, $2, 1, $3, '')
		ON CONFLICT (script_id) DO UPDATE
		   SET state = EXCLUDED.state, revision = script_state.revision + 1,
		       run_id = EXCLUDED.run_id, updated_by = '', updated_at = NOW()
		 WHERE script_state.revision = $4
		RETURNING revision`,
		w.scriptID, encoded, w.runID, w.read).Scan(&revision)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, stateConflict(ctx, tx, w)
	}
	if err != nil {
		return 0, fmt.Errorf("write run state: %w", err)
	}
	return revision, nil
}

// stateConflict reads the row that refused the write and renders the conflict
// naming its writer.
func stateConflict(ctx context.Context, tx *sql.Tx, w runStateWrite) error {
	current, err := scanState(tx.QueryRowContext(ctx,
		`SELECT `+stateColumns+` FROM script_state WHERE script_id = $1`, w.scriptID))
	if err != nil {
		return fmt.Errorf("reading the state that refused a run's write: %w", err)
	}
	return &script.StateConflictError{Read: w.read, Current: current.Revision, WrittenBy: current.WrittenBy()}
}
