package platform

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

const (
	personaFmtUnmetExpect = "unmet expectations: %v"
)

var personaColumns = []string{
	"name", "display_name", "description", "roles", "tools_allow", "tools_deny",
	"connections_allow", "connections_deny", "context", "priority", "created_by", "updated_at",
}

func newTestPersonaStore(t *testing.T) (*PostgresPersonaStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("creating sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresPersonaStore(db), mock
}

func TestToPersona(t *testing.T) {
	def := &PersonaDefinition{
		Name:        "analyst",
		DisplayName: "Data Analyst",
		Description: "Analyzes data",
		Roles:       []string{"analyst", "viewer"},
		ToolsAllow:  []string{"trino_*", "datahub_*"},
		ToolsDeny:   []string{"*_delete_*"},
		ConnsAllow:  []string{"prod_*"},
		ConnsDeny:   []string{"staging_*"},
		Context: persona.ContextOverrides{
			DescriptionPrefix:         "You are a data analyst.",
			DescriptionOverride:       "override-desc",
			AgentInstructionsSuffix:   "Be concise.",
			AgentInstructionsOverride: "override-instr",
		},
		Priority:  10,
		CreatedBy: "admin@example.com",
		UpdatedAt: time.Now(),
	}

	p := def.ToPersona()

	assert.Equal(t, "analyst", p.Name)
	assert.Equal(t, "Data Analyst", p.DisplayName)
	assert.Equal(t, "Analyzes data", p.Description)
	assert.Equal(t, []string{"analyst", "viewer"}, p.Roles)
	assert.Equal(t, []string{"trino_*", "datahub_*"}, p.Tools.Allow)
	assert.Equal(t, []string{"*_delete_*"}, p.Tools.Deny)
	assert.Equal(t, []string{"prod_*"}, p.Connections.Allow)
	assert.Equal(t, []string{"staging_*"}, p.Connections.Deny)
	assert.Equal(t, "You are a data analyst.", p.Context.DescriptionPrefix)
	assert.Equal(t, "override-desc", p.Context.DescriptionOverride)
	assert.Equal(t, "Be concise.", p.Context.AgentInstructionsSuffix)
	assert.Equal(t, "override-instr", p.Context.AgentInstructionsOverride)
	assert.Equal(t, 10, p.Priority)
}

func TestToPersona_EmptyFields(t *testing.T) {
	def := &PersonaDefinition{
		Name: "minimal",
	}

	p := def.ToPersona()

	assert.Equal(t, "minimal", p.Name)
	assert.Empty(t, p.DisplayName)
	assert.Empty(t, p.Roles)
	assert.Nil(t, p.Tools.Allow)
	assert.Nil(t, p.Tools.Deny)
	assert.Nil(t, p.Connections.Allow)
	assert.Nil(t, p.Connections.Deny)
	assert.Equal(t, 0, p.Priority)
}

func TestPersonaDefinitionFromPersona(t *testing.T) {
	p := &persona.Persona{
		Name:        "engineer",
		DisplayName: "Data Engineer",
		Description: "Builds pipelines",
		Roles:       []string{"engineer", "admin"},
		Tools: persona.ToolRules{
			Allow: []string{"*"},
			Deny:  []string{"s3_delete_*"},
		},
		Connections: persona.ConnectionRules{
			Allow: []string{"all_*"},
			Deny:  []string{"legacy_*"},
		},
		Context: persona.ContextOverrides{
			DescriptionPrefix:         "You are a data engineer.",
			DescriptionOverride:       "eng-override",
			AgentInstructionsSuffix:   "Use best practices.",
			AgentInstructionsOverride: "eng-instr-override",
		},
		Priority: 5,
	}

	def := PersonaDefinitionFromPersona(p, "creator@example.com")

	assert.Equal(t, "engineer", def.Name)
	assert.Equal(t, "Data Engineer", def.DisplayName)
	assert.Equal(t, "Builds pipelines", def.Description)
	assert.Equal(t, []string{"engineer", "admin"}, def.Roles)
	assert.Equal(t, []string{"*"}, def.ToolsAllow)
	assert.Equal(t, []string{"s3_delete_*"}, def.ToolsDeny)
	assert.Equal(t, []string{"all_*"}, def.ConnsAllow)
	assert.Equal(t, []string{"legacy_*"}, def.ConnsDeny)
	assert.Equal(t, "You are a data engineer.", def.Context.DescriptionPrefix)
	assert.Equal(t, "eng-override", def.Context.DescriptionOverride)
	assert.Equal(t, "Use best practices.", def.Context.AgentInstructionsSuffix)
	assert.Equal(t, "eng-instr-override", def.Context.AgentInstructionsOverride)
	assert.Equal(t, 5, def.Priority)
	assert.Equal(t, "creator@example.com", def.CreatedBy)
	assert.True(t, def.UpdatedAt.IsZero(), "UpdatedAt should be zero (DB sets via NOW())")
}

func TestUnmarshalPersonaJSON(t *testing.T) {
	tests := []struct {
		name        string
		roles       []byte
		toolsAllow  []byte
		toolsDeny   []byte
		connsAllow  []byte
		connsDeny   []byte
		contextJSON []byte
		wantErr     bool
		errContains string
		check       func(t *testing.T, d *PersonaDefinition)
	}{
		{
			name:        "valid JSON for all fields",
			roles:       []byte(`["analyst","viewer"]`),
			toolsAllow:  []byte(`["trino_*"]`),
			toolsDeny:   []byte(`["*_delete_*"]`),
			connsAllow:  []byte(`["prod_*"]`),
			connsDeny:   []byte(`["staging_*"]`),
			contextJSON: []byte(`{"description_prefix":"Hello"}`),
			check: func(t *testing.T, d *PersonaDefinition) {
				t.Helper()
				assert.Equal(t, []string{"analyst", "viewer"}, d.Roles)
				assert.Equal(t, []string{"trino_*"}, d.ToolsAllow)
				assert.Equal(t, []string{"*_delete_*"}, d.ToolsDeny)
				assert.Equal(t, []string{"prod_*"}, d.ConnsAllow)
				assert.Equal(t, []string{"staging_*"}, d.ConnsDeny)
				assert.Equal(t, "Hello", d.Context.DescriptionPrefix)
			},
		},
		{
			name:        "empty context JSON is tolerated",
			roles:       []byte(`[]`),
			toolsAllow:  []byte(`[]`),
			toolsDeny:   []byte(`[]`),
			connsAllow:  []byte(`[]`),
			connsDeny:   []byte(`[]`),
			contextJSON: []byte{},
			check: func(t *testing.T, d *PersonaDefinition) {
				t.Helper()
				assert.Empty(t, d.Roles)
				assert.Equal(t, persona.ContextOverrides{}, d.Context)
			},
		},
		{
			name:        "invalid roles JSON",
			roles:       []byte(`not-json`),
			toolsAllow:  []byte(`[]`),
			toolsDeny:   []byte(`[]`),
			connsAllow:  []byte(`[]`),
			connsDeny:   []byte(`[]`),
			contextJSON: []byte(`{}`),
			wantErr:     true,
			errContains: "unmarshaling roles",
		},
		{
			name:        "invalid tools_allow JSON",
			roles:       []byte(`[]`),
			toolsAllow:  []byte(`{bad`),
			toolsDeny:   []byte(`[]`),
			connsAllow:  []byte(`[]`),
			connsDeny:   []byte(`[]`),
			contextJSON: []byte(`{}`),
			wantErr:     true,
			errContains: "unmarshaling tools_allow",
		},
		{
			name:        "invalid tools_deny JSON",
			roles:       []byte(`[]`),
			toolsAllow:  []byte(`[]`),
			toolsDeny:   []byte(`{bad`),
			connsAllow:  []byte(`[]`),
			connsDeny:   []byte(`[]`),
			contextJSON: []byte(`{}`),
			wantErr:     true,
			errContains: "unmarshaling tools_deny",
		},
		{
			name:        "invalid connections_allow JSON",
			roles:       []byte(`[]`),
			toolsAllow:  []byte(`[]`),
			toolsDeny:   []byte(`[]`),
			connsAllow:  []byte(`{bad`),
			connsDeny:   []byte(`[]`),
			contextJSON: []byte(`{}`),
			wantErr:     true,
			errContains: "unmarshaling connections_allow",
		},
		{
			name:        "invalid connections_deny JSON",
			roles:       []byte(`[]`),
			toolsAllow:  []byte(`[]`),
			toolsDeny:   []byte(`[]`),
			connsAllow:  []byte(`[]`),
			connsDeny:   []byte(`{bad`),
			contextJSON: []byte(`{}`),
			wantErr:     true,
			errContains: "unmarshaling connections_deny",
		},
		{
			name:        "invalid context JSON is best-effort (no error)",
			roles:       []byte(`[]`),
			toolsAllow:  []byte(`[]`),
			toolsDeny:   []byte(`[]`),
			connsAllow:  []byte(`[]`),
			connsDeny:   []byte(`[]`),
			contextJSON: []byte(`{bad`),
			wantErr:     false,
			check: func(t *testing.T, d *PersonaDefinition) {
				t.Helper()
				// Context should remain zero-value since unmarshal failed silently.
				assert.Equal(t, persona.ContextOverrides{}, d.Context)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d PersonaDefinition
			err := unmarshalPersonaJSON(&d, personaJSONFields{
				roles: tt.roles, toolsAllow: tt.toolsAllow, toolsDeny: tt.toolsDeny,
				connsAllow: tt.connsAllow, connsDeny: tt.connsDeny, contextJSON: tt.contextJSON,
			})
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, &d)
			}
		})
	}
}

