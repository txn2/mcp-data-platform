// Package accessreq is the request for access a signed-in person makes from
// a link to an asset, collection or prompt they cannot open (#2090), and the
// owner's answer to it. Approving creates an ordinary person share, so the
// link the requester followed opens with no further step; declining closes
// the request. A requester has one pending request per item at a time, asks a
// few times a day at most, and a request expires after the access_requests
// setting's period.
package accessreq

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Statuses.
const (
	StatusPending  = "pending"
	StatusApproved = "approved"
	StatusDeclined = "declined"
	// StatusExpired is reported, never stored: a pending request past its
	// expiry reads as expired.
	StatusExpired = "expired"
)

// Target types.
const (
	TargetAsset      = "asset"
	TargetCollection = "collection"
	TargetPrompt     = "prompt"
)

// SettingsSection is the platform_settings section holding Settings.
const SettingsSection = "access_requests"

// Defaults and bounds.
const (
	DefaultExpireDays = 14
	maxExpireDays     = 365
	// MaxPerDay is how many requests one person makes for one item in a day.
	MaxPerDay     = 3
	maxMessageLen = 1000
)

// Errors a caller maps to a status.
var (
	ErrNotFound = errors.New("access request not found")
	ErrTooMany  = errors.New("you have asked for access to this item too often today; try again tomorrow")
	ErrDecided  = errors.New("the request was already answered")
)

// Request is one request for access.
type Request struct {
	ID             string     `json:"id"`
	TargetType     string     `json:"target_type" example:"asset"`
	TargetID       string     `json:"target_id"`
	RequesterID    string     `json:"requester_id"`
	RequesterEmail string     `json:"requester_email"`
	OwnerEmail     string     `json:"owner_email"`
	Message        string     `json:"message,omitempty"`
	Status         string     `json:"status" example:"pending"`
	Permission     string     `json:"permission,omitempty" example:"viewer"`
	DecidedBy      string     `json:"decided_by,omitempty"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	ShareID        string     `json:"share_id,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	// TargetName is the item's name, filled for its owner.
	TargetName string `json:"target_name,omitempty"`
}

// Settings is the stored configuration.
type Settings struct {
	// RevealNames shows the item's name on the page a person who cannot
	// open it sees; otherwise the page has a generic title.
	RevealNames bool `json:"reveal_names" example:"false"`
	// ExpireDays is how long a request waits for an answer.
	ExpireDays int `json:"expire_days" example:"14"`
}

// Defaults is the configuration before an administrator writes the section.
func Defaults() Settings { return Settings{ExpireDays: DefaultExpireDays} }

// Validate refuses a period out of range.
func (s Settings) Validate() error {
	if s.ExpireDays < 1 || s.ExpireDays > maxExpireDays {
		return fmt.Errorf("expire_days must be between 1 and %d", maxExpireDays)
	}
	return nil
}

// ValidTarget reports whether t is a target type.
func ValidTarget(t string) bool {
	return t == TargetAsset || t == TargetCollection || t == TargetPrompt
}

// CleanMessage trims a requester's note and refuses one too long.
func CleanMessage(m string) (string, error) {
	m = strings.TrimSpace(m)
	if len(m) > maxMessageLen {
		return "", fmt.Errorf("the message is longer than %d characters", maxMessageLen)
	}
	return m, nil
}

// reported is the request's status as read now: a pending request past its
// expiry is expired.
func (r *Request) reported(now time.Time) {
	if r.Status == StatusPending && !now.Before(r.ExpiresAt) {
		r.Status = StatusExpired
	}
}

// Store keeps the requests.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore returns the store over db.
func NewStore(db *sql.DB) *Store { return &Store{db: db, now: time.Now} }

const columns = `id, target_type, target_id, requester_id, requester_email, owner_email, message, status,
	permission, decided_by, decided_at, share_id, created_at, expires_at`

