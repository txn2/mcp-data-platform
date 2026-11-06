// Package eppreview is a form's preview (#2494): its owner, or an
// administrator, sees the form rendered as /forms/<endpoint> renders it, from
// a saved version or an unsaved source, with no endpoint bound and nobody
// else involved.
//
// Opening a preview stores one row naming the script, the version or the
// source, and who opened it, with the identity they opened it with; it
// expires within the hour. Its page (internal/endpoint/epformpage) renders
// only for that person, and its spec and submissions are answered here, by
// the endpoint runner's own rules (eprun.ServePreview), with form_spec and
// form_submit run as drafts under that identity: the draft's write barrier
// stops every write, and nothing a submission carries is stored, saved,
// notified or recorded.
package eppreview

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// TTL is how long a preview can be opened after it was created.
const TTL = 30 * time.Minute

// The routes a preview is reached at. An endpoint's name never starts with
// "_", so neither is ever an endpoint's or a form page's address.
const (
	PagePrefix = "/forms/_preview/"
	APIPrefix  = "/endpoints/_preview/"
)

// ErrNotFound is a preview that does not exist or has expired.
var ErrNotFound = errors.New("eppreview: no such preview")

// ErrNotAllowed is a preview opened by someone who may not open it.
var ErrNotAllowed = errors.New("eppreview: not allowed")

// Opener is who opens a preview, and the identity its drafts run as.
type Opener struct {
	UserID   string
	Email    string
	Roles    []string
	AuthType string
	Admin    bool
}

// Record is one preview.
type Record struct {
	ID       string
	ScriptID string
	// Version is the saved version rendered, 0 for Source.
	Version int
	// Source is the unsaved source rendered, empty for Version.
	Source    string
	CreatedBy string
	UserID    string
	Roles     []string
	AuthType  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Store keeps previews.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore builds the store over db.
func NewStore(db *sql.DB) *Store { return &Store{db: db, now: time.Now} }

// Insert stores a preview, removing the expired ones first.
func (s *Store) Insert(ctx context.Context, r *Record) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM form_previews WHERE expires_at < $1`, s.now()); err != nil {
		return fmt.Errorf("removing expired previews: %w", err)
	}
	roles, err := json.Marshal(nonNil(r.Roles))
	if err != nil {
		return fmt.Errorf("encoding the preview's roles: %w", err)
	}
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO form_previews (id, script_id, version, source, created_by, user_id, roles, auth_type, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at`,
		r.ID, r.ScriptID, r.Version, r.Source, r.CreatedBy, r.UserID, roles, r.AuthType, r.ExpiresAt,
	).Scan(&r.CreatedAt)
	if err != nil {
		return fmt.Errorf("storing the preview: %w", err)
	}
	return nil
}

// Get reads a preview that has not expired.
func (s *Store) Get(ctx context.Context, id string) (*Record, error) {
	var (
		r     Record
		roles []byte
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, script_id, version, source, created_by, user_id, roles, auth_type, created_at, expires_at
		  FROM form_previews WHERE id = $1 AND expires_at >= $2`, id, s.now()).
		Scan(&r.ID, &r.ScriptID, &r.Version, &r.Source, &r.CreatedBy, &r.UserID, &roles, &r.AuthType, &r.CreatedAt, &r.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading the preview: %w", err)
	}
	if err := json.Unmarshal(roles, &r.Roles); err != nil {
		return nil, fmt.Errorf("decoding the preview's roles: %w", err)
	}
	return &r, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// newID is a preview's id: unguessable, and never an endpoint name.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "fp" + hex.EncodeToString(b[:])
}

// Owner is the address of the person who opened a preview, which is the one
// person its page renders for (internal/endpoint/epformpage).
func (s *Store) Owner(ctx context.Context, id string) (string, error) {
	r, err := s.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return r.CreatedBy, nil
}
