package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// Compile-time interface verification.
var _ prompt.CollectionStore = (*Store)(nil)

// PostgreSQL error codes for constraint and input violations.
const (
	pqUniqueViolation     = "23505"
	pqForeignKeyViolation = "23503"
	// pqInvalidTextRepresentation fires when a caller-supplied id fails to
	// parse as a UUID; such an id cannot name any row, so lookups map it to
	// not-found rather than surfacing a 500.
	pqInvalidTextRepresentation = "22P02"
)

// pqCode reports whether err is a pq error with the given SQLSTATE code.
func pqCode(err error, code string) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && string(pqErr.Code) == code
}

// isUniqueViolation reports whether err is a unique-constraint violation.
func isUniqueViolation(err error) bool {
	return pqCode(err, pqUniqueViolation)
}

// nullableID binds an optional UUID column: empty string becomes SQL NULL.
func nullableID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

// CreateCollection persists a new collection, generating its ID.
func (s *Store) CreateCollection(ctx context.Context, c *prompt.Collection) error {
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO prompt_collections (name, description, created_by)
		VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at`,
		c.Name, c.Description, c.CreatedBy,
	).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
	if isUniqueViolation(err) {
		return prompt.ErrCollectionExists
	}
	if err != nil {
		return fmt.Errorf("create prompt collection: %w", err)
	}
	return nil
}

// GetCollection retrieves a collection by ID with its prompt count. Returns
// nil, nil if not found.
func (s *Store) GetCollection(ctx context.Context, id string) (*prompt.Collection, error) {
	c := &prompt.Collection{}
	err := s.db.QueryRowContext(ctx, collectionSelect+`
		WHERE c.id = $1
		GROUP BY c.id`, id,
	).Scan(collectionScanDest(c)...)
	if errors.Is(err, sql.ErrNoRows) || pqCode(err, pqInvalidTextRepresentation) {
		return nil, nil //nolint:nilnil // CollectionStore contract: nil, nil means not found
	}
	if err != nil {
		return nil, fmt.Errorf("get prompt collection: %w", err)
	}
	return c, nil
}

// collectionSelect reads the collection columns plus the member prompt count.
const collectionSelect = `
	SELECT c.id, c.name, c.description, c.created_by,
	       COUNT(p.id), c.created_at, c.updated_at
	  FROM prompt_collections c
	  LEFT JOIN prompts p ON p.collection_id = c.id`

// collectionScanDest returns the scan destinations in collectionSelect order.
func collectionScanDest(c *prompt.Collection) []any {
	return []any{
		&c.ID, &c.Name, &c.Description, &c.CreatedBy,
		&c.PromptCount, &c.CreatedAt, &c.UpdatedAt,
	}
}

// ListCollections returns every collection with its prompt count, ordered by
// name (case-insensitive, matching the uniqueness rule).
func (s *Store) ListCollections(ctx context.Context) ([]prompt.Collection, error) {
	rows, err := s.db.QueryContext(ctx, collectionSelect+`
		GROUP BY c.id
		ORDER BY LOWER(c.name)`)
	if err != nil {
		return nil, fmt.Errorf("list prompt collections: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := []prompt.Collection{}
	for rows.Next() {
		var c prompt.Collection
		if err := rows.Scan(collectionScanDest(&c)...); err != nil {
			return nil, fmt.Errorf("scan prompt collection: %w", err)
		}
		result = append(result, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate prompt collections: %w", err)
	}
	return result, nil
}

// UpdateCollection renames or re-describes a collection.
func (s *Store) UpdateCollection(ctx context.Context, id, name, description string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE prompt_collections
		SET name = $2, description = $3, updated_at = NOW()
		WHERE id = $1`,
		id, name, description,
	)
	if isUniqueViolation(err) {
		return prompt.ErrCollectionExists
	}
	if err != nil {
		return fmt.Errorf("update prompt collection: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("prompt collection %s not found", id)
	}
	return nil
}

// DeleteCollection removes a collection. The collection_id FK is ON DELETE
// SET NULL, so member prompts are released to the uncollected group.
func (s *Store) DeleteCollection(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM prompt_collections WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete prompt collection: %w", err)
	}
	return nil
}

// SetPromptCollection assigns a prompt to a collection (empty collectionID
// clears the assignment). Placement is not reviewable substance: no version
// snapshot, no review gate, and the search embedding is untouched.
func (s *Store) SetPromptCollection(ctx context.Context, promptID, collectionID string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE prompts SET collection_id = $2, updated_at = NOW()
		WHERE id = $1`,
		promptID, nullableID(collectionID),
	)
	// A dangling FK and a malformed collection id both mean "no such
	// collection". (A malformed prompt id cannot reach here: the handlers
	// resolve the prompt row first, so the only unparsed UUID left is the
	// collection id from the request body.)
	if pqCode(err, pqForeignKeyViolation) || pqCode(err, pqInvalidTextRepresentation) {
		return prompt.ErrCollectionNotFound
	}
	if err != nil {
		return fmt.Errorf("set prompt collection: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("prompt %s not found", promptID)
	}
	return nil
}