func TestNoopPersonaStore(t *testing.T) {
	store := &NoopPersonaStore{}
	ctx := context.Background()

	t.Run("List returns nil nil", func(t *testing.T) {
		defs, err := store.List(ctx)
		assert.NoError(t, err)
		assert.Nil(t, defs)
	})

	t.Run("Get returns ErrPersonaNotFound", func(t *testing.T) {
		def, err := store.Get(ctx, "anything")
		assert.Nil(t, def)
		assert.ErrorIs(t, err, ErrPersonaNotFound)
	})

	t.Run("Set returns nil", func(t *testing.T) {
		err := store.Set(ctx, PersonaDefinition{Name: "test"})
		assert.NoError(t, err)
	})

	t.Run("Delete returns ErrPersonaNotFound", func(t *testing.T) {
		err := store.Delete(ctx, "anything")
		assert.ErrorIs(t, err, ErrPersonaNotFound)
	})
}

func TestNewPostgresPersonaStore(t *testing.T) {
	store := NewPostgresPersonaStore(nil)
	require.NotNil(t, store)
	assert.Nil(t, store.db)
}

func TestPersonaDefinitionRoundTrip(t *testing.T) {
	original := &persona.Persona{
		Name:        "roundtrip",
		DisplayName: "Round Trip Test",
		Description: "Tests conversion symmetry",
		Roles:       []string{"role_a", "role_b"},
		Tools: persona.ToolRules{
			Allow: []string{"tool_a", "tool_b"},
			Deny:  []string{"tool_c"},
		},
		Connections: persona.ConnectionRules{
			Allow: []string{"conn_a"},
			Deny:  []string{"conn_b"},
		},
		Context: persona.ContextOverrides{
			DescriptionPrefix:       "prefix",
			AgentInstructionsSuffix: "suffix",
		},
		Priority: 42,
	}

	def := PersonaDefinitionFromPersona(original, "tester@example.com")
	converted := def.ToPersona()

	assert.Equal(t, original.Name, converted.Name)
	assert.Equal(t, original.DisplayName, converted.DisplayName)
	assert.Equal(t, original.Description, converted.Description)
	assert.Equal(t, original.Roles, converted.Roles)
	assert.Equal(t, original.Tools, converted.Tools)
	assert.Equal(t, original.Connections, converted.Connections)
	assert.Equal(t, original.Context, converted.Context)
	assert.Equal(t, original.Priority, converted.Priority)
}

