package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

// Sentinel errors returned by Store implementations.
var (
	// ErrNotFound is returned when a directory user does not exist.
	ErrNotFound = errors.New("user not found")
	// ErrAlreadyExists is returned by Insert when the email is already present.
	ErrAlreadyExists = errors.New("user already exists")
)

// DefaultListLimit caps a List query that supplies no limit.
const DefaultListLimit = 100

// MaxListLimit is the hard upper bound on a List page size. It bounds the
// response size for the directory endpoints (the portal picker is readable by
// any authenticated user, so an unbounded limit would let one request dump the
// whole directory).
const MaxListLimit = 100

// pgUniqueViolation is the PostgreSQL error code for a unique-constraint
// violation (primary key collision on email).
const pgUniqueViolation = "23505"

// Store persists and queries the known-users directory.
type Store interface {
	// Observe records a person seen via a real authenticated session. It
	// inserts a new confirmed row or, on conflict, fills ONLY blank name
	// fields (admin-entered names win) and stamps last_seen_at + confirmed.
	// firstName/lastName may be empty.
	Observe(ctx context.Context, email, firstName, lastName string) error
	// Insert adds a directory row, returning ErrAlreadyExists if the email is
	// already present. Used by the admin pre-add path.
	Insert(ctx context.Context, u User) error
	// Get returns a single user by email, or ErrNotFound.
	Get(ctx context.Context, email string) (*User, error)
	// List returns directory users matching the filter plus the total count.
	List(ctx context.Context, filter Filter) ([]User, int, error)
	// Update applies the non-nil fields of u, returning ErrNotFound if absent.
	Update(ctx context.Context, email string, u Update) error
	// Delete removes a user by email, returning ErrNotFound if absent.
	Delete(ctx context.Context, email string) error
}

// PostgresStore implements Store backed by PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgreSQL-backed user directory store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Observe upserts a person seen via authentication. On conflict it fills only
// the blank name fields so an admin-entered name is never overwritten, and it
// always marks the row confirmed and bumps last_seen_at.
func (s *PostgresStore) Observe(ctx context.Context, email, firstName, lastName string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (email, first_name, last_name, source, confirmed, last_seen_at)
		 VALUES ($1, $2, $3, 'auth', TRUE, NOW())
		 ON CONFLICT (email) DO UPDATE SET
		   first_name   = CASE WHEN users.first_name = '' THEN EXCLUDED.first_name ELSE users.first_name END,
		   last_name    = CASE WHEN users.last_name  = '' THEN EXCLUDED.last_name  ELSE users.last_name  END,
		   confirmed    = TRUE,
		   last_seen_at = NOW(),
		   updated_at   = NOW()`,
		email, firstName, lastName)
	if err != nil {
		return fmt.Errorf("observing user: %w", err)
	}
	return nil
}

// Insert adds a new directory row. The source defaults to 'admin' when unset,
// matching the pre-add use case.
func (s *PostgresStore) Insert(ctx context.Context, u User) error {
	source := u.Source
	if source == "" {
		source = SourceAdmin
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO users (email, first_name, last_name, source, confirmed, added_by)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		u.Email, u.FirstName, u.LastName, source, u.Confirmed, u.AddedBy)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && string(pqErr.Code) == pgUniqueViolation {
			return ErrAlreadyExists
		}
		return fmt.Errorf("inserting user: %w", err)
	}
	return nil
}

// Get returns a single directory user by email.
func (s *PostgresStore) Get(ctx context.Context, email string) (*User, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT email, first_name, last_name, source, confirmed, added_by,
		        last_seen_at, created_at, updated_at
		 FROM users WHERE email = $1`, email)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying user: %w", err)
	}
	return u, nil
}

// List returns directory users matching the filter, ordered by last name then
// first name then email, plus the total count of matches (before the
// limit/offset window).
func (s *PostgresStore) List(ctx context.Context, filter Filter) ([]User, int, error) {
	where, args := buildUserWhere(filter)

	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM users"+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting users: %w", err)
	}
	if total == 0 {
		return nil, 0, nil
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	} else if limit > MaxListLimit {
		limit = MaxListLimit
	}
	query := fmt.Sprintf( // #nosec G201 -- `where` holds only $N placeholders; all user input is parameterized via args
		`SELECT email, first_name, last_name, source, confirmed, added_by,
		        last_seen_at, created_at, updated_at
		 FROM users%s
		 ORDER BY last_name, first_name, email
		 LIMIT $%d OFFSET $%d`, where, len(args)+1, len(args)+2)
	args = append(args, limit, filter.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var users []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scanning user row: %w", err)
		}
		users = append(users, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterating user rows: %w", err)
	}
	return users, total, nil
}

// Update applies the non-nil fields of u to the named user.
func (s *PostgresStore) Update(ctx context.Context, email string, u Update) error {
	setClauses := []string{"updated_at = NOW()"}
	var args []any
	idx := 1

	if u.FirstName != nil {
		setClauses = append(setClauses, fmt.Sprintf("first_name = $%d", idx))
		args = append(args, *u.FirstName)
		idx++
	}
	if u.LastName != nil {
		setClauses = append(setClauses, fmt.Sprintf("last_name = $%d", idx))
		args = append(args, *u.LastName)
		idx++
	}

	query := fmt.Sprintf("UPDATE users SET %s WHERE email = $%d", // #nosec G201 -- dynamic SET clause with parameterized values
		strings.Join(setClauses, ", "), idx)
	args = append(args, email)

	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("updating user: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a user by email.
func (s *PostgresStore) Delete(ctx context.Context, email string) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM users WHERE email = $1", email)
	if err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanUser reads a full user row.
func scanUser(row rowScanner) (*User, error) {
	var u User
	var lastSeen sql.NullTime
	if err := row.Scan(&u.Email, &u.FirstName, &u.LastName, &u.Source,
		&u.Confirmed, &u.AddedBy, &lastSeen, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, err //nolint:wrapcheck // callers add context per call site
	}
	if lastSeen.Valid {
		u.LastSeenAt = &lastSeen.Time
	}
	return &u, nil
}

// buildUserWhere builds an optional WHERE clause for the search filter.
func buildUserWhere(filter Filter) (where string, args []any) {
	if strings.TrimSpace(filter.Query) == "" {
		return "", nil
	}
	// Escape the LIKE escape character (backslash) first, then the wildcards.
	// strings.NewReplacer applies the leftmost-longest match in a single pass
	// and never re-scans replacement output, so the inserted backslashes are
	// not themselves re-escaped. Omitting the backslash escape lets a query
	// ending in "\" produce a dangling escape that Postgres rejects ("LIKE
	// pattern must not end with escape character") — a user-triggerable 500.
	escaped := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.TrimSpace(filter.Query))
	pattern := "%" + escaped + "%"
	where = " WHERE (email ILIKE $1 OR first_name ILIKE $1 OR last_name ILIKE $1)"
	return where, []any{pattern}
}

// Verify interface compliance.
var _ Store = (*PostgresStore)(nil)
