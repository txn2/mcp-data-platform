package tableregister

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lib/pq"
)

// postgresStore is the PostgreSQL implementation of Store.
type postgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a registration store backed by PostgreSQL.
func NewPostgresStore(db *sql.DB) Store {
	return &postgresStore{db: db}
}

// selectColumns is the column list every read shares, in the order scanRow
// expects them.
const selectColumns = `id, source_kind, source_id, connection_name, catalog_name,
	schema_name, table_name, location, columns, registered_by, registered_at`

// ErrNameTaken is returned when the unique index on the table name rejects an
// insert. The registrar checks for a holder before it writes; this is the race
// between that check and this write, and it must not surface as a bare
// constraint violation.
var ErrNameTaken = errors.New("that table name was registered by someone else while this registration was being made")

// uniqueViolation is the PostgreSQL SQLSTATE for a unique-constraint breach.
const uniqueViolation = "23505"

// Insert records a registration, reporting a name collision as ErrNameTaken.
func (s *postgresStore) Insert(ctx context.Context, r Registration) error {
	cols, err := json.Marshal(nonNilColumns(r.Columns))
	if err != nil {
		return fmt.Errorf("encoding registration columns: %w", err)
	}
	const q = `INSERT INTO table_registrations
		(id, source_kind, source_id, connection_name, catalog_name, schema_name,
		 table_name, location, columns, registered_by, registered_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW())`
	_, err = s.db.ExecContext(ctx, q,
		r.ID, r.SourceKind, r.SourceID, r.Connection, r.Catalog, r.Schema,
		r.Table, r.Location, cols, r.RegisteredBy)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == uniqueViolation {
			return ErrNameTaken
		}
		return fmt.Errorf("inserting registration: %w", err)
	}
	return nil
}

// nonNilColumns keeps an empty column list encoding as [] rather than null, so
// the JSONB column never holds a value the NOT NULL default was written to
// avoid.
func nonNilColumns(cols []Column) []Column {
	if cols == nil {
		return []Column{}
	}
	return cols
}

// Get reads one registration by id.
func (s *postgresStore) Get(ctx context.Context, id string) (*Registration, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+selectColumns+` FROM table_registrations WHERE id = $1`, id)
	reg, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("reading registration: %w", err)
	}
	return reg, nil
}

// ByName returns the registration holding a name, or nil when it is free. A
// missing row is not an error here: the caller is asking whether the name is
// taken, and "no" is an answer.
func (s *postgresStore) ByName(ctx context.Context, connection, catalog, schema, table string) (*Registration, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+selectColumns+` FROM table_registrations
		 WHERE connection_name = $1 AND catalog_name = $2 AND schema_name = $3 AND table_name = $4`,
		connection, catalog, schema, table)
	reg, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // the caller asked whether the name is taken; "no" is an answer
	}
	if err != nil {
		return nil, fmt.Errorf("reading registration by name: %w", err)
	}
	return reg, nil
}

// BySource returns every registration over one resource or asset.
func (s *postgresStore) BySource(ctx context.Context, kind, sourceID string) ([]Registration, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+selectColumns+` FROM table_registrations
		 WHERE source_kind = $1 AND source_id = $2
		 ORDER BY registered_at DESC, id`, kind, sourceID)
	if err != nil {
		return nil, fmt.Errorf("listing registrations of a source: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	return collectRows(rows)
}

// ForSources returns the registrations of many sources of one kind.
func (s *postgresStore) ForSources(ctx context.Context, kind string, sourceIDs []string) (map[string][]Registration, error) {
	if len(sourceIDs) == 0 {
		return map[string][]Registration{}, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+selectColumns+` FROM table_registrations
		 WHERE source_kind = $1 AND source_id = ANY($2)
		 ORDER BY registered_at DESC, id`, kind, pq.Array(sourceIDs))
	if err != nil {
		return nil, fmt.Errorf("listing registrations of several sources: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	all, err := collectRows(rows)
	if err != nil {
		return nil, err
	}
	bySource := make(map[string][]Registration, len(sourceIDs))
	for _, reg := range all {
		bySource[reg.SourceID] = append(bySource[reg.SourceID], reg)
	}
	return bySource, nil
}

// Delete removes one registration, reporting a miss as ErrNotFound.
func (s *postgresStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM table_registrations WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("deleting registration: %w", err)
	}
	// A delete that matched nothing is reported rather than swallowed: the
	// caller asked to remove a specific registration, and silence would read
	// as success on an id that was never there.
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

// rowScanner is what a *sql.Row and a *sql.Rows have in common, so one scan
// serves the single-row and multi-row reads.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRow(sc rowScanner) (*Registration, error) {
	var (
		reg  Registration
		cols []byte
	)
	if err := sc.Scan(&reg.ID, &reg.SourceKind, &reg.SourceID, &reg.Connection,
		&reg.Catalog, &reg.Schema, &reg.Table, &reg.Location, &cols,
		&reg.RegisteredBy, &reg.RegisteredAt); err != nil {
		return nil, err //nolint:wrapcheck // callers distinguish sql.ErrNoRows
	}
	if len(cols) > 0 {
		if err := json.Unmarshal(cols, &reg.Columns); err != nil {
			return nil, fmt.Errorf("decoding registration columns: %w", err)
		}
	}
	return &reg, nil
}

func collectRows(rows *sql.Rows) ([]Registration, error) {
	var out []Registration
	for rows.Next() {
		reg, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning registration: %w", err)
		}
		out = append(out, *reg)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating registrations: %w", err)
	}
	return out, nil
}