// --- PostgresPersonaStore sqlmock tests ---

func TestPostgresPersonaStoreList(t *testing.T) {
	store, mock := newTestPersonaStore(t)
	now := time.Now()

	rows := sqlmock.NewRows(personaColumns).
		AddRow("analyst", "Data Analyst", "Analyzes data",
			[]byte(`["analyst","viewer"]`), []byte(`["trino_*"]`), []byte(`["*_delete_*"]`),
			[]byte(`["prod_*"]`), []byte(`["staging_*"]`), []byte(`{"description_prefix":"Hello"}`),
			10, "admin@example.com", now).
		AddRow("engineer", "Data Engineer", "Builds pipelines",
			[]byte(`["engineer"]`), []byte(`["*"]`), []byte(`[]`),
			[]byte(`[]`), []byte(`[]`), []byte(`{}`),
			5, "creator@example.com", now)

	mock.ExpectQuery("SELECT name, display_name, description, roles, tools_allow, tools_deny").
		WillReturnRows(rows)

	defs, err := store.List(context.Background())
	require.NoError(t, err)
	require.Len(t, defs, 2)

	assert.Equal(t, "analyst", defs[0].Name)
	assert.Equal(t, "Data Analyst", defs[0].DisplayName)
	assert.Equal(t, "Analyzes data", defs[0].Description)
	assert.Equal(t, []string{"analyst", "viewer"}, defs[0].Roles)
	assert.Equal(t, []string{"trino_*"}, defs[0].ToolsAllow)
	assert.Equal(t, []string{"*_delete_*"}, defs[0].ToolsDeny)
	assert.Equal(t, []string{"prod_*"}, defs[0].ConnsAllow)
	assert.Equal(t, []string{"staging_*"}, defs[0].ConnsDeny)
	assert.Equal(t, "Hello", defs[0].Context.DescriptionPrefix)
	assert.Equal(t, 10, defs[0].Priority)
	assert.Equal(t, "admin@example.com", defs[0].CreatedBy)
	assert.Equal(t, now, defs[0].UpdatedAt)

	assert.Equal(t, "engineer", defs[1].Name)
	assert.Equal(t, "Data Engineer", defs[1].DisplayName)
	assert.Equal(t, []string{"engineer"}, defs[1].Roles)
	assert.Equal(t, []string{"*"}, defs[1].ToolsAllow)
	assert.Equal(t, 5, defs[1].Priority)
	assert.Equal(t, "creator@example.com", defs[1].CreatedBy)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(personaFmtUnmetExpect, err)
	}
}