func scan(row interface{ Scan(...any) error }) (*Request, error) {
	var r Request
	var decided sql.NullTime
	if err := row.Scan(&r.ID, &r.TargetType, &r.TargetID, &r.RequesterID, &r.RequesterEmail, &r.OwnerEmail,
		&r.Message, &r.Status, &r.Permission, &r.DecidedBy, &decided, &r.ShareID, &r.CreatedAt, &r.ExpiresAt); err != nil {
		return nil, err //nolint:wrapcheck // the caller names the read
	}
	if decided.Valid {
		r.DecidedAt = &decided.Time
	}
	return &r, nil
}

// Open records a pending request, or returns the one already pending for the
// requester and item with created false. A requester who has asked
// MaxPerDay times for the item in the last day is refused.
func (s *Store) Open(ctx context.Context, r Request) (*Request, bool, error) {
	now := s.now()
	if existing, err := s.Pending(ctx, r.TargetType, r.TargetID, r.RequesterID); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, false, err
	}
	var recent int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM access_requests
		 WHERE target_type = $1 AND target_id = $2 AND requester_id = $3 AND created_at > $4`,
		r.TargetType, r.TargetID, r.RequesterID, now.Add(-24*time.Hour)).Scan(&recent); err != nil {
		return nil, false, fmt.Errorf("counting access requests: %w", err)
	}
	if recent >= MaxPerDay {
		return nil, false, ErrTooMany
	}
	created, err := scan(s.db.QueryRowContext(ctx,
		`INSERT INTO access_requests (id, target_type, target_id, requester_id, requester_email, owner_email, message, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		 ON CONFLICT DO NOTHING
		 RETURNING `+columns,
		r.ID, r.TargetType, r.TargetID, r.RequesterID, r.RequesterEmail, r.OwnerEmail, r.Message, r.ExpiresAt))
	if errors.Is(err, sql.ErrNoRows) {
		// Another request from the same person landed first.
		existing, perr := s.Pending(ctx, r.TargetType, r.TargetID, r.RequesterID)
		return existing, false, perr
	}
	if err != nil {
		return nil, false, fmt.Errorf("storing access request: %w", err)
	}
	return created, true, nil
}

// Pending reports the live pending request for the requester and item, or
// ErrNotFound.
func (s *Store) Pending(ctx context.Context, targetType, targetID, requesterID string) (*Request, error) {
	r, err := scan(s.db.QueryRowContext(ctx,
		`SELECT `+columns+` FROM access_requests
		 WHERE target_type = $1 AND target_id = $2 AND requester_id = $3 AND status = 'pending' AND expires_at > $4`,
		targetType, targetID, requesterID, s.now()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading access request: %w", err)
	}
	return r, nil
}

// Get returns one request.
func (s *Store) Get(ctx context.Context, id string) (*Request, error) {
	r, err := scan(s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM access_requests WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading access request: %w", err)
	}
	r.reported(s.now())
	return r, nil
}

// ListForOwner returns the live pending requests for items the address owns,
// or for one item when targetType and targetID are set; with all, every
// request on the platform (an administrator's list).
func (s *Store) ListForOwner(ctx context.Context, ownerEmail, targetType, targetID string, all bool) ([]Request, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+columns+` FROM access_requests
		 WHERE status = 'pending' AND expires_at > $1
		   AND ($2 OR lower(owner_email) = lower($3))
		   AND ($4 = '' OR (target_type = $4 AND target_id = $5))
		 ORDER BY created_at DESC LIMIT 200`,
		s.now(), all, ownerEmail, targetType, targetID)
	if err != nil {
		return nil, fmt.Errorf("listing access requests: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort close after the read
	out := make([]Request, 0)
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("reading access request: %w", err)
		}
		out = append(out, *r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing access requests: %w", err)
	}
	return out, nil
}

// Decision is the answer to a request: approved with a permission and the
// share it created, or declined.
type Decision struct {
	Status     string
	Permission string
	DecidedBy  string
	ShareID    string
}

// Decide records the answer to a pending request, once.
func (s *Store) Decide(ctx context.Context, id string, d Decision) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE access_requests SET status = $2, permission = $3, decided_by = $4, decided_at = $5, share_id = $6
		 WHERE id = $1 AND status = 'pending'`,
		id, d.Status, d.Permission, d.DecidedBy, s.now(), d.ShareID)
	if err != nil {
		return fmt.Errorf("answering access request: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrDecided
	}
	return nil
}
