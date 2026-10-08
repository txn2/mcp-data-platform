// Package secretstore holds the secrets a request references by placeholder
// (#2051): the record an administrator writes, the rules it is held to, its
// PostgreSQL store with the value encrypted, and the lookup the api gateway
// fills a placeholder through.
//
// The value is write-only. No method returns it except the lookup, which the
// gateway calls as it sends a request, and the lookup reads it from the
// database on every call, so a rotated value is used from the next call on.
package secretstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/internal/secretref"
)

// ErrNotFound is returned for a name no secret is stored under.
var ErrNotFound = errors.New("secret not found")

// ErrInvalid is wrapped by every refusal of a write.
var ErrInvalid = errors.New("invalid secret")

// Limits on what a write may carry.
const (
	MaxValueBytes       = 64 * 1024
	MaxDescriptionBytes = 1000
)

// Secret is a stored secret as every reader but the gateway sees it: what it
// is for and where it may go, never its value.
type Secret struct {
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	AllowConnections []string  `json:"allow_connections"`
	AllowPersonas    []string  `json:"allow_personas"`
	CreatedBy        string    `json:"created_by"`
	UpdatedBy        string    `json:"updated_by"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Write is one create or update. Value nil on an update keeps the stored
// value, so an administrator can rescope a secret without retyping it.
type Write struct {
	Name             string
	Description      string
	Value            *string
	AllowConnections []string
	AllowPersonas    []string
	Actor            string
}

// Validate refuses a write the store would hold wrongly. creating says
// whether no secret is stored under the name yet, in which case a value is
// required.
func Validate(w Write, creating bool) error {
	if problem := invalidValue(w.Value, creating); problem != "" {
		return fmt.Errorf("the value is refused, %s: %w", problem, ErrInvalid)
	}
	switch {
	case !secretref.ValidName(w.Name):
		return fmt.Errorf("name %q must be lower case letters, digits, '.', '_' and '-', starting with a letter or digit, at most 63 characters: %w", w.Name, ErrInvalid)
	case len(w.Description) > MaxDescriptionBytes:
		return fmt.Errorf("description is longer than %d bytes: %w", MaxDescriptionBytes, ErrInvalid)
	case len(w.AllowConnections) == 0:
		return fmt.Errorf("allow_connections must name at least one connection, since a secret is only ever sent through the connections it names: %w", ErrInvalid)
	}
	for _, list := range [][]string{w.AllowConnections, w.AllowPersonas} {
		if slices.ContainsFunc(list, func(s string) bool { return strings.TrimSpace(s) == "" }) {
			return fmt.Errorf("allow_connections and allow_personas may not hold an empty name: %w", ErrInvalid)
		}
	}
	return nil
}

// invalidValue is what is wrong with a write's value, or "".
func invalidValue(value *string, creating bool) string {
	switch {
	case value == nil && creating:
		return "value is required when a secret is created"
	case value == nil:
		return ""
	case len(*value) < secretref.MinValueLength:
		return fmt.Sprintf("value must be at least %d characters, since every occurrence of it is redacted from responses and a shorter one would rewrite ordinary text", secretref.MinValueLength)
	case len(*value) > MaxValueBytes:
		return fmt.Sprintf("value is longer than %d bytes", MaxValueBytes)
	}
	return ""
}

// Encryptor encrypts and decrypts one value. fieldcrypt.RestFieldEncryptor
// satisfies it; a nil one stores values as written.
type Encryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// Store persists secrets in gateway_secrets.
type Store struct {
	db  *sql.DB
	enc Encryptor
	// admin is the administrator persona, which allow_personas never
	// refuses: the administrator is not limited (WithAdmin).
	admin string
}

// NewStore creates a store. enc may be nil.
func NewStore(db *sql.DB, enc Encryptor) *Store {
	return &Store{db: db, enc: enc}
}

// WithAdmin names the administrator persona the lookup lets through
// allow_personas, and returns the store.
func (s *Store) WithAdmin(persona string) *Store {
	s.admin = persona
	return s
}

// metaColumns are every column but the value.
const metaColumns = `name, description, allow_connections, allow_personas, created_by, updated_by, created_at, updated_at`

// scanner is a *sql.Row or *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

// scanMeta reads the metaColumns.
func scanMeta(row scanner, extra ...any) (Secret, error) {
	var s Secret
	var conns, personas pq.StringArray
	dest := append([]any{&s.Name, &s.Description, &conns, &personas, &s.CreatedBy, &s.UpdatedBy, &s.CreatedAt, &s.UpdatedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return Secret{}, err //nolint:wrapcheck // callers wrap, and test for sql.ErrNoRows
	}
	s.AllowConnections = append(make([]string, 0, len(conns)), conns...)
	s.AllowPersonas = append(make([]string, 0, len(personas)), personas...)
	return s, nil
}

// List returns every secret in name order, without values.
func (s *Store) List(ctx context.Context) ([]Secret, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+metaColumns+` FROM gateway_secrets ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing secrets: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Secret, 0)
	for rows.Next() {
		sec, err := scanMeta(rows)
		if err != nil {
			return nil, fmt.Errorf("reading a secret: %w", err)
		}
		out = append(out, sec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating secrets: %w", err)
	}
	return out, nil
}

