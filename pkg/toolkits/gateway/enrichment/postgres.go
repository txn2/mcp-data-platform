package enrichment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// postgresStore is a PostgreSQL-backed implementation of Store.
type postgresStore struct {
	db *sql.DB
}

// NewPostgresStore wires a Store to the given database handle.
func NewPostgresStore(db *sql.DB) Store {
	return &postgresStore{db: db}
}

const selectColumns = `id, connection_name, tool_name, when_predicate,
                        enrich_action, merge_strategy, description, enabled,
                        created_by, created_at, updated_at`

// buildListQuery assembles the filtered SELECT without interpolating any
// caller-provided values. Only positional placeholder indices ("$1", "$2")
// are inserted into the query string; actual values travel through
// QueryContext args.
func buildListQuery(connection, tool string, enabledOnly bool) (query string, args []any) {
	query = `SELECT ` + selectColumns + `
              FROM gateway_enrichment_rules WHERE 1=1`
	args = []any{}
	if connection != "" {
		args = append(args, connection)
		query += fmt.Sprintf(" AND connection_name = $%d", len(args))
	}
	if tool != "" {
		args = append(args, tool)
		query += fmt.Sprintf(" AND tool_name = $%d", len(args))
	}
	if enabledOnly {
		query += " AND enabled = TRUE"
	}
	query += " ORDER BY updated_at DESC"
	return query, args
}

// List returns every rule matching the filter, newest-first by updated_at.
func (s *postgresStore) List(ctx context.Context, connection, tool string, enabledOnly bool) ([]Rule, error) {
	query, args := buildListQuery(connection, tool, enabledOnly)
	// #nosec G202 -- query built from fixed fragments with positional args;
	// variable values are bound through QueryContext, not interpolated.
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query rules: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var out []Rule
	for rows.Next() {
		r, scanErr := scanRule(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rules: %w", err)
	}
	return out, nil
}

// Get returns a single rule by id.
func (s *postgresStore) Get(ctx context.Context, id string) (*Rule, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+selectColumns+` FROM gateway_enrichment_rules WHERE id = $1`, id)
	r, err := scanRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRuleNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Create inserts a new rule, assigning an id if the input lacks one.
func (s *postgresStore) Create(ctx context.Context, r Rule) (Rule, error) {
	if r.ID == "" {
		id, err := GenerateID()
		if err != nil {
			return Rule{}, err
		}
		r.ID = id
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	r.UpdatedAt = now

	fields, err := marshalJSONFields(r)
	if err != nil {
		return Rule{}, err
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO gateway_enrichment_rules
         (id, connection_name, tool_name, when_predicate, enrich_action,
          merge_strategy, description, enabled, created_by, created_at, updated_at)
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		r.ID, r.ConnectionName, r.ToolName, fields.when, fields.action, fields.merge,
		r.Description, r.Enabled, r.CreatedBy, r.CreatedAt, r.UpdatedAt)
	if err != nil {
		return Rule{}, fmt.Errorf("insert rule: %w", err)
	}
	return r, nil
}

// Update modifies an existing rule by id.
func (s *postgresStore) Update(ctx context.Context, r Rule) (Rule, error) {
	if r.ID == "" {
		return Rule{}, errors.New("enrichment: update requires rule id")
	}
	r.UpdatedAt = time.Now().UTC()

	fields, err := marshalJSONFields(r)
	if err != nil {
		return Rule{}, err
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE gateway_enrichment_rules
         SET connection_name = $2, tool_name = $3, when_predicate = $4,
             enrich_action = $5, merge_strategy = $6, description = $7,
             enabled = $8, updated_at = $9
         WHERE id = $1`,
		r.ID, r.ConnectionName, r.ToolName, fields.when, fields.action, fields.merge,
		r.Description, r.Enabled, r.UpdatedAt)
	if err != nil {
		return Rule{}, fmt.Errorf("update rule: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Rule{}, fmt.Errorf("update rule result: %w", err)
	}
	if affected == 0 {
		return Rule{}, ErrRuleNotFound
	}
	return r, nil
}

// Delete removes a rule by id.
func (s *postgresStore) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM gateway_enrichment_rules WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete rule result: %w", err)
	}
	if affected == 0 {
		return ErrRuleNotFound
	}
	return nil
}

// scanner abstracts *sql.Row and *sql.Rows for the single scanRule helper.
type scanner interface {
	Scan(dest ...any) error
}

// ruleJSONFields holds the three marshaled JSONB columns of a rule row.
type ruleJSONFields struct {
	when, action, merge []byte
}

func scanRule(sc scanner) (Rule, error) {
	var (
		r                               Rule
		whenJSON, actionJSON, mergeJSON []byte
	)
	if err := sc.Scan(
		&r.ID, &r.ConnectionName, &r.ToolName,
		&whenJSON, &actionJSON, &mergeJSON,
		&r.Description, &r.Enabled, &r.CreatedBy,
		&r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return Rule{}, fmt.Errorf("scan rule: %w", err)
	}
	if err := json.Unmarshal(whenJSON, &r.WhenPredicate); err != nil {
		return Rule{}, fmt.Errorf("unmarshal when_predicate: %w", err)
	}
	if err := json.Unmarshal(actionJSON, &r.EnrichAction); err != nil {
		return Rule{}, fmt.Errorf("unmarshal enrich_action: %w", err)
	}
	if err := json.Unmarshal(mergeJSON, &r.MergeStrategy); err != nil {
		return Rule{}, fmt.Errorf("unmarshal merge_strategy: %w", err)
	}
	return r, nil
}

func marshalJSONFields(r Rule) (ruleJSONFields, error) {
	whenJSON, err := json.Marshal(r.WhenPredicate)
	if err != nil {
		return ruleJSONFields{}, fmt.Errorf("marshal when_predicate: %w", err)
	}
	actionJSON, err := json.Marshal(r.EnrichAction)
	if err != nil {
		return ruleJSONFields{}, fmt.Errorf("marshal enrich_action: %w", err)
	}
	mergeJSON, err := json.Marshal(r.MergeStrategy)
	if err != nil {
		return ruleJSONFields{}, fmt.Errorf("marshal merge_strategy: %w", err)
	}
	return ruleJSONFields{when: whenJSON, action: actionJSON, merge: mergeJSON}, nil
}
