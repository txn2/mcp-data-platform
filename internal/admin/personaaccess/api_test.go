package personaaccess

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/persona"
)

func pass(h http.Handler) http.Handler { return h }

// The route evaluates the unsaved persona in its body, with the stores'
// references, and answers a draft the decoder refuses with 400.
func TestServeEvaluatesTheDraft(t *testing.T) {
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "engineer", Roles: []string{"staff"}, Priority: 20}))
	s := New(Config{
		Personas: reg, AdminPersona: "admin",
		Inventory:   func(context.Context) Inventory { return inv() },
		RoleMapping: map[string]string{"oidc-analysts": "analyst"},
	})
	mux := http.NewServeMux()
	Register(mux, pass, s)

	body := `{"name":"analyst","roles":["staff"],"allow_tools":["*"],"allow_connections":["acme-*"],
		"portal":{"scratch_tables":"list","external":true},"external":false}`
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, Path, strings.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var r Report
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r))
	assert.Equal(t, AccountInternal, r.AccountType, "external inside the portal block is reported, never set")
	assert.Equal(t, []string{"oidc-analysts"}, r.Assignment.RoleMapping)
	assert.Equal(t, "engineer", r.Assignment.RoleOverlaps[0].Persona)
	require.Len(t, r.Scratch, 1)
	assert.True(t, r.Scratch[0].List)
	assert.False(t, r.Scratch[0].Register)
	assert.NotNil(t, r.References.WebhookSources, "no database reads as no references")

	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, Path, strings.NewReader(`{"bogus":1}`)))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// A draft named as the administrator persona is the administrator unless it
// is external, as Registry.AdminRule decides it saved.
func TestServiceAdministratorDraft(t *testing.T) {
	s := New(Config{Personas: persona.NewRegistry(), AdminPersona: "admin"})
	assert.True(t, s.isAdmin(&persona.Persona{Name: "admin"}))
	assert.False(t, s.isAdmin(&persona.Persona{Name: "admin", External: true}))
	assert.False(t, New(Config{Personas: persona.NewRegistry()}).isAdmin(&persona.Persona{Name: "admin"}))
	r, err := s.Report(t.Context(), &persona.Persona{Name: "admin"})
	require.NoError(t, err)
	assert.True(t, r.IsAdmin)
	assert.Equal(t, "admin", r.AdminPersona)
}

// A role resolves to the draft as the registry would resolve it saved: the
// draft in place of the stored persona, role_mapping first.
func TestResolvesToTheDraft(t *testing.T) {
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "analyst", Roles: []string{"old"}}))
	require.NoError(t, reg.Register(&persona.Persona{Name: "engineer", Roles: []string{"eng"}, Priority: 50}))
	s := New(Config{Personas: reg, RoleMapping: map[string]string{"oidc": "analyst"}})
	resolves := s.resolvesTo(&persona.Persona{Name: "analyst", Roles: []string{"new", "eng"}})
	assert.True(t, resolves([]string{"new"}))
	assert.False(t, resolves([]string{"old"}), "the stored roles are replaced by the draft's")
	assert.False(t, resolves([]string{"eng"}), "a higher-priority persona wins")
	assert.True(t, resolves([]string{"oidc", "eng"}), "role_mapping names the persona outright")
}

