package whsource

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lib/pq"
)

// ErrNotFound is returned for a name no source is stored under.
var ErrNotFound = errors.New("webhook source not found")

// ErrExists is returned when a source is created under a name already taken.
var ErrExists = errors.New("a webhook source with that name already exists")

// uniqueViolation is the PostgreSQL SQLSTATE for a unique-constraint breach.
const uniqueViolation = "23505"

// Encryptor encrypts and decrypts one secret. fieldcrypt.RestFieldEncryptor
// satisfies it; a nil one stores secrets as written.
type Encryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
}

// Store persists sources in webhook_sources.
type Store struct {
	db  *sql.DB
	enc Encryptor
}

// NewStore creates a source store. enc may be nil.
func NewStore(db *sql.DB, enc Encryptor) *Store {
	return &Store{db: db, enc: enc}
}

const selectColumns = `name, enabled, auth, config, connection_name, created_by, created_at, updated_at`

// List returns every source in name order, secrets decrypted.
func (s *Store) List(ctx context.Context) ([]Source, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+selectColumns+` FROM webhook_sources ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing webhook sources: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make([]Source, 0)
	for rows.Next() {
		src, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating webhook sources: %w", err)
	}
	return out, nil
}

// Get returns one source, or ErrNotFound.
func (s *Store) Get(ctx context.Context, name string) (Source, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+selectColumns+` FROM webhook_sources WHERE name = $1`, name)
	src, err := s.scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	return src, err
}

// Create inserts a new source, or returns ErrExists.
func (s *Store) Create(ctx context.Context, src Source) error {
	auth, cfg, err := s.encode(src)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO webhook_sources (name, enabled, auth, config, connection_name, created_by)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		src.Name, src.Enabled, auth, cfg, src.Connection, src.CreatedBy)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == uniqueViolation {
		return ErrExists
	}
	if err != nil {
		return fmt.Errorf("creating webhook source: %w", err)
	}
	return nil
}

// Update replaces a source's settings. The name, the connection and the
// creator are not changed: the table was created on that connection under a
// name derived from the source's.
func (s *Store) Update(ctx context.Context, src Source) error {
	auth, cfg, err := s.encode(src)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE webhook_sources SET enabled = $2, auth = $3, config = $4, updated_at = NOW() WHERE name = $1`,
		src.Name, src.Enabled, auth, cfg)
	if err != nil {
		return fmt.Errorf("updating webhook source: %w", err)
	}
	return oneRow(res)
}

// Delete removes a source. Its windows, counts and rejections go with it.
func (s *Store) Delete(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM webhook_sources WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("deleting webhook source: %w", err)
	}
	return oneRow(res)
}

// oneRow maps a write that matched nothing to ErrNotFound.
func oneRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// encode renders the auth and config columns, encrypting both secrets.
func (s *Store) encode(src Source) (auth, cfg []byte, err error) {
	a := src.Auth
	if a.Secret, err = s.encrypt(a.Secret); err != nil {
		return nil, nil, err
	}
	if a.PreviousSecret, err = s.encrypt(a.PreviousSecret); err != nil {
		return nil, nil, err
	}
	//nolint:gosec // G117: both secrets were encrypted above; this is the column they are stored in.
	if auth, err = json.Marshal(a); err != nil { // #nosec G117 -- both secrets were encrypted above
		return nil, nil, fmt.Errorf("encoding webhook auth: %w", err)
	}
	if cfg, err = json.Marshal(src.Config); err != nil {
		return nil, nil, fmt.Errorf("encoding webhook config: %w", err)
	}
	return auth, cfg, nil
}

func (s *Store) encrypt(v string) (string, error) {
	if s.enc == nil || v == "" {
		return v, nil
	}
	out, err := s.enc.Encrypt(v)
	if err != nil {
		return "", fmt.Errorf("encrypting webhook secret: %w", err)
	}
	return out, nil
}

func (s *Store) decrypt(v string) (string, error) {
	if s.enc == nil || v == "" {
		return v, nil
	}
	out, err := s.enc.Decrypt(v)
	if err != nil {
		return "", fmt.Errorf("decrypting webhook secret: %w", err)
	}
	return out, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

// scan reads one row and decrypts its secrets.
func (s *Store) scan(sc rowScanner) (Source, error) {
	var (
		src       Source
		auth, cfg []byte
	)
	if err := sc.Scan(&src.Name, &src.Enabled, &auth, &cfg, &src.Connection,
		&src.CreatedBy, &src.CreatedAt, &src.UpdatedAt); err != nil {
		return Source{}, fmt.Errorf("scanning webhook source: %w", err)
	}
	if err := json.Unmarshal(auth, &src.Auth); err != nil {
		return Source{}, fmt.Errorf("decoding webhook auth: %w", err)
	}
	if err := json.Unmarshal(cfg, &src.Config); err != nil {
		return Source{}, fmt.Errorf("decoding webhook config: %w", err)
	}
	var err error
	if src.Auth.Secret, err = s.decrypt(src.Auth.Secret); err != nil {
		return Source{}, err
	}
	if src.Auth.PreviousSecret, err = s.decrypt(src.Auth.PreviousSecret); err != nil {
		return Source{}, err
	}
	return src, nil
}
