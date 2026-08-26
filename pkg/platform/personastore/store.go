// Package personastore persists database-managed persona definitions,
// independent of the platform assembly. It holds the persona_definitions
// storage model (Definition), its Store interface, and the PostgreSQL and no-op
// implementations. Extracted from pkg/platform so the persona storage layer can
// be reasoned about (and size-budgeted) on its own.
package personastore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// ErrNotFound is returned when a persona definition does not exist in the database.
var ErrNotFound = errors.New("persona not found")

// Definition represents a database-managed persona.
type Definition struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Description string   `json:"description,omitempty"`
	Roles       []string `json:"roles"`
	ToolsAllow  []string `json:"tools_allow"`
	ToolsDeny   []string `json:"tools_deny"`
	ConnsAllow  []string `json:"connections_allow,omitempty"`
	ConnsDeny   []string `json:"connections_deny,omitempty"`
	// APIRoutes are the per-(connection, method, path) rules for api-kind
	// connections. Persisted so a persona edited in the portal expresses
	// everything a file persona can; before it was stored, saving a
	// persona through the admin API dropped the rules its file config
	// gave it (issue #1479).
	APIRoutes []persona.APIRouteRule   `json:"api_routes,omitempty"`
	Context   persona.ContextOverrides `json:"context"`
	Priority  int                      `json:"priority"`
	CreatedBy string                   `json:"created_by"`
	UpdatedAt time.Time                `json:"updated_at"`
}

// ToPersona converts a Definition to a persona.Persona.
func (d *Definition) ToPersona() *persona.Persona {
	return &persona.Persona{
		Name:        d.Name,
		DisplayName: d.DisplayName,
		Description: d.Description,
		Roles:       d.Roles,
		Tools: persona.ToolRules{
			Allow: d.ToolsAllow,
			Deny:  d.ToolsDeny,
		},
		Connections: persona.ConnectionRules{
			Allow: d.ConnsAllow,
			Deny:  d.ConnsDeny,
		},
		APIRoutes: d.APIRoutes,
		Context:   d.Context,
		Priority:  d.Priority,
	}
}

// DefinitionFromPersona converts a persona.Persona to a Definition.
func DefinitionFromPersona(p *persona.Persona, author string) Definition {
	return Definition{
		Name:        p.Name,
		DisplayName: p.DisplayName,
		Description: p.Description,
		Roles:       p.Roles,
		ToolsAllow:  p.Tools.Allow,
		ToolsDeny:   p.Tools.Deny,
		ConnsAllow:  p.Connections.Allow,
		ConnsDeny:   p.Connections.Deny,
		APIRoutes:   p.APIRoutes,
		Context:     p.Context,
		Priority:    p.Priority,
		CreatedBy:   author,
	}
}

// Store manages persona definition persistence.
type Store interface {
	List(ctx context.Context) ([]Definition, error)
	Get(ctx context.Context, name string) (*Definition, error)
	Set(ctx context.Context, def Definition) error
	Delete(ctx context.Context, name string) error
}

