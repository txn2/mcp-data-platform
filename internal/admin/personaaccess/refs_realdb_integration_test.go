//go:build integration

package personaaccess

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

// The references are read from the tables that hold them, each matching what
// the store wrote (#2257): a secret naming the persona and one naming none, a
// persona script grant, the latest version of a script saved with roles that
// resolve to it, a persona-scoped prompt, a share to the persona, a webhook
// source writing to it, an external account under it, and an API key whose
// roles resolve to it.
func TestRealDB_ReferencesReadEveryTableThatNamesAPersona(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`INSERT INTO gateway_secrets (name, value, allow_personas) VALUES ('crm-token', 'x', '{analyst}'), ('open', 'y', '{}'), ('other', 'z', '{finance}')`,
		`INSERT INTO scripts (id, name) VALUES ('00000000-0000-0000-0000-000000000001', 'nightly'), ('00000000-0000-0000-0000-000000000002', 'weekly')`,
		`INSERT INTO script_versions (script_id, version, author_roles) VALUES
			('00000000-0000-0000-0000-000000000001', 1, '{finance}'),
			('00000000-0000-0000-0000-000000000001', 2, '{analyst}'),
			('00000000-0000-0000-0000-000000000002', 1, '{finance}')`,
		`INSERT INTO script_grants (script_id, principal_kind, principal) VALUES ('00000000-0000-0000-0000-000000000002', 'persona', 'analyst')`,
		`INSERT INTO prompts (id, name, scope, personas) VALUES
			('00000000-0000-0000-0000-0000000000a1', 'p1', 'persona', '{analyst}'),
			('00000000-0000-0000-0000-0000000000a2', 'p2', 'personal', '{analyst}')`,
		`INSERT INTO portal_shares (id, prompt_id, token, created_by, shared_with_persona) VALUES
			('s1', '00000000-0000-0000-0000-0000000000a1', 't1', 'owner@example.com', 'analyst'),
			('s2', '00000000-0000-0000-0000-0000000000a1', 't2', 'owner@example.com', 'finance')`,
		`INSERT INTO webhook_sources (name, connection_name, config) VALUES
			('stripe-events', 'acme-trino', '{"persona":"analyst"}'), ('esp', 'acme-trino', '{}')`,
		`INSERT INTO external_accounts (id, email, persona) VALUES ('ext1', 'vendor@example.com', 'analyst')`,
		`INSERT INTO api_keys (name, key_hash, roles) VALUES ('etl', 'h1', '["analyst"]'), ('ops', 'h2', '["finance"]')`,
		`INSERT INTO table_registrations (id, source_kind, source_id, connection_name, catalog_name, schema_name, table_name, location, registered_by)
			VALUES ('reg_1', 'resource', 'r1', 'acme-trino', 'scratch', 'uploads', 't', 's3://b/k', 'a@example.com')`,
	} {
		_, err := db.ExecContext(ctx, stmt)
		require.NoError(t, err, stmt)
	}

	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: "analyst", Roles: []string{"analyst"}}))
	require.NoError(t, reg.Register(&persona.Persona{Name: "finance", Roles: []string{"finance"}}))
	s := New(Config{Personas: reg, AdminPersona: "admin", DB: db, FileKeyRoles: [][]string{{"analyst"}}})

	refs, err := s.References(ctx, "analyst")
	require.NoError(t, err)
	assert.Equal(t, []string{"crm-token"}, refs.Secrets.Named)
	assert.Equal(t, 1, refs.Secrets.Unrestricted)
	assert.Equal(t, 1, refs.ScriptGrants)
	assert.Equal(t, 1, refs.ScriptVersions, "the latest version of nightly; weekly's roles resolve to finance")
	assert.Equal(t, 1, refs.Prompts, "a personal prompt carrying the name is not scoped to the persona")
	assert.Equal(t, 1, refs.Shares)
	assert.Equal(t, []string{"stripe-events"}, refs.WebhookSources)
	assert.Equal(t, 1, refs.ExternalAccounts)
	assert.Equal(t, 2, refs.APIKeys, "one stored key and one from the file")

	admin := New(Config{Personas: reg, AdminPersona: "admin", DB: db})
	require.NoError(t, reg.Register(&persona.Persona{Name: "admin", Roles: []string{"admin"}}))
	adminRefs, err := admin.References(ctx, "admin")
	require.NoError(t, err)
	assert.Equal(t, []string{"esp"}, adminRefs.WebhookSources, "a source naming no persona writes to the administrator's")

	assert.Equal(t, map[string]int{"acme-trino": 1}, Registrations(ctx, db))
}