func TestPostgresPersonaStoreList_QueryError(t *testing.T) {
	store, mock := newTestPersonaStore(t)

	mock.ExpectQuery("SELECT name, display_name, description, roles, tools_allow, tools_deny").
		WillReturnError(errors.New("db error"))

	defs, err := store.List(context.Background())
	require.Error(t, err)
	assert.Nil(t, defs)
	assert.Contains(t, err.Error(), "querying persona definitions")
	assert.Contains(t, err.Error(), "db error")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(personaFmtUnmetExpect, err)
	}
}

func TestPostgresPersonaStoreGet(t *testing.T) {
	store, mock := newTestPersonaStore(t)
	now := time.Now()

	rows := sqlmock.NewRows(personaColumns).
		AddRow("analyst", "Data Analyst", "Analyzes data",
			[]byte(`["analyst"]`), []byte(`["trino_*"]`), []byte(`["*_delete_*"]`),
			[]byte(`["prod_*"]`), []byte(`["staging_*"]`), []byte(`{"description_prefix":"ctx"}`),
			10, "admin@example.com", now)

	mock.ExpectQuery("SELECT name, display_name, description, roles, tools_allow, tools_deny").
		WithArgs("analyst").
		WillReturnRows(rows)

	def, err := store.Get(context.Background(), "analyst")
	require.NoError(t, err)
	require.NotNil(t, def)

	assert.Equal(t, "analyst", def.Name)
	assert.Equal(t, "Data Analyst", def.DisplayName)
	assert.Equal(t, "Analyzes data", def.Description)
	assert.Equal(t, []string{"analyst"}, def.Roles)
	assert.Equal(t, []string{"trino_*"}, def.ToolsAllow)
	assert.Equal(t, []string{"*_delete_*"}, def.ToolsDeny)
	assert.Equal(t, []string{"prod_*"}, def.ConnsAllow)
	assert.Equal(t, []string{"staging_*"}, def.ConnsDeny)
	assert.Equal(t, "ctx", def.Context.DescriptionPrefix)
	assert.Equal(t, 10, def.Priority)
	assert.Equal(t, "admin@example.com", def.CreatedBy)
	assert.Equal(t, now, def.UpdatedAt)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(personaFmtUnmetExpect, err)
	}
}