// PostgresStore implements Store backed by PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed persona store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// List returns all persona definitions.
func (s *PostgresStore) List(ctx context.Context) ([]Definition, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, display_name, description, roles, tools_allow, tools_deny,
		        connections_allow, connections_deny, api_routes, context, priority, created_by, updated_at
		 FROM persona_definitions ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("querying persona definitions: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var defs []Definition
	for rows.Next() {
		d, err := scanDef(rows)
		if err != nil {
			return nil, err
		}
		defs = append(defs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating persona definitions: %w", err)
	}
	return defs, nil
}

// Get returns a single persona definition by name.
func (s *PostgresStore) Get(ctx context.Context, name string) (*Definition, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT name, display_name, description, roles, tools_allow, tools_deny,
		        connections_allow, connections_deny, api_routes, context, priority, created_by, updated_at
		 FROM persona_definitions WHERE name = $1`, name)

	var d Definition
	var roles, toolsAllow, toolsDeny, connsAllow, connsDeny, apiRoutes, contextJSON []byte
	err := row.Scan(&d.Name, &d.DisplayName, &d.Description,
		&roles, &toolsAllow, &toolsDeny, &connsAllow, &connsDeny, &apiRoutes, &contextJSON,
		&d.Priority, &d.CreatedBy, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying persona definition: %w", err)
	}

	if err := unmarshalJSON(&d, jsonFields{
		roles: roles, toolsAllow: toolsAllow, toolsDeny: toolsDeny,
		connsAllow: connsAllow, connsDeny: connsDeny, apiRoutes: apiRoutes,
		contextJSON: contextJSON,
	}); err != nil {
		return nil, err
	}
	return &d, nil
}

// Set creates or updates a persona definition.
func (s *PostgresStore) Set(ctx context.Context, def Definition) error {
	roles, _ := json.Marshal(def.Roles)
	toolsAllow, _ := json.Marshal(def.ToolsAllow)
	toolsDeny, _ := json.Marshal(def.ToolsDeny)
	connsAllow, _ := json.Marshal(def.ConnsAllow)
	connsDeny, _ := json.Marshal(def.ConnsDeny)
	apiRoutes, _ := json.Marshal(def.APIRoutes)
	contextJSON, _ := json.Marshal(def.Context)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO persona_definitions
		 (name, display_name, description, roles, tools_allow, tools_deny,
		  connections_allow, connections_deny, api_routes, context, priority, created_by, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW())
		 ON CONFLICT (name) DO UPDATE SET
		  display_name = $2, description = $3, roles = $4, tools_allow = $5, tools_deny = $6,
		  connections_allow = $7, connections_deny = $8, api_routes = $9, context = $10,
		  priority = $11, created_by = $12, updated_at = NOW()`,
		def.Name, def.DisplayName, def.Description,
		roles, toolsAllow, toolsDeny, connsAllow, connsDeny, apiRoutes, contextJSON,
		def.Priority, def.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("upserting persona definition: %w", err)
	}
	return nil
}

// Delete removes a persona definition by name.
func (s *PostgresStore) Delete(ctx context.Context, name string) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM persona_definitions WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("deleting persona definition: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking delete result: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// scanDef scans a row into a Definition.
func scanDef(rows *sql.Rows) (Definition, error) {
	var d Definition
	var roles, toolsAllow, toolsDeny, connsAllow, connsDeny, apiRoutes, contextJSON []byte
	if err := rows.Scan(&d.Name, &d.DisplayName, &d.Description,
		&roles, &toolsAllow, &toolsDeny, &connsAllow, &connsDeny, &apiRoutes, &contextJSON,
		&d.Priority, &d.CreatedBy, &d.UpdatedAt); err != nil {
		return d, fmt.Errorf("scanning persona definition: %w", err)
	}
	if err := unmarshalJSON(&d, jsonFields{
		roles: roles, toolsAllow: toolsAllow, toolsDeny: toolsDeny,
		connsAllow: connsAllow, connsDeny: connsDeny, apiRoutes: apiRoutes,
		contextJSON: contextJSON,
	}); err != nil {
		return d, err
	}
	return d, nil
}

// jsonFields holds the raw JSONB byte slices scanned from the database.
type jsonFields struct {
	roles       []byte
	toolsAllow  []byte
	toolsDeny   []byte
	connsAllow  []byte
	connsDeny   []byte
	apiRoutes   []byte
	contextJSON []byte
}

// unmarshalJSON deserializes JSONB columns into the Definition.
func unmarshalJSON(d *Definition, f jsonFields) error {
	if err := json.Unmarshal(f.roles, &d.Roles); err != nil {
		return fmt.Errorf("unmarshaling roles: %w", err)
	}
	if err := json.Unmarshal(f.toolsAllow, &d.ToolsAllow); err != nil {
		return fmt.Errorf("unmarshaling tools_allow: %w", err)
	}
	if err := json.Unmarshal(f.toolsDeny, &d.ToolsDeny); err != nil {
		return fmt.Errorf("unmarshaling tools_deny: %w", err)
	}
	if err := json.Unmarshal(f.connsAllow, &d.ConnsAllow); err != nil {
		return fmt.Errorf("unmarshaling connections_allow: %w", err)
	}
	if err := json.Unmarshal(f.connsDeny, &d.ConnsDeny); err != nil {
		return fmt.Errorf("unmarshaling connections_deny: %w", err)
	}
	if len(f.apiRoutes) > 0 {
		if err := json.Unmarshal(f.apiRoutes, &d.APIRoutes); err != nil {
			return fmt.Errorf("unmarshaling api_routes: %w", err)
		}
	}
	if len(f.contextJSON) > 0 {
		_ = json.Unmarshal(f.contextJSON, &d.Context) // best-effort
	}
	return nil
}

// NoopStore is a no-op implementation for when no database is available.
type NoopStore struct{}

// List returns an empty list (no database available).
func (*NoopStore) List(_ context.Context) ([]Definition, error) {
	return nil, nil
}

// Get always returns ErrNotFound (no database available).
func (*NoopStore) Get(_ context.Context, _ string) (*Definition, error) {
	return nil, ErrNotFound
}

// Set is a no-op (no database available).
func (*NoopStore) Set(_ context.Context, _ Definition) error { return nil }

// Delete always returns ErrNotFound (no database available).
func (*NoopStore) Delete(_ context.Context, _ string) error {
	return ErrNotFound
}
