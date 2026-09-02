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

// moveOutputAssetsStmt and moveOutputCollectionsStmt hand the files a script's
// runs created to the script's new owner (#1588). Each rewrites the address on
// every live row whose producer record says this script CREATED it: a row the
// script only wrote a version over is somebody else's file and stays theirs.
// The owner id is left as it is. A run finds its output by
// (owner_id, idempotency_key), and the owner id there is the script's own
// principal, which a transfer does not change; the address is the arm a person
// is matched on, and it is the one that moves.
const (
	moveOutputAssetsStmt = `
	UPDATE portal_assets a
	SET owner_email = $1, updated_at = NOW()
	FROM content_producers cp
	WHERE cp.target_kind = 'asset' AND cp.target_id = a.id
	  AND cp.producer_kind = 'script' AND cp.producer_id = $2 AND cp.created
	  AND a.deleted_at IS NULL`

	moveOutputCollectionsStmt = `
	UPDATE portal_collections c
	SET owner_email = $1, updated_at = NOW()
	FROM content_producers cp
	WHERE cp.target_kind = 'collection' AND cp.target_id = c.id
	  AND cp.producer_kind = 'script' AND cp.producer_id = $2 AND cp.created
	  AND c.deleted_at IS NULL`
)

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
//
// When the request says the outputs move, the assets and collections the
// script's runs created are handed to the new owner in the same transaction
// (#1588), so a transfer never lands with half of what it was asked for: a
// refused name moves no file, and a file update that fails moves no script.
func (s *Store) Transfer(ctx context.Context, req script.TransferRequest, author script.Author) (script.Transferred, error) {
	var moved script.Transferred
	err := s.withTx(ctx, "transfer script", func(tx *sql.Tx) error {
		sc, err := transferScriptTx(ctx, tx, req, author)
		if err != nil {
			return err
		}
		if req.Outputs != script.OutputsMove {
			return nil
		}
		moved, err = moveOutputs(ctx, tx, req.ID, sc.OwnerEmail)
		return err
	})
	if err != nil {
		return script.Transferred{}, err
	}
	return moved, nil
}

// transferScriptTx is the script's own half of the transfer: the locked
// record moved to its new owner, the version that carries the new authority,
// and the live row, in that order. It returns the record as written, whose
// address is the normalized one the outputs are handed to.
func transferScriptTx(ctx context.Context, tx *sql.Tx, req script.TransferRequest, author script.Author) (*script.Script, error) {
	sc, err := lockScript(ctx, tx, req.ID)
	if err != nil {
		return nil, err
	}
	if err := sc.Transfer(req.NewOwnerEmail); err != nil {
		return nil, err //nolint:wrapcheck // caller-facing domain refusal, deliberately verbatim
	}
	n, err := nextVersionNumber(ctx, tx, req.ID)
	if err != nil {
		return nil, err
	}
	sc.Version = n
	if err := insertVersionRow(ctx, tx, versionInsert{
		ScriptID: req.ID, Version: n, Snapshot: sc,
		Author: author, Status: script.VersionStatusApplied,
	}); err != nil {
		return nil, err
	}
	if _, err := updateTx(ctx, tx, sc); err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("a script named %q already belongs to %s: %w",
				sc.Name, sc.OwnerEmail, script.ErrNameTaken)
		}
		return nil, err
	}
	return sc, nil
}

// moveOutputs rewrites the owner address on the files a script created, and
// counts what it touched. It takes the normalized address off the script
// record rather than the request, so a file and the script it belongs to can
// never come to spell one person two ways.
func moveOutputs(ctx context.Context, tx *sql.Tx, scriptID, ownerEmail string) (script.Transferred, error) {
	assets, err := tx.ExecContext(ctx, moveOutputAssetsStmt, ownerEmail, scriptID)
	if err != nil {
		return script.Transferred{}, fmt.Errorf("moving the script's assets: %w", err)
	}
	collections, err := tx.ExecContext(ctx, moveOutputCollectionsStmt, ownerEmail, scriptID)
	if err != nil {
		return script.Transferred{}, fmt.Errorf("moving the script's collections: %w", err)
	}
	a, err := assets.RowsAffected()
	if err != nil {
		return script.Transferred{}, fmt.Errorf("counting moved assets: %w", err)
	}
	c, err := collections.RowsAffected()
	if err != nil {
		return script.Transferred{}, fmt.Errorf("counting moved collections: %w", err)
	}
	return script.Transferred{AssetsMoved: int(a), CollectionsMoved: int(c)}, nil
}

// isUniqueViolation reports whether err is a unique-constraint violation.
func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && string(pqErr.Code) == pgUniqueViolation
}
