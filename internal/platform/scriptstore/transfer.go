package scriptstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// pgUniqueViolation is the SQLSTATE a name collision under the per-owner unique
// index raises.
const pgUniqueViolation = "23505"

// Transfer moves a script to a new owner and records the move as a new applied
// version authored by the administrator making it.
//
// The version is written unconditionally, unlike an edit, which snapshots only
// when the substance moved. Nothing about the code changes here — what changes
// is the authority a run presents, since a run carries the roles captured on
// the version it executes. Writing the snapshot is therefore the transfer: it
// is what makes the script run as its new owner rather than as the person who
// no longer has it, and it leaves the hand-over in the history where the next
// reader of a run can see where that authority came from.
//
// A collision with a script the new owner already keeps under the same name is
// reported as script.ErrNameTaken: names are unique within an owner, so the
// receiving side decides whether the transfer is possible.
func (s *Store) Transfer(ctx context.Context, id, newOwnerEmail string, author script.Author) error {
	return s.withTx(ctx, "transfer script", func(tx *sql.Tx) error {
		sc, err := lockScript(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := sc.Transfer(newOwnerEmail); err != nil {
			return err //nolint:wrapcheck // caller-facing domain refusal, deliberately verbatim
		}
		n, err := nextVersionNumber(ctx, tx, id)
		if err != nil {
			return err
		}
		sc.Version = n
		if err := insertVersionRow(ctx, tx, versionInsert{
			ScriptID: id, Version: n, Snapshot: sc,
			Author: author, Status: script.VersionStatusApplied,
		}); err != nil {
			return err
		}
		if _, err := updateTx(ctx, tx, sc); err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("a script named %q already belongs to %s: %w",
					sc.Name, sc.OwnerEmail, script.ErrNameTaken)
			}
			return err
		}
		return nil
	})
}

// isUniqueViolation reports whether err is a unique-constraint violation.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && string(pqErr.Code) == pgUniqueViolation
}
