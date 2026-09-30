//go:build integration

package callrecord_test

// A persona marked as a service account over a real PostgreSQL database
// (#1980): its calls are audited and not cataloged, the records it wrote before
// it was marked go on the next sweep with their index units, a backlog larger
// than one batch goes in several statements, and the admin count of who wrote
// the catalog is a statement PostgreSQL accepts.

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	auditpostgres "github.com/txn2/mcp-data-platform/pkg/audit/postgres"
	"github.com/txn2/mcp-data-platform/pkg/persona"
)

const (
	servicePersona = "integration"
	serviceID      = "apikey:crm-sync"
)

// serviceRegistry is the live persona registry with the integration persona
// unmarked and the analyst persona beside it.
func serviceRegistry(t *testing.T) *persona.Registry {
	t.Helper()
	reg := persona.NewRegistry()
	require.NoError(t, reg.Register(&persona.Persona{Name: servicePersona, Roles: []string{"crm-sync"}}))
	require.NoError(t, reg.Register(&persona.Persona{Name: "analyst", Roles: []string{"analyst"}}))
	return reg
}

// markServiceAccount is what the admin API does when the persona is saved with
// the setting on: the registry holds the marked persona from then on.
func markServiceAccount(t *testing.T, reg *persona.Registry) {
	t.Helper()
	require.NoError(t, reg.Register(&persona.Persona{
		Name: servicePersona, Roles: []string{"crm-sync"}, ServiceAccount: true,
	}))
}

// apiEvent is one successful api_invoke_endpoint audit event.
func apiEvent(id, userID, personaName string) audit.Event {
	return audit.Event{
		ID: id, Timestamp: time.Now().UTC(), ToolName: "api_invoke_endpoint", ToolkitKind: "api",
		Connection: "crm", UserID: userID, Persona: personaName, Success: true,
		Parameters: map[string]any{"method": "GET", "path": "/v1/constituents/42"},
		Purpose:    "Syncing one constituent.",
	}
}

// countRows counts rows the statement selects, read from the table itself.
func countRows(ctx context.Context, t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRowContext(ctx, query, args...).Scan(&n))
	return n
}

func TestServiceAccountRealDBIsAuditedAndNotCataloged(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	reg := serviceRegistry(t)
	calls := callrecord.NewPostgresStore(db, callrecord.Config{RetentionDays: 90, ServiceAccounts: reg})
	auditStore := auditpostgres.New(db, auditpostgres.Config{RetentionDays: 30})
	logger := callrecord.NewRecorder(auditStore, calls, nil, calls.Exclusion())

	// Unmarked, the integration's call is cataloged like anyone's.
	require.NoError(t, logger.Log(ctx, apiEvent("evt-before", serviceID, servicePersona)))
	assert.Equal(t, 1, countCallRecords(ctx, t, db, servicePersona))

	// Marked at run time, with no new store or restart: the next call is
	// audited and writes no record.
	markServiceAccount(t, reg)
	require.NoError(t, logger.Log(ctx, apiEvent("evt-after", serviceID, servicePersona)))
	assert.Equal(t, 1, countRows(ctx, t, db, `SELECT COUNT(*) FROM audit_logs WHERE id = 'evt-after'`),
		"the marked caller's call is audited")
	_, err := calls.GetByEventID(ctx, "evt-after", serviceID)
	assert.ErrorIs(t, err, callrecord.ErrNotFound, "and not cataloged")

	// A person's call under an unmarked persona is cataloged as before.
	require.NoError(t, logger.Log(ctx, apiEvent("evt-person", analystID, "analyst")))
	if _, err := calls.GetByEventID(ctx, "evt-person", analystID); err != nil {
		t.Errorf("a person's call under an unmarked persona must be cataloged, got %v", err)
	}
}

