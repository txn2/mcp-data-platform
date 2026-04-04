package platform

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// ErrPersonaNotFound is returned when a persona definition does not exist in the database.
var ErrPersonaNotFound = errors.New("persona not found")

// PersonaDefinition represents a database-managed persona.
type PersonaDefinition struct {
	Name        string                   `json:"name"`
	DisplayName string                   `json:"display_name"`
	Description string                   `json:"description,omitempty"`
	Roles       []string                 `json:"roles"`
	ToolsAllow  []string                 `json:"tools_allow"`
	ToolsDeny   []string                 `json:"tools_deny"`
	ConnsAllow  []string                 `json:"connections_allow,omitempty"`
	ConnsDeny   []string                 `json:"connections_deny,omitempty"`
	Context     persona.ContextOverrides `json:"context"`
	Priority    int                      `json:"priority"`
	CreatedBy   string                   `json:"created_by"`
	UpdatedAt   time.Time                `json:"updated_at"`
}

// ToPersona converts a PersonaDefinition to a persona.Persona.
func (d *PersonaDefinition) ToPersona() *persona.Persona {
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
		Context:  d.Context,
		Priority: d.Priority,
	}
}

// PersonaDefinitionFromPersona converts a persona.Persona to a PersonaDefinition.
func PersonaDefinitionFromPersona(p *persona.Persona, author string) PersonaDefinition {
	return PersonaDefinition{
		Name:        p.Name,
		DisplayName: p.DisplayName,
		Description: p.Description,
		Roles:       p.Roles,
		ToolsAllow:  p.Tools.Allow,
		ToolsDeny:   p.Tools.Deny,
		ConnsAllow:  p.Connections.Allow,
		ConnsDeny:   p.Connections.Deny,
		Context:     p.Context,
		Priority:    p.Priority,
		CreatedBy:   author,
		UpdatedAt:   time.Now(),
	}
}

// PersonaStore manages persona definition persistence.
type PersonaStore interface {
	List(ctx context.Context) ([]PersonaDefinition, error)
	Get(ctx context.Context, name string) (*PersonaDefinition, error)
	Set(ctx context.Context, def PersonaDefinition) error
	Delete(ctx context.Context, name string) error
}

// PostgresPersonaStore implements PersonaStore backed by PostgreSQL.
type PostgresPersonaStore struct {
	db *sql.DB
}

// NewPostgresPersonaStore creates a new PostgreSQL-backed persona store.
func NewPostgresPersonaStore(db *sql.DB) *PostgresPersonaStore {
	return &PostgresPersonaStore{db: db}
}

