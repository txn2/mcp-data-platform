package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lib/pq"
	"github.com/pgvector/pgvector-go"
)

// PostgresStore implements Store against PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore returns a Store backed by the given *sql.DB. The
// caller owns the connection lifecycle; Close on the DB after the
// store is no longer in use.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Compile-time interface check.
var _ Store = (*PostgresStore)(nil)

const (
	// pgUniqueViolation is the SQLSTATE code Postgres returns when a
	// UNIQUE constraint (or PRIMARY KEY) is violated. Used to map
	// raw database errors back to the package-level ErrConflict.
	pgUniqueViolation = "23505"

	// pgForeignKeyViolation is the SQLSTATE code Postgres returns
	// when an FK constraint fails — used by UpsertSpec / DeleteSpec
	// to translate "no such catalog_id" into ErrNotFound.
	pgForeignKeyViolation = "23503"
)

// CreateCatalog inserts a new catalog header. Returns ErrConflict
// when the ID already exists or (name, version) collides.
func (s *PostgresStore) CreateCatalog(ctx context.Context, c Catalog) error {
	if err := ValidateID(c.ID); err != nil {
		return err
	}
	const q = `
		INSERT INTO api_catalogs
		    (id, name, version, display_name, description, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := s.db.ExecContext(ctx, q,
		c.ID, c.Name, c.Version, c.DisplayName, c.Description, c.CreatedBy)
	if isPGCode(err, pgUniqueViolation) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("catalog: create: %w", err)
	}
	return nil
}

// GetCatalog returns the catalog by ID or ErrNotFound.
func (s *PostgresStore) GetCatalog(ctx context.Context, id string) (*Catalog, error) {
	const q = `
		SELECT id, name, version, display_name, description,
		       created_by, created_at, updated_at
		  FROM api_catalogs
		 WHERE id = $1
	`
	var c Catalog
	err := s.db.QueryRowContext(ctx, q, id).Scan(
		&c.ID, &c.Name, &c.Version, &c.DisplayName, &c.Description,
		&c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get: %w", err)
	}
	return &c, nil
}

// ListCatalogs returns every catalog sorted by (name, version).
// Sort is stable so the portal list view doesn't reshuffle on each
// refresh.
func (s *PostgresStore) ListCatalogs(ctx context.Context) ([]Catalog, error) {
	const q = `
		SELECT id, name, version, display_name, description,
		       created_by, created_at, updated_at
		  FROM api_catalogs
		 ORDER BY name ASC, version ASC, id ASC
	`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("catalog: list: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var out []Catalog
	for rows.Next() {
		var c Catalog
		if err := rows.Scan(&c.ID, &c.Name, &c.Version, &c.DisplayName,
			&c.Description, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalog: list scan: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: list rows: %w", err)
	}
	return out, nil
}

// UpdateCatalog applies a partial edit. Nil fields in u are skipped.
// Returns ErrNotFound when no row matches the id, ErrConflict when
// the edit would violate (name, version) uniqueness.
func (s *PostgresStore) UpdateCatalog(ctx context.Context, id string, u Update) error {
	setClauses, args := buildUpdateSet(u)
	if len(setClauses) == 0 {
		// MemoryStore returns ErrNotFound for an empty update on a
		// missing id; mirror that here so admin handlers see a
		// consistent ErrNotFound regardless of backend.
		var probe string
		err := s.db.QueryRowContext(ctx,
			`SELECT id FROM api_catalogs WHERE id = $1`, id).Scan(&probe)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("catalog: update existence-check: %w", err)
		}
		return nil
	}
	setClauses = append(setClauses, "updated_at = NOW()")
	args = append(args, id)
	// setClauses entries come from a closed set of constants in
	// buildUpdateSet (one of "name = $N", "version = $N", ...
	// , "updated_at = NOW()"). The only dynamic piece is the
	// placeholder number — never operator input — so the
	// concatenation is safe.
	q := "UPDATE api_catalogs SET " + strings.Join(setClauses, ", ") + // #nosec G202 -- closed-set SET clauses, not operator input
		" WHERE id = $" + strconv.Itoa(len(args))
	res, err := s.db.ExecContext(ctx, q, args...)
	if isPGCode(err, pgUniqueViolation) {
		return ErrConflict
	}
	if err != nil {
		return fmt.Errorf("catalog: update: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("catalog: update rows-affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// buildUpdateSet returns the SET clauses and matching args
// for a partial catalog update. Extracted so UpdateCatalog stays
// under the cyclomatic-complexity gate.
func buildUpdateSet(u Update) (clauses []string, args []any) {
	if u.Name != nil {
		args = append(args, *u.Name)
		clauses = append(clauses, "name = $"+strconv.Itoa(len(args)))
	}
	if u.Version != nil {
		args = append(args, *u.Version)
		clauses = append(clauses, "version = $"+strconv.Itoa(len(args)))
	}
	if u.DisplayName != nil {
		args = append(args, *u.DisplayName)
		clauses = append(clauses, "display_name = $"+strconv.Itoa(len(args)))
	}
	if u.Description != nil {
		args = append(args, *u.Description)
		clauses = append(clauses, "description = $"+strconv.Itoa(len(args)))
	}
	return clauses, args
}

// DeleteCatalog removes the catalog and (via ON DELETE CASCADE) all
// of its specs. Returns ErrNotFound when no row matches. Callers
// are expected to consult ReferencingConnections first; this method
// does not check for connection references.
func (s *PostgresStore) DeleteCatalog(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM api_catalogs WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("catalog: delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("catalog: delete rows-affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpsertSpec inserts or replaces a (catalog_id, spec_name) row.
// Returns ErrNotFound when catalog_id has no matching catalog
// (translated from the FK violation).
func (s *PostgresStore) UpsertSpec(ctx context.Context, catalogID string, spec SpecEntry) error {
	if err := ValidateSpecName(spec.SpecName); err != nil {
		return err
	}
	if err := ValidateSourceKind(spec.SourceKind); err != nil {
		return err
	}
	normalizedBasePath, err := NormalizeBasePath(spec.BasePath)
	if err != nil {
		return err
	}
	const q = `
		INSERT INTO api_catalog_specs
		    (catalog_id, spec_name, content, source_kind,
		     source_url, etag, base_path, last_fetched_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (catalog_id, spec_name) DO UPDATE
		SET content         = EXCLUDED.content,
		    source_kind     = EXCLUDED.source_kind,
		    source_url      = EXCLUDED.source_url,
		    etag            = EXCLUDED.etag,
		    base_path       = EXCLUDED.base_path,
		    last_fetched_at = EXCLUDED.last_fetched_at,
		    updated_at      = NOW()
	`
	var lastFetched any
	if !spec.LastFetchedAt.IsZero() {
		lastFetched = spec.LastFetchedAt
	}
	_, err = s.db.ExecContext(ctx, q,
		catalogID, spec.SpecName, spec.Content, spec.SourceKind,
		spec.SourceURL, spec.ETag, normalizedBasePath, lastFetched)
	if isPGCode(err, pgForeignKeyViolation) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("catalog: upsert spec: %w", err)
	}
	return nil
}

// GetSpec returns the named spec from the catalog or ErrNotFound.
func (s *PostgresStore) GetSpec(ctx context.Context, catalogID, specName string) (*SpecEntry, error) {
	const q = `
		SELECT spec_name, content, source_kind, source_url, etag,
		       base_path, last_fetched_at, created_at, updated_at
		  FROM api_catalog_specs
		 WHERE catalog_id = $1 AND spec_name = $2
	`
	var (
		spec      SpecEntry
		fetchedAt sql.NullTime
	)
	err := s.db.QueryRowContext(ctx, q, catalogID, specName).Scan(
		&spec.SpecName, &spec.Content, &spec.SourceKind, &spec.SourceURL,
		&spec.ETag, &spec.BasePath, &fetchedAt, &spec.CreatedAt, &spec.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("catalog: get spec: %w", err)
	}
	if fetchedAt.Valid {
		spec.LastFetchedAt = fetchedAt.Time
	}
	return &spec, nil
}

// ListSpecs returns every component spec in the catalog, sorted by
// spec_name. Empty slice (not error) when the catalog has no specs
// yet.
func (s *PostgresStore) ListSpecs(ctx context.Context, catalogID string) ([]SpecEntry, error) {
	const q = `
		SELECT spec_name, content, source_kind, source_url, etag,
		       base_path, last_fetched_at, created_at, updated_at
		  FROM api_catalog_specs
		 WHERE catalog_id = $1
		 ORDER BY spec_name ASC
	`
	rows, err := s.db.QueryContext(ctx, q, catalogID)
	if err != nil {
		return nil, fmt.Errorf("catalog: list specs: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var out []SpecEntry
	for rows.Next() {
		var (
			spec      SpecEntry
			fetchedAt sql.NullTime
		)
		if err := rows.Scan(&spec.SpecName, &spec.Content, &spec.SourceKind,
			&spec.SourceURL, &spec.ETag, &spec.BasePath, &fetchedAt,
			&spec.CreatedAt, &spec.UpdatedAt); err != nil {
			return nil, fmt.Errorf("catalog: list specs scan: %w", err)
		}
		if fetchedAt.Valid {
			spec.LastFetchedAt = fetchedAt.Time
		}
		out = append(out, spec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: list specs rows: %w", err)
	}
	return out, nil
}

// DeleteSpec removes one component spec from a catalog. Returns
// ErrNotFound when (catalog_id, spec_name) has no row.
func (s *PostgresStore) DeleteSpec(ctx context.Context, catalogID, specName string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM api_catalog_specs WHERE catalog_id = $1 AND spec_name = $2`,
		catalogID, specName)
	if err != nil {
		return fmt.Errorf("catalog: delete spec: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("catalog: delete spec rows-affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReferencingConnections scans connection_instances for any row
// whose config JSONB has `catalog_id` equal to the given id.
// Sorted by (kind, name) for stable output to the admin handler's
// "still referenced by" error.
func (s *PostgresStore) ReferencingConnections(ctx context.Context, catalogID string) ([]ConnectionRef, error) {
	const q = `
		SELECT kind, name
		  FROM connection_instances
		 WHERE config ->> 'catalog_id' = $1
		 ORDER BY kind ASC, name ASC
	`
	rows, err := s.db.QueryContext(ctx, q, catalogID)
	if err != nil {
		return nil, fmt.Errorf("catalog: referencing connections: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var out []ConnectionRef
	for rows.Next() {
		var r ConnectionRef
		if err := rows.Scan(&r.Kind, &r.Name); err != nil {
			return nil, fmt.Errorf("catalog: referencing connections scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: referencing connections rows: %w", err)
	}
	return out, nil
}

// UpsertOperationEmbeddings replaces every embedding row for
// (catalogID, specName) with the supplied rows in a single
// transaction. Atomic: ranking reads either see the prior set or
// the new set, never a partial mix. Returns ErrNotFound when the
// referenced spec does not exist (FK violation).
func (s *PostgresStore) UpsertOperationEmbeddings(ctx context.Context, catalogID, specName string, rows []OperationEmbedding) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("catalog: upsert embeddings begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	const delQ = `
		DELETE FROM api_catalog_operation_embeddings
		 WHERE catalog_id = $1 AND spec_name = $2
	`
	if _, err = tx.ExecContext(ctx, delQ, catalogID, specName); err != nil {
		return fmt.Errorf("catalog: upsert embeddings delete: %w", err)
	}
	const insQ = `
		INSERT INTO api_catalog_operation_embeddings
		    (catalog_id, spec_name, operation_id, text_hash,
		     embedding, model, dim, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
	`
	for _, r := range rows {
		_, err = tx.ExecContext(ctx, insQ,
			catalogID, specName, r.OperationID, r.TextHash,
			pgvector.NewVector(r.Embedding), r.Model, r.Dim)
		if isPGCode(err, pgForeignKeyViolation) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("catalog: upsert embedding %s: %w", r.OperationID, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("catalog: upsert embeddings commit: %w", err)
	}
	return nil
}

// ListOperationEmbeddings returns every embedding row for the
// (catalogID, specName) pair. Empty slice (not error) when the
// spec has not been embedded yet — the caller surfaces this as
// "fall back to lexical, vectors not yet computed".
func (s *PostgresStore) ListOperationEmbeddings(ctx context.Context, catalogID, specName string) ([]OperationEmbedding, error) {
	const q = `
		SELECT operation_id, text_hash, embedding, model, dim
		  FROM api_catalog_operation_embeddings
		 WHERE catalog_id = $1 AND spec_name = $2
	`
	rows, err := s.db.QueryContext(ctx, q, catalogID, specName)
	if err != nil {
		return nil, fmt.Errorf("catalog: list embeddings: %w", err)
	}
	defer rows.Close() //nolint:errcheck // close error on read-only iteration is not actionable
	var out []OperationEmbedding
	for rows.Next() {
		var (
			row OperationEmbedding
			vec pgvector.Vector
		)
		if err := rows.Scan(&row.OperationID, &row.TextHash, &vec, &row.Model, &row.Dim); err != nil {
			return nil, fmt.Errorf("catalog: list embeddings scan: %w", err)
		}
		row.Embedding = vec.Slice()
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("catalog: list embeddings rows: %w", err)
	}
	return out, nil
}

// DeleteOperationEmbeddings removes every embedding row for the
// (catalogID, specName) pair. Used by the reembed admin endpoint
// before recomputing. Spec deletion does not need to call this —
// the FK ON DELETE CASCADE drops the rows automatically.
func (s *PostgresStore) DeleteOperationEmbeddings(ctx context.Context, catalogID, specName string) error {
	const q = `
		DELETE FROM api_catalog_operation_embeddings
		 WHERE catalog_id = $1 AND spec_name = $2
	`
	if _, err := s.db.ExecContext(ctx, q, catalogID, specName); err != nil {
		return fmt.Errorf("catalog: delete embeddings: %w", err)
	}
	return nil
}

// isPGCode reports whether err is a *pq.Error with the given
// SQLSTATE. Centralizes the type-assert dance so callers stay clean.
func isPGCode(err error, code string) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return string(pqErr.Code) == code
	}
	return false
}