// The stored persona's references come from the store; a persona not
// registered has none, and a failed read is reported.
func TestServiceReferences(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "analyst", Roles: []string{"analyst"}}))
	s := New(Config{Personas: reg, DB: db})

	refs, err := s.References(t.Context(), "nobody")
	require.NoError(t, err)
	assert.True(t, refs.Empty())

	mock.ExpectQuery("FROM gateway_secrets").WithArgs("analyst", false).WillReturnRows(
		sqlmock.NewRows([]string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}).
			AddRow("{crm-token}", 2, 1, 0, 3, "{}", "{orders}", 1, 2))
	mock.ExpectQuery("FROM script_versions").WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow("{analyst}").AddRow("{x}"))
	mock.ExpectQuery("FROM api_keys").WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow([]byte(`["analyst"]`)))
	mock.ExpectQuery("FROM users").WillReturnRows(sqlmock.NewRows([]string{"e", "r"}).
		AddRow("ann@example.com", []byte(`["analyst"]`)).AddRow("fin@example.com", []byte(`["finance"]`)))
	refs, err = s.References(t.Context(), "analyst")
	require.NoError(t, err)
	assert.Equal(t, []string{"ann@example.com"}, refs.People, "the people whose roles resolve to it")
	assert.Equal(t, []string{"crm-token"}, refs.Secrets.Named)
	assert.Equal(t, 1, refs.ScriptVersions)
	assert.Equal(t, 1, refs.APIKeys)
	assert.Equal(t, 3, refs.Shares)
	assert.Equal(t, []string{"orders"}, refs.Endpoints, "the authenticated endpoints whose callers name it")

	mock.ExpectQuery("FROM gateway_secrets").WillReturnError(assert.AnError)
	_, err = s.References(t.Context(), "analyst")
	require.Error(t, err)

	mock.ExpectQuery("FROM gateway_secrets").WillReturnRows(
		sqlmock.NewRows([]string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}).AddRow("{}", 0, 0, 0, 0, "{}", "{}", 0, 0))
	mock.ExpectQuery("FROM script_versions").WillReturnError(assert.AnError)
	_, err = s.References(t.Context(), "analyst")
	require.Error(t, err)

	mock.ExpectQuery("FROM gateway_secrets").WillReturnRows(
		sqlmock.NewRows([]string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}).AddRow("{}", 0, 0, 0, 0, "{}", "{}", 0, 0))
	mock.ExpectQuery("FROM script_versions").WillReturnRows(sqlmock.NewRows([]string{"r"}))
	mock.ExpectQuery("FROM api_keys").WillReturnRows(sqlmock.NewRows([]string{"r"}).AddRow([]byte(`not json`)))
	_, err = s.References(t.Context(), "analyst")
	require.Error(t, err)

	people := func(rows *sqlmock.Rows, err error) {
		mock.ExpectQuery("FROM gateway_secrets").WillReturnRows(
			sqlmock.NewRows([]string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}).AddRow("{}", 0, 0, 0, 0, "{}", "{}", 0, 0))
		mock.ExpectQuery("FROM script_versions").WillReturnRows(sqlmock.NewRows([]string{"r"}))
		mock.ExpectQuery("FROM api_keys").WillReturnRows(sqlmock.NewRows([]string{"r"}))
		q := mock.ExpectQuery("FROM users")
		if err != nil {
			q.WillReturnError(err)
			return
		}
		q.WillReturnRows(rows)
	}
	people(nil, assert.AnError)
	_, err = s.References(t.Context(), "analyst")
	require.Error(t, err)
	people(sqlmock.NewRows([]string{"e", "r"}).AddRow("a@example.com", []byte(`{`)), nil)
	_, err = s.References(t.Context(), "analyst")
	require.Error(t, err)
	people(sqlmock.NewRows([]string{"e"}).AddRow("a@example.com"), nil)
	_, err = s.References(t.Context(), "analyst")
	require.Error(t, err)
	people(sqlmock.NewRows([]string{"e", "r"}).AddRow("a@example.com", []byte(`[]`)).RowError(0, assert.AnError), nil)
	_, err = s.References(t.Context(), "analyst")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRegistrationsWithoutADatabase(t *testing.T) {
	assert.Empty(t, Registrations(t.Context(), nil))
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.ExpectQuery("FROM table_registrations").WillReturnError(assert.AnError)
	assert.Empty(t, Registrations(t.Context(), db))
	mock.ExpectQuery("FROM table_registrations").WillReturnRows(sqlmock.NewRows([]string{"c", "n"}).AddRow("acme-trino", 3))
	assert.Equal(t, map[string]int{"acme-trino": 3}, Registrations(t.Context(), db))
}
