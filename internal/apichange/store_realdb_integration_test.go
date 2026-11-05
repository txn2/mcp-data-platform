//go:build integration

package apichange

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	apicatalog "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// The real-schema proof for #2469: every statement of the store, warnings,
// callers and used-by read runs against a migrated database.
func TestRealDB_WatchChangesWarningsAndCallers(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	catalogs := apicatalog.NewPostgresStore(db)
	require.NoError(t, catalogs.CreateCatalog(ctx, apicatalog.Catalog{ID: "acme", Name: "acme", DisplayName: "Acme"}))
	require.NoError(t, catalogs.UpsertSpec(ctx, "acme", apicatalog.SpecEntry{SpecName: "pets", Content: specV1, SourceKind: apicatalog.SourceURL, SourceURL: "https://vendor.example/s"}))
	s := &Store{DB: db}

	// A watch saved, made due, checked, and the next check one interval on.
	require.NoError(t, s.SaveSettings(ctx, Watch{SpecKey: key, RefreshInterval: time.Hour, HoldChanges: true}, "a@example.com"))
	w, err := s.GetWatch(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, time.Hour, w.RefreshInterval)
	require.NotNil(t, w.NextCheckAt)
	assert.WithinDuration(t, time.Now().Add(time.Hour), *w.NextCheckAt, time.Minute)
	due, err := s.Due(ctx, 10)
	require.NoError(t, err)
	assert.Empty(t, due)
	_, err = db.ExecContext(ctx, `UPDATE api_catalog_spec_watch SET next_check_at = NOW() - interval '1 second'`)
	require.NoError(t, err)
	due, err = s.Due(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, []SpecKey{key}, due)
	require.NoError(t, s.RecordCheck(ctx, key, CheckRecord{Err: "boom", Upstream: true, UpstreamETag: `"u1"`, UpstreamBody: "doc"}))
	w, err = s.GetWatch(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, "boom", w.LastError)
	assert.Equal(t, `"u1"`, w.UpstreamETag)
	assert.WithinDuration(t, time.Now().Add(time.Hour), *w.NextCheckAt, time.Minute)
	require.NoError(t, s.SaveSettings(ctx, Watch{SpecKey: key, UpstreamURL: "https://other.example/s"}, "a"))
	w, _ = s.GetWatch(ctx, key)
	assert.Empty(t, w.UpstreamContent, "a new upstream URL forgets the copy read from the old one")
	assert.Nil(t, w.NextCheckAt)

	// Changes: one pending at a time, a newer one superseding it.
	report := Report{Added: []Op{{ID: "createPet"}}, Removed: []Op{}, Changed: []Changed{}, Deprecated: []Op{}}
	require.NoError(t, s.AddChange(ctx, Change{ID: "c1", CatalogID: "acme", SpecName: "pets", Status: StatusPending, Trigger: TriggerSchedule, Report: report, PendingContent: "doc1"}))
	require.NoError(t, s.AddChange(ctx, Change{ID: "c2", CatalogID: "acme", SpecName: "pets", Status: StatusPending, Trigger: TriggerManual, Report: report, PendingContent: "doc2"}))
	pending, err := s.PendingFor(ctx, "acme")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"pets": "c2"}, pending)
	c1, err := s.GetChange(ctx, key, "c1")
	require.NoError(t, err)
	assert.Equal(t, StatusSuperseded, c1.Status)
	assert.Empty(t, c1.PendingContent)
	require.NoError(t, s.Decide(ctx, key, "c2", StatusApplied, "a"))
	require.ErrorIs(t, s.Decide(ctx, key, "c2", StatusApplied, "a"), ErrNotPending)
	list, err := s.ListChanges(ctx, key, 50)
	require.NoError(t, err)
	assert.Len(t, list, 2)

	// Warnings: a sunset sent once, a removal only after a success.
	sent := &warned{}
	wr := NewWarnings(db, sent)
	require.NoError(t, wr.warn(ctx, "vendor", "getPet", WarningSunset, "2027"))
	require.NoError(t, wr.warn(ctx, "vendor", "getPet", WarningSunset, "2027"))
	assert.Len(t, sent.list, 1, "the second write updated the row and sent nothing")
	require.NoError(t, wr.removal(ctx, "vendor", "deletePet", "answered 410"))
	assert.Len(t, sent.list, 1, "an operation that never answered 2xx is not removed")
	require.NoError(t, wr.success(ctx, "vendor", "deletePet"))
	require.NoError(t, wr.removal(ctx, "vendor", "deletePet", "answered 410"))
	assert.Len(t, sent.list, 2)
	warnings, err := ListWarnings(ctx, db, "vendor")
	require.NoError(t, err)
	require.Len(t, warnings, 2)
	assert.NotNil(t, warnings[0].NotifiedAt)
	wr.Observe(ctx, "vendor", "listPets", http.StatusOK, http.Header{"Deprecation": {"true"}})
	settle(wr)

	// Callers: a script's run recorded from its session, and read back by the
	// used-by index with the script's source and a prompt.
	var scriptID string
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO scripts (name, source_code, owner_email)
		VALUES ('nightly', $1, 'jane@example.com') RETURNING id::text`, scriptSource).Scan(&scriptID))
	var versionID string
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO script_versions (script_id, version, source_code, author)
		VALUES ($1, 1, $2, 'jane@example.com') RETURNING id::text`, scriptID, scriptSource).Scan(&versionID))
	_, err = db.ExecContext(ctx, `INSERT INTO script_runs (id, script_id, script_version_id, version, trigger_kind, status, requested_by)
		VALUES ('dpx_run1', $1, $2, 1, 'tool', 'succeeded', 'jane@example.com')`, scriptID, versionID)
	require.NoError(t, err)
	callers := NewCallers(&innerLog{}, db, func(_ context.Context, _, _, _, op string) string { return op })
	require.NoError(t, callers.Log(ctx, audit.Event{
		ToolName: "api_invoke_endpoint", UserID: "script:nightly",
		Connection: "vendor", SessionID: "dpx_run1", Parameters: map[string]any{"operation_id": "getPet"},
	}))
	settle(callers.w)
	_, err = db.ExecContext(ctx, `INSERT INTO prompts (name, content) VALUES ('triage', 'On vendor, call getPet first.')`)
	require.NoError(t, err)

	got, err := (Usage{DB: db}).Callers(ctx, []string{"vendor"}, NewOperationIndex(specV1))
	require.NoError(t, err)
	kinds := map[string][]string{}
	for op, list := range got {
		for _, c := range list {
			kinds[op] = append(kinds[op], c.Kind+":"+c.Via)
		}
	}
	assert.ElementsMatch(t, []string{"script:source"}, kinds["listPets"])
	assert.ElementsMatch(t, []string{"script:source"}, kinds["deletePet"])
	assert.ElementsMatch(t, []string{"prompt:prompt", "script:run"}, kinds["getPet"])
	require.NoError(t, PruneCallers(ctx, db))
}
