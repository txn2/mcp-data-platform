// Package secretstore holds the secrets a request references by placeholder
// (#2051): the record an administrator writes, the rules it is held to, its
// PostgreSQL store with the value encrypted, and the lookup the api gateway
// fills a placeholder through.
//
// The value is write-only. No method returns it except the lookup, which the
// gateway calls as it sends a request, and the lookup reads it from the
// database on every call, so a rotated value is used from the next call on.
//
// A secret is one of two kinds (#2065): a value, sent where {{secret:<name>}}
// is written, or an authenticator seed, never sent, whose current one-time
// code is sent where {{totp:<name>}} is written.
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
	"github.com/txn2/mcp-data-platform/internal/totp"
)

// ErrNotFound is returned for a name no secret is stored under.
var ErrNotFound = errors.New("secret not found")

// ErrInvalid is wrapped by every refusal of a write.
var ErrInvalid = errors.New("invalid secret")

// The kinds a secret may be.
const (
	// KindValue is sent as written where {{secret:<name>}} is.
	KindValue = "value"
	// KindTOTP is an authenticator seed (RFC 6238): never sent, and its
	// current code is sent where {{totp:<name>}} is (#2065).
	KindTOTP = "totp"
)

// Limits on what a write may carry.
const (
	MaxValueBytes       = 64 * 1024
	MaxDescriptionBytes = 1000
)