// List returns all persona definitions.
func (s *PostgresPersonaStore) List(ctx context.Context) ([]PersonaDefinition, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, display_name, description, roles, tools_allow, tools_deny,
		        connections_allow, connections_deny, context, priority, created_by, updated_at
		 FROM persona_definitions ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("querying persona definitions: %w", err)
	}
	defer rows.Close() //nolint:errcheck // best-effort cleanup

	var defs []PersonaDefinition
	for rows.Next() {
		d, err := scanPersonaDef(rows)
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
func (s *PostgresPersonaStore) Get(ctx context.Context, name string) (*PersonaDefinition, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT name, display_name, description, roles, tools_allow, tools_deny,
		        connections_allow, connections_deny, context, priority, created_by, updated_at
		 FROM persona_definitions WHERE name = $1`, name)

	var d PersonaDefinition
	var roles, toolsAllow, toolsDeny, connsAllow, connsDeny, contextJSON []byte
	err := row.Scan(&d.Name, &d.DisplayName, &d.Description,
		&roles, &toolsAllow, &toolsDeny, &connsAllow, &connsDeny, &contextJSON,
		&d.Priority, &d.CreatedBy, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrPersonaNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("querying persona definition: %w", err)
	}

	if err := unmarshalPersonaJSON(&d, personaJSONFields{
		roles: roles, toolsAllow: toolsAllow, toolsDeny: toolsDeny,
		connsAllow: connsAllow, connsDeny: connsDeny, contextJSON: contextJSON,
	}); err != nil {
		return nil, err
	}
	return &d, nil
}

// Set creates or updates a persona definition.
func (s *PostgresPersonaStore) Set(ctx context.Context, def PersonaDefinition) error {
	roles, _ := json.Marshal(def.Roles)
	toolsAllow, _ := json.Marshal(def.ToolsAllow)
	toolsDeny, _ := json.Marshal(def.ToolsDeny)
	connsAllow, _ := json.Marshal(def.ConnsAllow)
	connsDeny, _ := json.Marshal(def.ConnsDeny)
	contextJSON, _ := json.Marshal(def.Context)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO persona_definitions
		 (name, display_name, description, roles, tools_allow, tools_deny,
		  connections_allow, connections_deny, context, priority, created_by, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 ON CONFLICT (name) DO UPDATE SET
		  display_name = $2, description = $3, roles = $4, tools_allow = $5, tools_deny = $6,
		  connections_allow = $7, connections_deny = $8, context = $9, priority = $10,
		  created_by = $11, updated_at = $12`,
		def.Name, def.DisplayName, def.Description,
		roles, toolsAllow, toolsDeny, connsAllow, connsDeny, contextJSON,
		def.Priority, def.CreatedBy, def.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upserting persona definition: %w", err)
	}
	return nil
}

// Delete removes a persona definition by name.
func (s *PostgresPersonaStore) Delete(ctx context.Context, name string) error {
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
		return ErrPersonaNotFound
	}
	return nil
}

// scanPersonaDef scans a row into a PersonaDefinition.
func scanPersonaDef(rows *sql.Rows) (PersonaDefinition, error) {
	var d PersonaDefinition
	var roles, toolsAllow, toolsDeny, connsAllow, connsDeny, contextJSON []byte
	if err := rows.Scan(&d.Name, &d.DisplayName, &d.Description,
		&roles, &toolsAllow, &toolsDeny, &connsAllow, &connsDeny, &contextJSON,
		&d.Priority, &d.CreatedBy, &d.UpdatedAt); err != nil {
		return d, fmt.Errorf("scanning persona definition: %w", err)
	}
	if err := unmarshalPersonaJSON(&d, personaJSONFields{
		roles: roles, toolsAllow: toolsAllow, toolsDeny: toolsDeny,
		connsAllow: connsAllow, connsDeny: connsDeny, contextJSON: contextJSON,
	}); err != nil {
		return d, err
	}
	return d, nil
}

// personaJSONFields holds the raw JSONB byte slices scanned from the database.
type personaJSONFields struct {
	roles       []byte
	toolsAllow  []byte
	toolsDeny   []byte
	connsAllow  []byte
	connsDeny   []byte
	contextJSON []byte
}

// unmarshalPersonaJSON deserializes JSONB columns into the PersonaDefinition.
func unmarshalPersonaJSON(d *PersonaDefinition, f personaJSONFields) error {
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
	if len(f.contextJSON) > 0 {
		_ = json.Unmarshal(f.contextJSON, &d.Context) // best-effort
	}
	return nil
}

// NoopPersonaStore is a no-op implementation for when no database is available.
type NoopPersonaStore struct{}

// List returns an empty list (no database available).
func (*NoopPersonaStore) List(_ context.Context) ([]PersonaDefinition, error) {
	return nil, nil
}

// Get always returns ErrPersonaNotFound (no database available).
func (*NoopPersonaStore) Get(_ context.Context, _ string) (*PersonaDefinition, error) {
	return nil, ErrPersonaNotFound
}

// Set is a no-op (no database available).
func (*NoopPersonaStore) Set(_ context.Context, _ PersonaDefinition) error { return nil }

// Delete always returns ErrPersonaNotFound (no database available).
func (*NoopPersonaStore) Delete(_ context.Context, _ string) error {
	return ErrPersonaNotFound
}
