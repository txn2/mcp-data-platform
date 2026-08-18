package scriptstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Compile-time interface verification for the execution gate's write side.
var _ script.ApprovalStore = (*Store)(nil)

// ApproveVersion is the only write in this package that may set
// scripts.approved_version_id, and therefore the only thing that can make a
// script executable by the platform.
//
// Everything it does happens under the script row lock, in one transaction:
//
//  1. The grant's roles are REPLACED with the version's own author roles. A
//     caller cannot pass roles in — approval records the authority the author
//     held, so approving can narrow what a script reaches but can never hand it
//     authority its author did not have.
//  2. The approved version's snapshot is applied to the live row and every
//     pending draft is superseded, so the code being served and the code being
//     executed are the same code. That holds for a draft being promoted and
//     equally for an earlier version being approved back into service: a
//     rollback that moved the execution pointer while the live row kept serving
//     the newer source would leave a script whose readable code is not the code
//     that runs.
//  3. The script's execution pointer moves to the approved version, and a
//     script still in its authoring state becomes active.
//
// Re-approving a version rebinds its grant and re-stamps the approval; that is
// the deliberate act the "widening requires re-approval" rule asks for.
func (s *Store) ApproveVersion(ctx context.Context, scriptID string, version int, approver string, grants script.Grants) (*script.Version, error) {
	var approved *script.Version
	err := s.withTx(ctx, "approve script version", func(tx *sql.Tx) error {
		var err error
		approved, err = approveTx(ctx, tx, approval{
			scriptID: scriptID, version: version, approver: approver, grants: grants,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return approved, nil
}

// approval is one approval request as the transaction body reads it.
type approval struct {
	scriptID string
	version  int
	approver string
	grants   script.Grants
}

// approveTx is ApproveVersion's transaction body: lock both rows, check the
// version may be approved, bind the grant, and move the gate.
func approveTx(ctx context.Context, tx *sql.Tx, req approval) (*script.Version, error) {
	sc, err := lockScript(ctx, tx, req.scriptID)
	if err != nil {
		return nil, err
	}
	v, err := lockVersion(ctx, tx, req.scriptID, req.version)
	if err != nil {
		return nil, err
	}
	if err := approvable(sc, v); err != nil {
		return nil, err
	}
	// The roles come from the version, never from the request: approving may
	// narrow what a script reaches and may never widen what it is.
	req.grants.Roles = v.AuthorRoles
	if err := req.grants.Validate(); err != nil {
		return nil, fmt.Errorf("this version cannot be approved with that capability set: %w (%w)", err, script.ErrInvalidGrant)
	}
	if err := stampApproval(ctx, tx, v, req.approver, req.grants); err != nil {
		return nil, err
	}
	if err := applySnapshot(ctx, tx, sc, v); err != nil {
		return nil, err
	}
	if err := pointExecutionGate(ctx, tx, sc, v); err != nil {
		return nil, err
	}
	return readVersionTx(ctx, tx, v.ID)
}

// lockVersion loads one version FOR UPDATE within the caller's transaction.
func lockVersion(ctx context.Context, tx *sql.Tx, scriptID string, version int) (*script.Version, error) {
	v, err := scanVersion(tx.QueryRowContext(ctx,
		versionSelect+` WHERE script_id = $1 AND version = $2 FOR UPDATE`, scriptID, version))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("script %s has no version %d: %w", scriptID, version, script.ErrVersionConflict)
	}
	if err != nil {
		return nil, fmt.Errorf("lock script version: %w", err)
	}
	return v, nil
}

// approvable refuses the version states an approval must not resolve: a
// version already taken out of consideration, and a script that has been
// replaced. Both would produce a running script nobody expects to be running.
func approvable(sc *script.Script, v *script.Version) error {
	switch v.Status {
	case script.VersionStatusRejected, script.VersionStatusSuperseded:
		return fmt.Errorf("version %d is %s and cannot be approved; propose a new version: %w",
			v.Version, v.Status, script.ErrVersionConflict)
	}
	if sc.Status == script.StatusSuperseded {
		return fmt.Errorf("script %s was superseded by %q, so it must not be made executable: %w",
			sc.Name, sc.SupersededBy, script.ErrVersionConflict)
	}
	return nil
}

// stampApproval writes the approval stamp and the bound grant onto the version.
func stampApproval(ctx context.Context, tx *sql.Tx, v *script.Version, approver string, grants script.Grants) error {
	grantsJSON, err := json.Marshal(grants)
	if err != nil {
		return fmt.Errorf("marshal version grants: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE script_versions
		   SET approved_by = $2, approved_at = NOW(), grants = $3,
		       status = $4
		 WHERE id = $1`,
		v.ID, approver, grantsJSON, script.VersionStatusApplied)
	if err != nil {
		return fmt.Errorf("stamp script version approval: %w", err)
	}
	return nil
}

// applySnapshot copies the approved version onto the live script row and
// supersedes every pending draft, so approving resolves the queue rather than
// leaving competing proposals behind it. Approving the version the live row
// already carries writes the same values back, which is a no-op worth paying
// for: the alternative is a conditional that has to decide which versions are
// "already live", and getting that wrong is exactly the divergence this
// prevents.
func applySnapshot(ctx context.Context, tx *sql.Tx, sc *script.Script, v *script.Version) error {
	sc.Source = v.Source
	sc.Params = v.Params
	sc.DisplayName = v.DisplayName
	sc.Description = v.Description
	sc.Tags = v.Tags
	sc.Version = v.Version
	normalizeSlices(sc)
	if _, err := updateTx(ctx, tx, sc); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE script_versions SET status = $3
		 WHERE script_id = $1 AND id <> $2 AND status = $4`,
		sc.ID, v.ID, script.VersionStatusSuperseded, script.VersionStatusDraft)
	if err != nil {
		return fmt.Errorf("supersede pending script drafts: %w", err)
	}
	return nil
}

// pointExecutionGate moves scripts.approved_version_id to the approved version
// and lifts a script out of its authoring state. A deprecated script keeps its
// status: re-approving code does not put a retired script back in service,
// which is an operator's decision to make explicitly.
func pointExecutionGate(ctx context.Context, tx *sql.Tx, sc *script.Script, v *script.Version) error {
	status := sc.Status
	if status == script.StatusDraft {
		status = script.StatusActive
	}
	// The struct is advanced to the state this statement writes before the hash
	// is taken, because the indexed text reads the execution pointer: making a
	// script executable rewrites the card's last line, and a hash taken from the
	// pre-approval struct would leave the old vector in place claiming nothing
	// will run it. applySnapshot's own updateTx ran while the pointer was still
	// unset, so this is the write that sees the change.
	sc.ApprovedVersionID = v.ID
	sc.Status = status
	// #nosec G201 G202 -- the only interpolation is a constant parameter index
	// into a constant SQL fragment; every value is bound.
	q := `
		UPDATE scripts SET approved_version_id = $2, status = $3, updated_at = NOW()` +
		fmt.Sprintf(indexInvalidation, gateHashParam) +
		"\n\t\t WHERE id = $1"
	_, err := tx.ExecContext(ctx, q, sc.ID, v.ID, status,
		indexjobs.TextHash(script.IndexText(sc)))
	if err != nil {
		return fmt.Errorf("point script execution gate: %w", err)
	}
	return nil
}

// gateHashParam is pointExecutionGate's placeholder index for the new text
// hash, one past its last column value.
const gateHashParam = 4

// readVersionTx re-reads a version inside the transaction so the caller gets
// the row as stored rather than the struct the caller assembled.
func readVersionTx(ctx context.Context, tx *sql.Tx, id string) (*script.Version, error) {
	v, err := scanVersion(tx.QueryRowContext(ctx, versionSelect+` WHERE id = $1`, id))
	if err != nil {
		return nil, fmt.Errorf("re-read approved script version: %w", err)
	}
	return v, nil
}