// Secret is a stored secret as every reader but the gateway sees it: what it
// is for and where it may go, never its value.
type Secret struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Kind is "value" or "totp".
	Kind string `json:"kind" enums:"value,totp"`
	// TOTP is a totp secret's code parameters, read from the otpauth URI it
	// was saved as; absent for a value secret. The seed is never returned.
	TOTP             *totp.Params `json:"totp,omitempty"`
	AllowConnections []string     `json:"allow_connections"`
	AllowPersonas    []string     `json:"allow_personas"`
	CreatedBy        string       `json:"created_by"`
	UpdatedBy        string       `json:"updated_by"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
}

// Write is one create or update. Value nil on an update keeps the stored
// value, so an administrator can rescope a secret without retyping it. Kind
// empty is a value secret on a create and the stored kind on an update; a
// totp secret's Value is an otpauth://totp URI or a bare base32 seed.
type Write struct {
	Name             string
	Description      string
	Kind             string
	Value            *string
	AllowConnections []string
	AllowPersonas    []string
	Actor            string
}

// Validate refuses a write the store would hold wrongly. creating says
// whether no secret is stored under the name yet, in which case a value is
// required.
func Validate(w Write, creating bool) error {
	switch w.Kind {
	case "", KindValue, KindTOTP:
	default:
		return fmt.Errorf("kind %q is not value or totp: %w", w.Kind, ErrInvalid)
	}
	if err := validateValue(w, creating); err != nil {
		return err
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

// validateValue refuses a write's value: an authenticator seed that does not
// parse, or a value secret's value outside its bounds.
func validateValue(w Write, creating bool) error {
	if w.Kind != KindTOTP {
		if problem := invalidValue(w.Value, creating); problem != "" {
			return fmt.Errorf("the value is refused, %s: %w", problem, ErrInvalid)
		}
		return nil
	}
	if w.Value == nil {
		if creating {
			return fmt.Errorf("the authenticator seed is required when a totp secret is created: %w", ErrInvalid)
		}
		return nil
	}
	if _, _, err := totp.Parse(*w.Value); err != nil {
		return fmt.Errorf("the authenticator seed is refused, %w: %w", err, ErrInvalid)
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
	// now and sleep are the clock a one-time code is issued by and the wait
	// for the next period (WithClock).
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

// NewStore creates a store. enc may be nil.
func NewStore(db *sql.DB, enc Encryptor) *Store {
	return &Store{db: db, enc: enc, now: time.Now, sleep: sleepCtx}
}

// WithClock replaces the clock and the wait a one-time code is issued by,
// and returns the store. A test passes a clock it moves itself, so no test
// waits out a period.
func (s *Store) WithClock(now func() time.Time, sleep func(ctx context.Context, d time.Duration) error) *Store {
	s.now, s.sleep = now, sleep
	return s
}

// sleepCtx waits d, or until ctx ends.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for the next one-time code period: %w", ctx.Err())
	}
}

// WithAdmin names the administrator persona the lookup lets through
// allow_personas, and returns the store.
func (s *Store) WithAdmin(persona string) *Store {
	s.admin = persona
	return s
}

// metaColumns are every column but the value.
const metaColumns = `name, description, allow_connections, allow_personas, created_by, updated_by, created_at, updated_at, kind, totp_algorithm, totp_digits, totp_period`

// scanner is a *sql.Row or *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

// scanMeta reads the metaColumns.
func scanMeta(row scanner, extra ...any) (Secret, error) {
	var s Secret
	var conns, personas pq.StringArray
	var p totp.Params
	dest := append([]any{
		&s.Name, &s.Description, &conns, &personas, &s.CreatedBy, &s.UpdatedBy, &s.CreatedAt, &s.UpdatedAt,
		&s.Kind, &p.Algorithm, &p.Digits, &p.Period,
	}, extra...)
	if err := row.Scan(dest...); err != nil {
		return Secret{}, err //nolint:wrapcheck // callers wrap, and test for sql.ErrNoRows
	}
	s.AllowConnections = append(make([]string, 0, len(conns)), conns...)
	s.AllowPersonas = append(make([]string, 0, len(personas)), personas...)
	if s.Kind == KindTOTP {
		s.TOTP = &p
	}
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
	stored, err := s.Get(ctx, w.Name)
	creating := errors.Is(err, ErrNotFound)
	if err != nil && !creating {
		return Secret{}, false, err
	}
	switch {
	case w.Kind == "" && creating:
		w.Kind = KindValue
	case w.Kind == "":
		w.Kind = stored.Kind
	case !creating && w.Kind != stored.Kind && w.Value == nil:
		return Secret{}, false, fmt.Errorf("changing secret %q from %s to %s needs a new value, since the stored one is a %s: %w", w.Name, stored.Kind, w.Kind, stored.Kind, ErrInvalid)
	}
	if err := Validate(w, creating); err != nil {
		return Secret{}, false, err
	}
	value, params, err := s.storedForm(w)
	if err != nil {
		return Secret{}, false, err
	}
	if creating {
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO gateway_secrets (name, description, value, allow_connections, allow_personas, created_by, updated_by,
			 kind, totp_algorithm, totp_digits, totp_period)
			 VALUES ($1, $2, $3, $4, $5, $6, $6, $7, $8, $9, $10)`,
			w.Name, w.Description, value.String, pq.Array(w.AllowConnections), pq.Array(nonNil(w.AllowPersonas)), w.Actor,
			w.Kind, params.Algorithm, params.Digits, params.Period)
	} else {
		// A new value replaces the kind and the code parameters with it, and
		// a new seed starts its own count of issued periods; a rescope keeps
		// all three.
		_, err = s.db.ExecContext(ctx,
			`UPDATE gateway_secrets SET description = $2, value = COALESCE($3, value), allow_connections = $4,
			 allow_personas = $5, updated_by = $6, updated_at = NOW(),
			 kind = CASE WHEN $3::text IS NULL THEN kind ELSE $7 END,
			 totp_algorithm = CASE WHEN $3::text IS NULL THEN totp_algorithm ELSE $8 END,
			 totp_digits = CASE WHEN $3::text IS NULL THEN totp_digits ELSE $9 END,
			 totp_period = CASE WHEN $3::text IS NULL THEN totp_period ELSE $10 END,
			 totp_last_period = CASE WHEN $3::text IS NULL THEN totp_last_period ELSE 0 END
			 WHERE name = $1`,
			w.Name, w.Description, value, pq.Array(w.AllowConnections), pq.Array(nonNil(w.AllowPersonas)), w.Actor,
			w.Kind, params.Algorithm, params.Digits, params.Period)
	}
	if err != nil {
		return Secret{}, false, fmt.Errorf("writing secret %q: %w", w.Name, err)
	}
	sec, err := s.Get(ctx, w.Name)
	return sec, creating, err
}

// storedForm is the encrypted value a write stores, and its code parameters:
// a totp secret stores its seed in canonical base32, whatever form it was
// pasted in. A write that keeps the stored value stores neither.
func (s *Store) storedForm(w Write) (sql.NullString, totp.Params, error) {
	if w.Value == nil {
		return sql.NullString{}, totp.Params{}, nil
	}
	plain, params := *w.Value, totp.Params{}
	if w.Kind == KindTOTP {
		var err error
		if plain, params, err = totp.Parse(*w.Value); err != nil {
			return sql.NullString{}, totp.Params{}, fmt.Errorf("reading the authenticator seed: %w: %w", err, ErrInvalid)
		}
	}
	enc, err := s.encrypt(plain)
	if err != nil {
		return sql.NullString{}, totp.Params{}, err
	}
	return sql.NullString{String: enc, Valid: true}, params, nil
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
