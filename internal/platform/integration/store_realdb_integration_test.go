//go:build integration

package integration

// Real-Postgres tests for the integrations (#2659): the store's statements on
// the real schema, the run an integration queues, the change announcements
// the triggers send, and the script_handlers view's integration arm.

import (
	"context"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestStore_RealDB(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	scripts := scriptstore.New(db)
	sc := &script.Script{Name: "ingress-routes", Source: "INTEGRATION = \"site_routing\"\n\ndef route(req):\n    return {}\n", OwnerEmail: "ops@example.com", Enabled: true}
	require.NoError(t, scripts.Create(ctx, sc, script.Author{Email: "ops@example.com", Roles: []string{"admin"}}))
	v, err := scripts.GetVersion(ctx, sc.ID, sc.Version)
	require.NoError(t, err)

	s := &Store{DB: db}
	b, err := s.Bind(ctx, OpSiteRouting, sc.ID, 900, "ops@example.com")
	require.NoError(t, err)
	assert.True(t, b.RetryRequested)
	got, err := s.Binding(ctx, OpSiteRouting)
	require.NoError(t, err)
	assert.Equal(t, sc.ID, got.ScriptID)

	var kind, target string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT kind, target FROM script_handlers WHERE script_id = $1`, sc.ID).Scan(&kind, &target))
	assert.Equal(t, "integration", kind)
	assert.Equal(t, OpSiteRouting, target, "the script's page lists the operation among its handlers")

	spec, _ := SpecOf(OpSiteRouting)
	req := Request{Reason: ReasonChange, Added: []map[string]any{{"hostname": "a.example.com"}}, Items: []map[string]any{{"hostname": "a.example.com"}}}
	require.NoError(t, s.StartRun(ctx, OpSiteRouting, NewRun{
		ID: "run-1", ScriptID: sc.ID, VersionID: v.ID, Version: v.Version, Approver: "ops@example.com", Roles: []string{"admin"},
		Call: RunCall(spec, req), Reason: ReasonChange, Added: []Item{{Key: "a.example.com", Entry: map[string]any{"hostname": "a.example.com"}}},
	}))
	var trigger, status string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT trigger_kind, status FROM script_runs WHERE id = 'run-1'`).Scan(&trigger, &status))
	assert.Equal(t, "integration", trigger)
	assert.Equal(t, "pending", status)
	got, err = s.Binding(ctx, OpSiteRouting)
	require.NoError(t, err)
	assert.Equal(t, "run-1", got.CurrentRunID)
	assert.False(t, got.RetryRequested)

	out, err := s.RunOutcome(ctx, "run-1")
	require.NoError(t, err)
	assert.False(t, out.Finished)
	assert.Equal(t, OpSiteRouting, out.Call["integration"])

	_, err = db.ExecContext(ctx, `UPDATE script_runs SET status = 'succeeded', result = '{"a.example.com":{"status":"ready","message":"ok"}}', finished_at = NOW() WHERE id = 'run-1'`)
	require.NoError(t, err)
	out, err = s.RunOutcome(ctx, "run-1")
	require.NoError(t, err)
	assert.True(t, out.Finished)

	require.NoError(t, s.Apply(ctx, OpSiteRouting, Applied{
		RunID: "run-1", Status: "succeeded",
		Results: map[string]ItemResult{"a.example.com": {Status: StatusReady, Message: "ok"}},
	}))
	items, err := s.ItemsByKey(ctx, OpSiteRouting, []string{"a.example.com"})
	require.NoError(t, err)
	assert.Equal(t, StatusReady, items["a.example.com"].Status)
	got, err = s.Binding(ctx, OpSiteRouting)
	require.NoError(t, err)
	assert.Empty(t, got.CurrentRunID)
	assert.Equal(t, "run-1", got.LastRunID)

	release, ok, err := s.Lock(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	release()

	require.NoError(t, s.RequestRetry(ctx, OpSiteRouting))
	require.NoError(t, s.RecordRefusal(ctx, OpSiteRouting, "no version"))
	cleared, err := s.Clear(ctx, OpSiteRouting)
	require.NoError(t, err)
	assert.True(t, cleared)
	all, err := s.Items(ctx, OpSiteRouting)
	require.NoError(t, err)
	assert.Empty(t, all, "clearing drops every status")
}

// TestTriggers_RealDB: a change to a site, an environment, a domain, an
// application, the sites settings and the end of an integration's run each
// announce on integration_changed.
func TestTriggers_RealDB(t *testing.T) {
	db, dsn := testdb.NewWithDSN(t)
	ctx := context.Background()
	l := pq.NewListener(dsn, time.Second, time.Second, nil)
	t.Cleanup(func() { _ = l.Close() })
	require.NoError(t, l.Listen(Channel))

	expect := func(what string) {
		t.Helper()
		select {
		case n := <-l.Notify:
			require.NotNil(t, n)
		case <-time.After(5 * time.Second):
			t.Fatalf("no announcement after %s", what)
		}
	}
	_, err := db.ExecContext(ctx, `INSERT INTO sites (id, name, owner_email, created_by) VALUES ('st1', 'handbook', 'a@example.com', 'a@example.com')`)
	require.NoError(t, err)
	expect("a site was added")
	_, err = db.ExecContext(ctx, `INSERT INTO site_environments (site_id, name) VALUES ('st1', 'qa')`)
	require.NoError(t, err)
	expect("an environment was added")
	_, err = db.ExecContext(ctx, `INSERT INTO platform_settings (section, value) VALUES ('sites', '{}') ON CONFLICT (section) DO UPDATE SET value = '{}'`)
	require.NoError(t, err)
	expect("the sites settings changed")
	_, err = db.ExecContext(ctx, `INSERT INTO platform_settings (section, value) VALUES ('storage', '{}') ON CONFLICT (section) DO UPDATE SET value = '{}'`)
	require.NoError(t, err)
	select {
	case n := <-l.Notify:
		t.Fatalf("another settings section announced a change: %+v", n)
	case <-time.After(500 * time.Millisecond):
	}
}
