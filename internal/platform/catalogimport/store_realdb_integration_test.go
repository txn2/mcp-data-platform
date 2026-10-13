//go:build integration

package catalogimport

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/catalogstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// The mapping keeps what each URN became, and a second record of one URN
// replaces the first.
func TestStore_Mapping_RealDB(t *testing.T) {
	ctx := context.Background()
	s := NewStore(testdb.New(t))
	require.NoError(t, s.Record(ctx, termURN, "mcp:glossary_term:a"))
	require.NoError(t, s.Record(ctx, termURN, "mcp:glossary_term:b"))
	require.NoError(t, s.Record(ctx, ordersURN, "mcp:dataset:(trino,w,hive.sales.orders)"))
	got, err := s.Map(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{termURN: "mcp:glossary_term:b", ordersURN: "mcp:dataset:(trino,w,hive.sales.orders)"}, got)
}

// A run is recorded at its start and its end, its report reads back, and an
// unfinished run reads as running only while the lock is held.
func TestStore_Runs_RealDB(t *testing.T) {
	ctx := context.Background()
	s := NewStore(testdb.New(t))
	first, err := s.begin(ctx, "http://datahub-a:8080", "ana")
	require.NoError(t, err)
	report := newReport()
	report.Datasets.Imported = 3
	require.NoError(t, s.finish(ctx, first.ID, report, ""))
	time.Sleep(10 * time.Millisecond)
	second, err := s.begin(ctx, "http://datahub-b:8080", "bo")
	require.NoError(t, err)

	runs, err := s.Runs(ctx, 10, true)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	assert.Equal(t, second.ID, runs[0].ID)
	assert.True(t, runs[0].Running)
	assert.Nil(t, runs[0].FinishedAt)
	assert.Equal(t, first.ID, runs[1].ID)
	require.NotNil(t, runs[1].Report)
	assert.Equal(t, 3, runs[1].Report.Datasets.Imported)
	assert.False(t, runs[1].Running)

	runs, err = s.Runs(ctx, 10, false)
	require.NoError(t, err)
	assert.False(t, runs[0].Running)
	assert.Contains(t, runs[0].Error, "stopped before it finished")
}

// The runner refuses a second import while one holds the lock, and the run
// it started records its end.
func TestRunner_OneAtATime_RealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	release := make(chan struct{})
	src := &blockingSource{release: release}
	rn := NewRunner(db, New(catalogstore.New(db), NewStore(db), Options{}), nil,
		func(string, string) (Source, error) { return src, nil })
	t.Cleanup(rn.Stop)

	run, err := rn.Start(ctx, "http://datahub:8080", "token", "ana")
	require.NoError(t, err)
	_, err = rn.Start(ctx, "http://datahub:8080", "token", "ana")
	require.ErrorIs(t, err, ErrRunning)
	runs, err := rn.Runs(ctx, 10)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.True(t, runs[0].Running)

	close(release)
	require.Eventually(t, func() bool {
		runs, err := rn.Runs(ctx, 10)
		return err == nil && runs[0].FinishedAt != nil
	}, 10*time.Second, 20*time.Millisecond)
	runs, err = rn.Runs(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, run.ID, runs[0].ID)
	assert.False(t, runs[0].Running)
	assert.Empty(t, runs[0].Error)

	_, err = rn.Start(ctx, "http://datahub:8080", "token", "ana")
	require.NoError(t, err, "the lock is free once the run ended")
}

// blockingSource holds the import until release is closed.
type blockingSource struct{ release chan struct{} }

func (b *blockingSource) DatasetHeads(context.Context) ([]DatasetHead, error) {
	<-b.release
	return nil, nil
}
func (*blockingSource) Dataset(context.Context, string) (*DatasetRecord, error) { return nil, nil }
func (*blockingSource) Definitions(context.Context) ([]Definition, error)       { return nil, nil }