func TestServiceAccountRealDBSweepRemovesRecordsAndTheirIndexUnits(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	reg := serviceRegistry(t)
	calls := callrecord.NewPostgresStore(db, callrecord.Config{RetentionDays: 90, ServiceAccounts: reg})
	auditStore := auditpostgres.New(db, auditpostgres.Config{RetentionDays: 30})
	logger := callrecord.NewRecorder(auditStore, calls, nil, calls.Exclusion())

	for i := range 3 {
		require.NoError(t, logger.Log(ctx, apiEvent(fmt.Sprintf("svc-%d", i), serviceID, servicePersona)))
	}
	require.NoError(t, logger.Log(ctx, apiEvent("person-0", analystID, "analyst")))

	// Every record has an index unit, in each state a unit can be in: the
	// sweep must take them all, since a pending one would otherwise be claimed
	// for a record that is gone and a finished one would go on being counted.
	statuses := []string{"pending", "running", "succeeded"}
	_, err := db.ExecContext(ctx, `
		INSERT INTO index_jobs (source_kind, source_id, trigger_kind, status)
		SELECT $1, id::text, 'write', ($2::text[])[1 + (row_number() OVER (ORDER BY id))::int % 3]
		  FROM call_records`, callrecord.IndexSourceKind, "{"+statuses[0]+","+statuses[1]+","+statuses[2]+"}")
	require.NoError(t, err)
	unitsOf := func(personaName string) int {
		return countRows(ctx, t, db, `
			SELECT COUNT(*) FROM index_jobs j
			 WHERE j.source_kind = $1
			   AND EXISTS (SELECT 1 FROM call_records r WHERE r.id::text = j.source_id AND r.persona = $2)`,
			callrecord.IndexSourceKind, personaName)
	}
	unitsBefore := countRows(ctx, t, db, `SELECT COUNT(*) FROM index_jobs WHERE source_kind = $1`, callrecord.IndexSourceKind)
	require.Equal(t, 4, unitsBefore)
	require.Equal(t, 3, unitsOf(servicePersona))

	// Nothing is past the retention window and nothing is excluded, so the
	// sweep removes nothing.
	removed, err := calls.Cleanup(ctx)
	require.NoError(t, err)
	assert.Zero(t, removed)

	markServiceAccount(t, reg)
	removed, err = calls.Cleanup(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), removed, "the marked persona's records, whatever their age")
	assert.Zero(t, countCallRecords(ctx, t, db, servicePersona))
	assert.Equal(t, 1, countCallRecords(ctx, t, db, "analyst"), "the person's record stays")

	unitsAfter := countRows(ctx, t, db, `SELECT COUNT(*) FROM index_jobs WHERE source_kind = $1`, callrecord.IndexSourceKind)
	assert.Equal(t, unitsBefore-3, unitsAfter, "the index loses exactly the units of the removed records")
	assert.Equal(t, 1, unitsOf("analyst"))
}

func TestServiceAccountRealDBSweepsABacklogInBatches(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	reg := serviceRegistry(t)
	calls := callrecord.NewPostgresStore(db, callrecord.Config{RetentionDays: 90, ServiceAccounts: reg})

	// More than one batch, written the way the recorder writes them.
	const backlog = 5003
	_, err := db.ExecContext(ctx, `
		INSERT INTO call_records (event_id, kind, tool_name, user_id, persona, success, path)
		SELECT 'backlog-' || g, 'api', 'api_invoke_endpoint', $1, $2, TRUE, '/v1/gifts/' || g
		  FROM generate_series(1, $3) AS g`, serviceID, servicePersona, backlog)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO index_jobs (source_kind, source_id, trigger_kind, status)
		SELECT $1, id::text, 'write', 'pending' FROM call_records`, callrecord.IndexSourceKind)
	require.NoError(t, err)

	top, err := calls.TopCallers(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, top.Personas)
	assert.Equal(t, backlog, top.Total)
	assert.Equal(t, servicePersona, top.Personas[0].Persona)
	assert.InDelta(t, 1.0, top.Personas[0].Share, 1e-9)
	assert.False(t, top.Personas[0].ServiceAccount)
	require.NotEmpty(t, top.Principals)
	assert.Equal(t, serviceID, top.Principals[0].UserID)

	markServiceAccount(t, reg)
	removed, err := calls.Cleanup(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(backlog), removed)
	assert.Zero(t, countCallRecords(ctx, t, db, servicePersona))
	assert.Zero(t, countRows(ctx, t, db, `SELECT COUNT(*) FROM index_jobs WHERE source_kind = $1`, callrecord.IndexSourceKind))

	// The sweep removed records, so the count is taken again.
	top, err = calls.TopCallers(ctx)
	require.NoError(t, err)
	assert.Zero(t, top.Total)
	assert.Empty(t, top.Personas)
}
