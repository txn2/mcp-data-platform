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
var (
	_ script.ApprovalStore     = (*Store)(nil)
	_ script.AutoApprovalStore = (*Store)(nil)
)

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
	// auto marks an approval the platform made for a personal script's owner
	// rather than one a reviewer decided (#1367).
	auto bool
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
	if err := autoApprovable(sc, v, req.auto); err != nil {
		return nil, err
	}
	// The roles come from the version, never from the request: approving may
	// narrow what a script reaches and may never widen what it is.
	req.grants.Roles = v.AuthorRoles
	if err := req.grants.Validate(); err != nil {
		return nil, fmt.Errorf("this version cannot be approved with that capability set: %w (%w)", err, script.ErrInvalidGrant)
	}
	if err := stampApproval(ctx, tx, v, req); err != nil {
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

// autoApprovable refuses an automatic approval of anything but a personal
// script's own owner's version (#1367).
//
// It is here rather than only in the caller because this is where the grant's
// roles are taken from the version's author. A version somebody else wrote
// carries THEIR roles, so approving it without a reviewer would put a script on
// authority its owner never held — the one thing the whole grant model exists to
// prevent. The caller decides whether to ask; this decides whether the answer
// can be yes.
func autoApprovable(sc *script.Script, v *script.Version, auto bool) error {
	if !auto {
		return nil
	}
	if !sc.OwnedPersonally(v.Author) {
		return fmt.Errorf(
			"version %d was written by %q, who does not own this personal script, so it cannot be approved without a reviewer: %w",
			v.Version, v.Author, script.ErrVersionConflict)
	}
	return nil
}

// stampApproval writes the approval stamp and the bound grant onto the version,
// recording whether a person decided it or the platform minted it.
func stampApproval(ctx context.Context, tx *sql.Tx, v *script.Version, req approval) error {
	grantsJSON, err := json.Marshal(req.grants)
	if err != nil {
		return fmt.Errorf("marshal version grants: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE script_versions
		   SET approved_by = $2, approved_at = NOW(), grants = $3,
		       status = $4, auto_approved = $5
		 WHERE id = $1`,
		v.ID, req.approver, grantsJSON, script.VersionStatusApplied, req.auto)
	if err != nil {
		return fmt.Errorf("stamp script version approval: %w", err)
	}
	return nil
}

// AutoApproveVersion is the automatic half of the execution gate (#1367): the
// approval the platform makes for a personal script whose own owner wrote the
// version.
//
// It is deliberately the same transaction body as ApproveVersion. An
// automatically approved version binds a grant, applies its snapshot to the live
// row, supersedes competing drafts, and moves the execution pointer, on exactly
// the terms a reviewed one does — including the rule that matters most, which is
// that the grant's roles are replaced with the version author's own. What is
// different is one recorded fact: nobody reviewed it, and the row says so.
func (s *Store) AutoApproveVersion(ctx context.Context, scriptID string, version int, owner string, grants script.Grants) (*script.Version, error) {
	var approved *script.Version
	err := s.withTx(ctx, "auto-approve script version", func(tx *sql.Tx) error {
		var err error
		approved, err = approveTx(ctx, tx, approval{
			scriptID: scriptID, version: version, approver: owner, grants: grants, auto: true,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return approved, nil
}

// withdrawAutoApproval takes back an approval nobody made, when the edit being
// applied widens a personal script's scope past the audience that approval was
// reasoned about (#1367).
//
// The version was approved because its only caller was its author. A
// persona-scoped or global script has an audience that agreed to nothing, so the
// automatic approval must not follow the script into it: the execution pointer is
// cleared and the version's stamp and grant are removed, which puts it straight
// back into the review queue (that query lists the live version of any script
// with no approved version). An approval a PERSON made survives the change,
// which is what the auto_approved predicate on the update expresses — they
// decided, and widening the audience does not un-decide it.
//
// It runs inside UpdateWithVersion's transaction, under the script row lock, so
// there is no instant at which a widened script is executable on an approval
// nobody gave it.
func withdrawAutoApproval(ctx context.Context, tx *sql.Tx, before, sc *script.Script) error {
	if !script.WithdrawsAutoApproval(before, sc) {
		return nil
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE script_versions
		   SET approved_by = '', approved_at = NULL, grants = '{}'::jsonb,
		       auto_approved = FALSE
		 WHERE id = $1 AND auto_approved`, before.ApprovedVersionID)
	if err != nil {
		return fmt.Errorf("withdraw automatic script approval: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("withdraw automatic script approval: %w", err)
	}
	if n == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE scripts SET approved_version_id = NULL WHERE id = $1`, sc.ID); err != nil {
		return fmt.Errorf("clear script execution gate: %w", err)
	}
	// The struct is advanced to what this transaction leaves behind, because the
	// caller's own updateTx writes the status and the indexed text hash after
	// this runs, and both read the execution pointer.
	sc.ApprovedVersionID = ""
	if sc.Status == script.StatusActive {
		sc.Status = script.StatusDraft
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
