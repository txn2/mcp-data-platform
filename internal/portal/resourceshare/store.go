// Package resourceshare shares managed resources the way assets are shared
// (#2258): with a person, an address or a persona, as viewer or editor, in
// portal_shares beside asset, collection and prompt shares. A share is
// addressed to whoever it names when they open the resource, so a share to a
// persona reaches the people whose roles resolve to it then.
//
// The resources API reads a caller's share on every request through Lookup,
// which is what lets a recipient read a resource outside every folder they
// can see, and an editor change it. Deleting the resource removes its shares
// (the column cascades).
package resourceshare

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// recipientAt1 is the predicate a share names the caller by: their user id
// ($1), their address ($2), or the persona they resolve to now ($3).
// recipientAt2 is the same with the arguments from $2.
const (
	recipientAt1 = `( ($1 <> '' AND shared_with_user_id = $1)
	OR ($2 <> '' AND LOWER(shared_with_email) = LOWER($2))
	OR ($3 <> '' AND shared_with_persona = $3) )`
	recipientAt2 = `( ($2 <> '' AND shared_with_user_id = $2)
	OR ($3 <> '' AND LOWER(shared_with_email) = LOWER($3))
	OR ($4 <> '' AND shared_with_persona = $4) )`
)

// active is a share that still grants: not revoked, not expired.
const active = `revoked = FALSE AND (expires_at IS NULL OR expires_at > NOW())`

// editorFirst orders an editor share before a viewer one.
const editorFirst = `(permission = 'editor') DESC`

const (
	insertQuery = `INSERT INTO portal_shares
		(id, resource_id, token, created_by, shared_with_user_id, shared_with_email, shared_with_persona,
		 permission, origin, access_mode, notice_text, posted_to)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7, $8, $9, $10, $11, $12)
		RETURNING created_at`
	listQuery = `SELECT id, created_by, COALESCE(shared_with_user_id, ''), COALESCE(shared_with_email, ''),
		shared_with_persona, permission, origin, access_mode, posted_to, created_at
		FROM portal_shares WHERE resource_id = $1 AND ` + active + ` ORDER BY created_at DESC`
	revokeQuery = `UPDATE portal_shares SET revoked = TRUE WHERE id = $1 AND resource_id = $2 AND revoked = FALSE`
)

const (
	// permissionQuery is the highest permission a share gives the recipient
	// on a resource: one addressed to them, or, for a person of an internal
	// persona ($5), a link share any signed-in person opens, which is what a
	// file posted to a channel is shared by.
	permissionQuery = `SELECT permission FROM portal_shares
		WHERE resource_id = $1 AND ` + active + ` AND (` + recipientAt2 + `
		   OR ($5 AND access_mode = 'authenticated' AND shared_with_user_id IS NULL
		       AND shared_with_email IS NULL AND shared_with_persona = ''))
		ORDER BY ` + editorFirst + ` LIMIT 1`
	sharedWithQuery = `SELECT DISTINCT ON (resource_id) resource_id, id, created_by, created_at, permission,
		CASE WHEN $3 <> '' AND shared_with_persona = $3 THEN shared_with_persona ELSE '' END
		FROM portal_shares WHERE resource_id IS NOT NULL AND ` + active + ` AND ` + recipientAt1 + `
		ORDER BY resource_id, ` + editorFirst + `, created_at DESC`
)

// ErrNotFound is a share that is not on the resource or is already revoked.
var ErrNotFound = errors.New("share not found")

// Recipient is who a lookup is for.
type Recipient struct {
	UserID  string
	Email   string
	Persona string
	// Internal is a person of an internal persona, whom a link share any
	// signed-in person opens reaches; a person of an external persona sees
	// only what is shared with them.
	Internal bool
}

// Ref is one resource shared with a recipient.
type Ref struct {
	ResourceID string
	ShareID    string
	SharedBy   string
	SharedAt   time.Time
	Permission string
	// ViaPersona names the persona the share reached them through.
	ViaPersona string
}

// Store reads and writes resource shares.
type Store struct{ db *sql.DB }

// NewStore returns the store over db.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// Insert writes a share built by portaldomain.BuildShare with a ResourceID
// target, filling CreatedAt from the row.
func (s *Store) Insert(ctx context.Context, share *portaldomain.Share) error {
	origin := share.Origin
	if origin == "" {
		origin = portaldomain.OriginExplicit
	}
	err := s.db.QueryRowContext(ctx, insertQuery, share.ID, share.ResourceID, share.Token, share.CreatedBy,
		share.SharedWithUserID, share.SharedWithEmail, share.SharedWithPersona, string(share.Permission),
		string(origin), string(share.AccessMode), share.NoticeText, share.PostedTo,
	).Scan(&share.CreatedAt)
	if err != nil {
		return fmt.Errorf("inserting resource share: %w", err)
	}
	return nil
}

// List returns the shares that still grant on a resource, newest first.
func (s *Store) List(ctx context.Context, resourceID string) ([]portaldomain.Share, error) {
	rows, err := s.db.QueryContext(ctx, listQuery, resourceID)
	if err != nil {
		return nil, fmt.Errorf("listing resource shares: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []portaldomain.Share{}
	for rows.Next() {
		sh := portaldomain.Share{ResourceID: resourceID}
		if err := rows.Scan(&sh.ID, &sh.CreatedBy, &sh.SharedWithUserID, &sh.SharedWithEmail,
			&sh.SharedWithPersona, &sh.Permission, &sh.Origin, &sh.AccessMode, &sh.PostedTo, &sh.CreatedAt); err != nil {
			return nil, fmt.Errorf("reading resource share: %w", err)
		}
		out = append(out, sh)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing resource shares: %w", err)
	}
	return out, nil
}

// Permission is the highest permission a share gives the recipient on a
// resource: "editor", "viewer", or "" for none.
func (s *Store) Permission(ctx context.Context, resourceID string, rcpt Recipient) (string, error) {
	var perm string
	err := s.db.QueryRowContext(ctx, permissionQuery, resourceID, rcpt.UserID, rcpt.Email, rcpt.Persona, rcpt.Internal).Scan(&perm)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading resource share permission: %w", err)
	}
	return perm, nil
}

// SharedWith returns the resources shared with the recipient, one row each
// with the highest permission's share.
func (s *Store) SharedWith(ctx context.Context, rcpt Recipient) ([]Ref, error) {
	rows, err := s.db.QueryContext(ctx, sharedWithQuery, rcpt.UserID, rcpt.Email, rcpt.Persona)
	if err != nil {
		return nil, fmt.Errorf("listing resources shared with the caller: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []Ref{}
	for rows.Next() {
		var ref Ref
		if err := rows.Scan(&ref.ResourceID, &ref.ShareID, &ref.SharedBy, &ref.SharedAt, &ref.Permission, &ref.ViaPersona); err != nil {
			return nil, fmt.Errorf("reading a resource shared with the caller: %w", err)
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing resources shared with the caller: %w", err)
	}
	return out, nil
}

// Revoke ends a share on a resource.
func (s *Store) Revoke(ctx context.Context, resourceID, shareID string) error {
	res, err := s.db.ExecContext(ctx, revokeQuery, shareID, resourceID)
	if err != nil {
		return fmt.Errorf("revoking resource share: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return ErrNotFound
	}
	return nil
}