// The rewrite turns the DataHub URNs every rewritten column holds into the
// references the resolver gives, leaves and reports the ones it gives none
// for, moves a page's DataHub citation to a catalog citation, and drops the
// dataset index's DataHub rows.
func TestRewriter_RealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	ds := "mcp:dataset:(trino,w,hive.sales.orders)"
	resolve := func(urn string) string {
		switch urn {
		case ordersURN:
			return ds
		case termURN:
			return "mcp:glossary_term:t1"
		}
		return ""
	}
	exec := func(q string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, q, args...)
		require.NoError(t, err, q)
	}
	exec(`INSERT INTO memory_records (id, content, entity_urns, related_columns) VALUES ($1, $2, $3, $4)`,
		"m1", "See "+ordersURN+" and urn:li:corpuser:ana.",
		`["`+ordersURN+`","mcp:asset:a1"]`, `[{"urn":"`+ordersURN+`","column":"amount"}]`)
	exec(`INSERT INTO knowledge_changesets (id, target_urn, change_type) VALUES ('c1', $1, 'add_tag')`, ordersURN)
	exec(`INSERT INTO call_records (event_id, kind, tool_name, targets, promoted_urn) VALUES ('e1', 'sql', 'trino_query', $1, $2)`,
		`["`+ordersURN+`"]`, "urn:li:query:q1")
	exec(`INSERT INTO portal_knowledge_pages (id, title, body, summary) VALUES ('p1', 'Orders', $1, $2)`,
		"Revenue is "+termURN+".", "About "+ordersURN)
	exec(`INSERT INTO knowledge_page_entity_refs (id, page_id, target_type, entity_urn) VALUES ('r1', 'p1', 'datahub', $1)`, ordersURN)
	exec(`INSERT INTO knowledge_page_entity_refs (id, page_id, target_type, entity_urn) VALUES ('r2', 'p1', 'datahub', $1)`, termURN)
	exec(`INSERT INTO knowledge_page_entity_refs (id, page_id, target_type, entity_urn) VALUES ('r3', 'p1', 'catalog', 'mcp:glossary_term:t1')`)
	exec(`INSERT INTO knowledge_page_entity_refs (id, page_id, target_type, entity_urn) VALUES ('r4', 'p1', 'datahub', 'urn:li:chart:c')`)
	exec(`INSERT INTO catalog_datasets (urn, name) VALUES ($1, 'orders')`, ordersURN)

	report := newReport()
	require.NoError(t, NewRewriter(db).Rewrite(ctx, resolve, report))

	var content, urns, related, target, targets, promoted, body, summary string
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT content, entity_urns::text, related_columns::text FROM memory_records WHERE id = 'm1'`).Scan(&content, &urns, &related))
	assert.Equal(t, "See "+ds+" and urn:li:corpuser:ana.", content)
	assert.JSONEq(t, `["`+ds+`","mcp:asset:a1"]`, urns)
	assert.JSONEq(t, `[{"urn":"`+ds+`","column":"amount"}]`, related)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT target_urn FROM knowledge_changesets WHERE id = 'c1'`).Scan(&target))
	assert.Equal(t, ds, target)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT targets::text, promoted_urn FROM call_records WHERE event_id = 'e1'`).Scan(&targets, &promoted))
	assert.JSONEq(t, `["`+ds+`"]`, targets)
	assert.Equal(t, "urn:li:query:q1", promoted, "a query entity has no catalog counterpart and stays")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT body, summary FROM portal_knowledge_pages WHERE id = 'p1'`).Scan(&body, &summary))
	assert.Equal(t, "Revenue is mcp:glossary_term:t1.", body)
	assert.Equal(t, "About "+ds, summary)

	refs := pageRefs(t, db)
	assert.Equal(t, map[string]string{"r1": "catalog " + ds, "r3": "catalog mcp:glossary_term:t1", "r4": "datahub urn:li:chart:c"}, refs,
		"r2 duplicated a citation the page already had, so it is removed")

	var index int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM catalog_datasets`).Scan(&index))
	assert.Zero(t, index)
	assert.Equal(t, 1, report.Rewrite.IndexRowsDropped)
	assert.Equal(t, 1, report.Rewrite.Rewritten["memory_records.content"])
	assert.Equal(t, 2, report.Rewrite.Rewritten["knowledge_page_entity_refs.entity_urn"])
	assert.Equal(t, 3, report.Rewrite.Unmapped, "the corpuser, the query and the chart")
	var where []string
	for _, s := range report.Skipped {
		where = append(where, s.Where)
	}
	assert.Contains(t, strings.Join(where, "|"), "call_records.promoted_urn")

	// A second rewrite finds nothing more to change.
	again := newReport()
	require.NoError(t, NewRewriter(db).Rewrite(ctx, resolve, again))
	assert.Empty(t, again.Rewrite.Rewritten)
}

func pageRefs(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `SELECT id, target_type, entity_urn FROM knowledge_page_entity_refs`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var id, typ, urn string
		require.NoError(t, rows.Scan(&id, &typ, &urn))
		out[id] = typ + " " + urn
	}
	return out
}

// The import writes onto the catalog the store keeps: a definition, a
// dataset's aspects and its lineage edge, recorded as the import's.
func TestImport_RealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	cat := catalogstore.New(db)
	for _, table := range []string{"orders", "users"} {
		_, err := cat.PutDataset(ctx, catalog.DatasetInput{
			Ref: catalog.DatasetRef("trino", "warehouse", "hive", "sales", table), Type: catalog.DatasetTable,
			Columns: []catalog.ColumnInput{{Name: "id"}, {Name: "amount"}},
		})
		require.NoError(t, err)
	}
	fx := fixture()
	src := fx.src
	report, err := New(cat, NewStore(db), Options{}).Run(ctx, src, NewRewriter(db))
	require.NoError(t, err)
	assert.Equal(t, 2, report.Datasets.Imported)

	orders := catalog.DatasetRef("trino", "warehouse", "hive", "sales", "orders")
	d, err := cat.GetDataset(ctx, orders)
	require.NoError(t, err)
	assert.Equal(t, "Orders placed online.", d.Aspects.Description)
	history, err := cat.History(ctx, orders, catalog.AspectDescription)
	require.NoError(t, err)
	assert.Equal(t, Principal, history[0].WrittenBy)
	hops, err := cat.Lineage(ctx, orders.String(), catalog.Upstream, 1)
	require.NoError(t, err)
	require.Len(t, hops, 1)
	assert.Equal(t, catalog.WriterImport, hops[0].WriterKind)
}
