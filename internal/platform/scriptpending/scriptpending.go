// Package scriptpending keeps a managed-script create the save gate refused
// (#2114): its source and the rest of what the create named, one per owner and
// name, so the author fixes a failing test or a lint finding with a small
// patch rather than sending a 45 KB script again. A successful save of the
// name removes it, and the next refused create of the same name replaces it.
package scriptpending

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Create is a refused create as its author sent it.
type Create struct {
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name,omitempty"`
	Description string         `json:"description,omitempty"`
	Category    *string        `json:"category,omitempty"`
	Params      []script.Param `json:"params,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Source      string         `json:"-"`
	UpdatedAt   time.Time      `json:"-"`
}

// Store keeps the refused creates.
type Store interface {
	// Put keeps c as the owner's pending create of its name, replacing one.
	Put(ctx context.Context, owner string, c Create) error
	// Get returns the owner's pending create of name, or nil when none is kept.
	Get(ctx context.Context, owner, name string) (*Create, error)
	// Delete removes the owner's pending create of name, if any.
	Delete(ctx context.Context, owner, name string) error
}

// Postgres keeps them in script_pending_creates.
type Postgres struct{ db *sql.DB }

// NewPostgres returns the store over db, or nil without one.
func NewPostgres(db *sql.DB) *Postgres {
	if db == nil {
		return nil
	}
	return &Postgres{db: db}
}

// Put implements Store.
func (p *Postgres) Put(ctx context.Context, owner string, c Create) error {
	fields, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("scriptpending: encoding the create: %w", err)
	}
	_, err = p.db.ExecContext(ctx, `INSERT INTO script_pending_creates (owner_email, name, source, fields, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (owner_email, name) DO UPDATE SET source = EXCLUDED.source, fields = EXCLUDED.fields, updated_at = NOW()`,
		owner, c.Name, c.Source, fields)
	if err != nil {
		return fmt.Errorf("scriptpending: keeping the create: %w", err)
	}
	return nil
}

// Get implements Store.
func (p *Postgres) Get(ctx context.Context, owner, name string) (*Create, error) {
	var (
		c      Create
		fields []byte
	)
	err := p.db.QueryRowContext(ctx, `SELECT source, fields, updated_at FROM script_pending_creates
		WHERE owner_email = $1 AND name = $2`, owner, name).Scan(&c.Source, &fields, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // none kept is an answer, not a failure
	}
	if err != nil {
		return nil, fmt.Errorf("scriptpending: reading the create: %w", err)
	}
	if err := json.Unmarshal(fields, &c); err != nil {
		return nil, fmt.Errorf("scriptpending: decoding the create: %w", err)
	}
	c.Name = name
	return &c, nil
}

// Delete implements Store.
func (p *Postgres) Delete(ctx context.Context, owner, name string) error {
	if _, err := p.db.ExecContext(ctx, `DELETE FROM script_pending_creates WHERE owner_email = $1 AND name = $2`,
		owner, name); err != nil {
		return fmt.Errorf("scriptpending: removing the create: %w", err)
	}
	return nil
}

// Notice is what a refusal says when its source was kept.
func Notice(name string) string {
	return fmt.Sprintf("Kept as the pending create of %q, in the canonical format the findings' lines refer to. "+
		"Fix it with command=patch name=%s instead of sending the source again; a patch that passes every gate "+
		"saves it. command=get name=%s reads it.", name, name, name)
}

// Fields renders a pending create as manage_script get answers it.
func (c *Create) Fields() map[string]any {
	return map[string]any{
		"name": c.Name, "display_name": c.DisplayName, "source": c.Source, "pending": true, "updated_at": c.UpdatedAt,
		"message": "Not saved: a create of this name was refused and its source kept. Patch it with " +
			"command=patch; a patch that passes every gate saves it.",
	}
}
