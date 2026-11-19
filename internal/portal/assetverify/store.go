// Package assetverify is the Verified badge on assets (#2503): who checked an
// asset, the version they checked, an optional note and an optional expiry.
//
// Who may verify is an authorization decision made on the server for every
// surface: an administrator, or a person whose persona carries the portal
// permission verify_assets ("Can verify assets"). An asset's owner without it
// cannot verify their own asset. Removing a verification is open to the
// person who verified, any holder of the permission, and administrators.
// Verifying grants nothing: the verifier must be able to open the asset, and
// verifying shares it with no one.
//
// A verification past its expiry reads as none (the asset store reads only
// unexpired rows), and the person who verified and the owner are reminded
// once, a week before it expires, by the reminder pass in this package.
package assetverify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/assetstatus"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// ErrInvalid is returned for a verification the store refuses before writing.
var ErrInvalid = errors.New("invalid verification")

// The history values a verification change records.
const (
	valueVerified = "verified"
)

// Verification is one asset's verification to record.
type Verification struct {
	Actor     string
	Note      string
	Version   int
	ExpiresAt *time.Time
}

// Normalize trims and validates v against now: a note within
// portaldomain.MaxMarkNoteLen, an expiry in the future, a version and an actor.
func (v Verification) Normalize(now time.Time) (Verification, error) {
	v.Note = strings.TrimSpace(v.Note)
	switch {
	case len(v.Note) > portaldomain.MaxMarkNoteLen:
		return v, fmt.Errorf("note is longer than %d characters: %w", portaldomain.MaxMarkNoteLen, ErrInvalid)
	case v.ExpiresAt != nil && !v.ExpiresAt.After(now):
		return v, fmt.Errorf("the expiry must be in the future: %w", ErrInvalid)
	case v.Version < 1:
		return v, fmt.Errorf("no version: %w", ErrInvalid)
	case strings.TrimSpace(v.Actor) == "":
		return v, fmt.Errorf("no actor: %w", ErrInvalid)
	}
	return v, nil
}

// Due is a verification whose reminder is owed.
type Due struct {
	AssetID    string
	VerifiedBy string
	ExpiresAt  time.Time
}

// Store writes verifications.
type Store interface {
	// Verify records v on the asset, replacing any verification it had.
	Verify(ctx context.Context, assetID string, v Verification) error
	// Unverify removes the asset's verification, reporting whether it had
	// one, and who had verified it.
	Unverify(ctx context.Context, assetID, actor string) (bool, error)
	// VerifiedBy is who verified the asset, empty when it carries no
	// unexpired verification.
	VerifiedBy(ctx context.Context, assetID string) (string, error)
	// ClaimDue claims the verifications expiring before horizon and not yet
	// expired at now whose reminder was not sent, marking each sent so one
	// replica sends it.
	ClaimDue(ctx context.Context, now, horizon time.Time, limit int) ([]Due, error)
}

// Postgres is the PostgreSQL Store.
type Postgres struct {
	db  *sql.DB
	now func() time.Time
}

// New returns the PostgreSQL store over db.
func New(db *sql.DB) *Postgres { return &Postgres{db: db, now: time.Now} }

const (
	upsertVerificationSQL = `INSERT INTO portal_asset_verifications
	(asset_id, verified_by, verified_at, verified_version, note, expires_at, reminded_at)
	VALUES ($1, $2, NOW(), $3, $4, $5, NULL)
	ON CONFLICT (asset_id) DO UPDATE SET verified_by = EXCLUDED.verified_by, verified_at = EXCLUDED.verified_at,
		verified_version = EXCLUDED.verified_version, note = EXCLUDED.note, expires_at = EXCLUDED.expires_at,
		reminded_at = NULL`
	deleteVerificationSQL = `DELETE FROM portal_asset_verifications WHERE asset_id = $1 RETURNING verified_by`
	verifiedBySQL         = `SELECT verified_by FROM portal_asset_verifications
	WHERE asset_id = $1 AND (expires_at IS NULL OR expires_at > NOW())`
	claimDueSQL = `UPDATE portal_asset_verifications SET reminded_at = $1
	WHERE asset_id IN (
		SELECT asset_id FROM portal_asset_verifications
		WHERE expires_at IS NOT NULL AND reminded_at IS NULL AND expires_at > $1 AND expires_at <= $2
		ORDER BY expires_at LIMIT $3 FOR UPDATE SKIP LOCKED)
	RETURNING asset_id, verified_by, expires_at`
)

// Verify implements Store.
func (p *Postgres) Verify(ctx context.Context, assetID string, v Verification) error {
	v, err := v.Normalize(p.now())
	if err != nil {
		return err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, upsertVerificationSQL, assetID, v.Actor, v.Version, v.Note, v.ExpiresAt); err != nil {
		return fmt.Errorf("verifying asset: %w", err)
	}
	note := v.Note
	if v.ExpiresAt != nil {
		note = strings.TrimSpace(note + " (expires " + v.ExpiresAt.UTC().Format(time.DateOnly) + ")")
	}
	event := assetstatus.Event{Kind: assetstatus.KindVerification, To: valueVerified, Note: note, Actor: v.Actor}
	if err := assetstatus.RecordEvent(ctx, tx, assetID, event); err != nil {
		return err //nolint:wrapcheck // RecordEvent names the change
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing verification: %w", err)
	}
	return nil
}

// Unverify implements Store.
func (p *Postgres) Unverify(ctx context.Context, assetID, actor string) (bool, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("starting transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var by string
	err = tx.QueryRowContext(ctx, deleteVerificationSQL, assetID).Scan(&by)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("removing verification: %w", err)
	}
	event := assetstatus.Event{Kind: assetstatus.KindVerification, From: valueVerified, Actor: actor}
	if err := assetstatus.RecordEvent(ctx, tx, assetID, event); err != nil {
		return false, err //nolint:wrapcheck // RecordEvent names the change
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("committing removal: %w", err)
	}
	return true, nil
}

// VerifiedBy implements Store.
func (p *Postgres) VerifiedBy(ctx context.Context, assetID string) (string, error) {
	var by string
	err := p.db.QueryRowContext(ctx, verifiedBySQL, assetID).Scan(&by)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading verification: %w", err)
	}
	return by, nil
}

// ClaimDue implements Store.
func (p *Postgres) ClaimDue(ctx context.Context, now, horizon time.Time, limit int) ([]Due, error) {
	rows, err := p.db.QueryContext(ctx, claimDueSQL, now, horizon, limit)
	if err != nil {
		return nil, fmt.Errorf("claiming reminders: %w", err)
	}
	defer rows.Close() //nolint:errcheck // cleanup after the claim
	var due []Due
	for rows.Next() {
		var d Due
		if err := rows.Scan(&d.AssetID, &d.VerifiedBy, &d.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scanning reminder: %w", err)
		}
		due = append(due, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating reminders: %w", err)
	}
	return due, nil
}