func TestPostgresPersonaStoreGet_NotFound(t *testing.T) {
	store, mock := newTestPersonaStore(t)

	rows := sqlmock.NewRows(personaColumns) // empty result set
	mock.ExpectQuery("SELECT name, display_name, description, roles, tools_allow, tools_deny").
		WithArgs("nonexistent").
		WillReturnRows(rows)

	def, err := store.Get(context.Background(), "nonexistent")
	assert.Nil(t, def)
	assert.ErrorIs(t, err, ErrPersonaNotFound)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(personaFmtUnmetExpect, err)
	}
}

func TestPostgresPersonaStoreSet(t *testing.T) {
	store, mock := newTestPersonaStore(t)
	now := time.Now()

	def := PersonaDefinition{
		Name:        "analyst",
		DisplayName: "Data Analyst",
		Description: "Analyzes data",
		Roles:       []string{"analyst"},
		ToolsAllow:  []string{"trino_*"},
		ToolsDeny:   []string{"*_delete_*"},
		ConnsAllow:  []string{"prod_*"},
		ConnsDeny:   []string{"staging_*"},
		Context:     persona.ContextOverrides{DescriptionPrefix: "Hello"},
		Priority:    10,
		CreatedBy:   "admin@example.com",
		UpdatedAt:   now,
	}

	mock.ExpectExec("INSERT INTO persona_definitions").
		WithArgs(
			def.Name, def.DisplayName, def.Description,
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), // roles, tools_allow, tools_deny
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), // conns_allow, conns_deny, context
			def.Priority, def.CreatedBy,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := store.Set(context.Background(), def)
	require.NoError(t, err)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(personaFmtUnmetExpect, err)
	}
}

func TestPostgresPersonaStoreDelete(t *testing.T) {
	store, mock := newTestPersonaStore(t)

	mock.ExpectExec("DELETE FROM persona_definitions WHERE name").
		WithArgs("analyst").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := store.Delete(context.Background(), "analyst")
	require.NoError(t, err)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(personaFmtUnmetExpect, err)
	}
}

func TestPostgresPersonaStoreDelete_NotFound(t *testing.T) {
	store, mock := newTestPersonaStore(t)

	mock.ExpectExec("DELETE FROM persona_definitions WHERE name").
		WithArgs("nonexistent").
		WillReturnResult(driver.RowsAffected(0))

	err := store.Delete(context.Background(), "nonexistent")
	assert.ErrorIs(t, err, ErrPersonaNotFound)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(personaFmtUnmetExpect, err)
	}
}

func TestPostgresPersonaStoreDelete_ExecError(t *testing.T) {
	store, mock := newTestPersonaStore(t)

	mock.ExpectExec("DELETE FROM persona_definitions WHERE name").
		WithArgs("analyst").
		WillReturnError(errors.New("exec error"))

	err := store.Delete(context.Background(), "analyst")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "deleting persona definition")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(personaFmtUnmetExpect, err)
	}
}

func TestPostgresPersonaStoreList_ScanError(t *testing.T) {
	store, mock := newTestPersonaStore(t)

	// Return a row with invalid data types to trigger scan error
	rows := sqlmock.NewRows(personaColumns).
		AddRow("bad", "Bad", "desc", "not-json", "[]", "[]", "[]", "[]", "{}", 0, "admin", time.Now())

	mock.ExpectQuery("SELECT .+ FROM persona_definitions").WillReturnRows(rows)

	_, err := store.List(context.Background())
	assert.Error(t, err)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(personaFmtUnmetExpect, err)
	}
}

func TestPostgresPersonaStoreGet_QueryError(t *testing.T) {
	store, mock := newTestPersonaStore(t)

	mock.ExpectQuery("SELECT .+ FROM persona_definitions WHERE name").
		WithArgs("test").
		WillReturnError(errors.New("query error"))

	_, err := store.Get(context.Background(), "test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "querying persona definition")

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf(personaFmtUnmetExpect, err)
	}
}
