// Package postgres provides PostgreSQL storage for prompts.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// Compile-time interface verification.
var _ prompt.Store = (*Store)(nil)

// Store implements prompt.Store using PostgreSQL.
type Store struct {
	db *sql.DB
}

// New creates a new PostgreSQL prompt store.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Create persists a new prompt. If p.ID is empty the database generates one.
func (s *Store) Create(ctx context.Context, p *prompt.Prompt) error {
	argsJSON, err := json.Marshal(p.Arguments)
	if err != nil {
		return fmt.Errorf("marshal arguments: %w", err)
	}

	query := `
		INSERT INTO prompts (name, display_name, description, content, arguments,
		                     category, scope, personas, owner_email, source, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at, updated_at`

	return s.db.QueryRowContext(ctx, query,
		p.Name, p.DisplayName, p.Description, p.Content, argsJSON,
		p.Category, p.Scope, pq.Array(p.Personas), p.OwnerEmail,
		p.Source, p.Enabled,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

// Get retrieves a prompt by name. Returns nil, nil if not found.
func (s *Store) Get(ctx context.Context, name string) (*prompt.Prompt, error) {
	return s.getBy(ctx, "name", name)
}

// GetByID retrieves a prompt by ID. Returns nil, nil if not found.
func (s *Store) GetByID(ctx context.Context, id string) (*prompt.Prompt, error) {
	return s.getBy(ctx, "id", id)
}

func (s *Store) getBy(ctx context.Context, column, value string) (*prompt.Prompt, error) {
	query := `SELECT id, name, display_name, description, content, arguments,
	                 category, scope, personas, owner_email, source, enabled,
	                 created_at, updated_at
	          FROM prompts WHERE ` + column + ` = $1`

	p := &prompt.Prompt{}
	var argsJSON []byte
	err := s.db.QueryRowContext(ctx, query, value).Scan(
		&p.ID, &p.Name, &p.DisplayName, &p.Description, &p.Content, &argsJSON,
		&p.Category, &p.Scope, pq.Array(&p.Personas), &p.OwnerEmail,
		&p.Source, &p.Enabled, &p.CreatedAt, &p.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get prompt by %s: %w", column, err)
	}
	if err := json.Unmarshal(argsJSON, &p.Arguments); err != nil {
		return nil, fmt.Errorf("unmarshal arguments: %w", err)
	}
	return p, nil
}

// Update modifies an existing prompt identified by ID.
func (s *Store) Update(ctx context.Context, p *prompt.Prompt) error {
	argsJSON, err := json.Marshal(p.Arguments)
	if err != nil {
		return fmt.Errorf("marshal arguments: %w", err)
	}

	query := `
		UPDATE prompts
		SET name = $2, display_name = $3, description = $4, content = $5,
		    arguments = $6, category = $7, scope = $8, personas = $9,
		    owner_email = $10, source = $11, enabled = $12, updated_at = NOW()
		WHERE id = $1`

	res, err := s.db.ExecContext(ctx, query,
		p.ID, p.Name, p.DisplayName, p.Description, p.Content, argsJSON,
		p.Category, p.Scope, pq.Array(p.Personas), p.OwnerEmail,
		p.Source, p.Enabled,
	)
	if err != nil {
		return fmt.Errorf("update prompt: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("prompt %s not found", p.ID)
	}
	return nil
}

// Delete removes a prompt by name.
func (s *Store) Delete(ctx context.Context, name string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM prompts WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("delete prompt: %w", err)
	}
	return nil
}

// DeleteByID removes a prompt by ID.
func (s *Store) DeleteByID(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM prompts WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete prompt by id: %w", err)
	}
	return nil
}

// List returns prompts matching the filter.
func (s *Store) List(ctx context.Context, filter prompt.ListFilter) ([]prompt.Prompt, error) {
	where, args := buildWhere(filter)
	query := `SELECT id, name, display_name, description, content, arguments,
	                 category, scope, personas, owner_email, source, enabled,
	                 created_at, updated_at
	          FROM prompts` + where + ` ORDER BY scope, name`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list prompts: %w", err)
	}
	defer rows.Close()

	var result []prompt.Prompt
	for rows.Next() {
		var p prompt.Prompt
		var argsJSON []byte
		if err := rows.Scan(
			&p.ID, &p.Name, &p.DisplayName, &p.Description, &p.Content, &argsJSON,
			&p.Category, &p.Scope, pq.Array(&p.Personas), &p.OwnerEmail,
			&p.Source, &p.Enabled, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan prompt: %w", err)
		}
		if err := json.Unmarshal(argsJSON, &p.Arguments); err != nil {
			return nil, fmt.Errorf("unmarshal arguments: %w", err)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// Count returns the number of prompts matching the filter.
func (s *Store) Count(ctx context.Context, filter prompt.ListFilter) (int, error) {
	where, args := buildWhere(filter)
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM prompts`+where, args...,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count prompts: %w", err)
	}
	return count, nil
}

// buildWhere constructs a WHERE clause and parameter list from a ListFilter.
func buildWhere(f prompt.ListFilter) (string, []any) {
	var conds []string
	var args []any
	idx := 1

	if f.Scope != "" {
		conds = append(conds, fmt.Sprintf("scope = $%d", idx))
		args = append(args, f.Scope)
		idx++
	}
	if f.OwnerEmail != "" {
		conds = append(conds, fmt.Sprintf("owner_email = $%d", idx))
		args = append(args, f.OwnerEmail)
		idx++
	}
	if f.Enabled != nil {
		conds = append(conds, fmt.Sprintf("enabled = $%d", idx))
		args = append(args, *f.Enabled)
		idx++
	}
	if len(f.Personas) > 0 {
		conds = append(conds, fmt.Sprintf("personas && $%d", idx))
		args = append(args, pq.Array(f.Personas))
		idx++
	}
	if f.Search != "" {
		conds = append(conds, fmt.Sprintf(
			"(name ILIKE $%d OR display_name ILIKE $%d OR description ILIKE $%d)",
			idx, idx, idx))
		args = append(args, "%"+f.Search+"%")
		idx++
	}

	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}