// Get returns one secret without its value, or ErrNotFound.
func (s *Store) Get(ctx context.Context, name string) (Secret, error) {
	sec, err := scanMeta(s.db.QueryRowContext(ctx, `SELECT `+metaColumns+` FROM gateway_secrets WHERE name = $1`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return Secret{}, ErrNotFound
	}
	if err != nil {
		return Secret{}, fmt.Errorf("reading secret %q: %w", name, err)
	}
	return sec, nil
}

// Put creates or updates a secret and returns it as stored, and whether it
// was created.
func (s *Store) Put(ctx context.Context, w Write) (Secret, bool, error) {
	_, err := s.Get(ctx, w.Name)
	creating := errors.Is(err, ErrNotFound)
	if err != nil && !creating {
		return Secret{}, false, err
	}
	if err := Validate(w, creating); err != nil {
		return Secret{}, false, err
	}
	var value sql.NullString
	if w.Value != nil {
		enc, err := s.encrypt(*w.Value)
		if err != nil {
			return Secret{}, false, err
		}
		value = sql.NullString{String: enc, Valid: true}
	}
	if creating {
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO gateway_secrets (name, description, value, allow_connections, allow_personas, created_by, updated_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $6)`,
			w.Name, w.Description, value.String, pq.Array(w.AllowConnections), pq.Array(nonNil(w.AllowPersonas)), w.Actor)
	} else {
		_, err = s.db.ExecContext(ctx,
			`UPDATE gateway_secrets SET description = $2, value = COALESCE($3, value), allow_connections = $4,
			 allow_personas = $5, updated_by = $6, updated_at = NOW() WHERE name = $1`,
			w.Name, w.Description, value, pq.Array(w.AllowConnections), pq.Array(nonNil(w.AllowPersonas)), w.Actor)
	}
	if err != nil {
		return Secret{}, false, fmt.Errorf("writing secret %q: %w", w.Name, err)
	}
	sec, err := s.Get(ctx, w.Name)
	return sec, creating, err
}

// nonNil is list, or an empty list for nil, so an absent persona list is
// stored as '{}' and not NULL.
func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// Delete removes a secret, or returns ErrNotFound.
func (s *Store) Delete(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM gateway_secrets WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("deleting secret %q: %w", name, err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// encrypt encrypts a value, or passes it through with no encryptor.
func (s *Store) encrypt(v string) (string, error) {
	if s.enc == nil {
		return v, nil
	}
	out, err := s.enc.Encrypt(v)
	if err != nil {
		return "", fmt.Errorf("encrypting secret value: %w", err)
	}
	return out, nil
}

// withValue reads a secret and its decrypted value.
func (s *Store) withValue(ctx context.Context, name string) (Secret, string, error) {
	var stored string
	sec, err := scanMeta(s.db.QueryRowContext(ctx, `SELECT `+metaColumns+`, value FROM gateway_secrets WHERE name = $1`, name), &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Secret{}, "", ErrNotFound
	}
	if err != nil {
		return Secret{}, "", fmt.Errorf("reading secret %q: %w", name, err)
	}
	if s.enc == nil {
		return sec, stored, nil
	}
	value, err := s.enc.Decrypt(stored)
	if err != nil {
		return Secret{}, "", fmt.Errorf("decrypting secret %q: %w", name, err)
	}
	return sec, value, nil
}
