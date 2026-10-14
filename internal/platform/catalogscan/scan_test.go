package catalogscan

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

var errBoom = errors.New("boom")

type fakeWriter struct {
	mu       sync.Mutex
	put      []catalog.DatasetInput
	removed  map[string][]string
	unread   map[string][]string
	putErr   error
	markErr  error
	markRets int
}

func (w *fakeWriter) PutDataset(_ context.Context, in catalog.DatasetInput) (*catalog.Dataset, error) {
	if w.putErr != nil {
		return nil, w.putErr
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.put = append(w.put, in)
	return &catalog.Dataset{Reference: in.Ref.String()}, nil
}

func (w *fakeWriter) MarkRemoved(_ context.Context, r catalog.Removal) (int, error) {
	if w.markErr != nil {
		return 0, w.markErr
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.removed == nil {
		w.removed, w.unread = map[string][]string{}, map[string][]string{}
	}
	w.removed[r.ConnectionKind+"/"+r.Connection] = r.Keep
	w.unread[r.ConnectionKind+"/"+r.Connection] = r.Unread
	return w.markRets, nil
}

type fakeSettings struct {
	set Settings
	err error
}

func (f fakeSettings) Get(context.Context) (Settings, error) { return f.set, f.err }

type fakeRecorder struct {
	mu       sync.Mutex
	outcomes []Outcome
	err      error
}

func (f *fakeRecorder) RecordOutcome(_ context.Context, o Outcome) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes = append(f.outcomes, o)
	return f.err
}

type fakeTrino struct {
	conns    []Connection
	catalogs map[string][]string
	rows     map[string][]ColumnRow
	catErr   error
	colErr   map[string]error
}

func (f fakeTrino) Connections() []Connection { return f.conns }

func (f fakeTrino) Catalogs(_ context.Context, bound string) ([]string, error) {
	return f.catalogs[bound], f.catErr
}

func (f fakeTrino) Columns(_ context.Context, _, cat string, fn func(ColumnRow) error) error {
	if err := f.colErr[cat]; err != nil {
		return err
	}
	for _, r := range f.rows[cat] {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

func trinoFixture() fakeTrino {
	return fakeTrino{
		conns:    []Connection{{Instance: "warehouse", Bound: "Warehouse"}},
		catalogs: map[string][]string{"Warehouse": {"hive", "system", "pg"}},
		rows: map[string][]ColumnRow{
			"hive": {
				{Schema: "sales", Table: "orders", TableType: "BASE TABLE", Column: "id", DataType: "bigint"},
				{Schema: "sales", Table: "orders", TableType: "BASE TABLE", Column: "amount", DataType: "double", Nullable: true},
				{Schema: "sales", Table: "daily", TableType: "VIEW", Column: "day", DataType: "date", Nullable: true},
				{Schema: "tmp", Table: "scratch", TableType: "BASE TABLE", Column: "x", DataType: "varchar"},
			},
			"system": {{Schema: "runtime", Table: "nodes", Column: "id"}},
			"pg":     {{Schema: "public", Table: "users", TableType: "BASE TABLE", Column: "id", DataType: "integer"}},
		},
	}
}

func TestScanWritesWhatEachConnectionSees(t *testing.T) {
	w, rec := &fakeWriter{markRets: 2}, &fakeRecorder{}
	set := Defaults()
	set.Exclusions = []Exclusion{{ConnectionKind: KindTrino, Connection: "warehouse", Entries: []string{"hive.tmp", "pg"}}}
	s := New(w, fakeSettings{set: set}, rec, NewTrinoSource(trinoFixture()))

	out, err := s.Scan(context.Background())
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, 2, out[0].Datasets)
	assert.Equal(t, 2, out[0].Removed)
	assert.Empty(t, out[0].Error)
	require.Len(t, rec.outcomes, 1)

	require.Len(t, w.put, 2, "the system catalog, the excluded schema and the excluded catalog are skipped")
	orders := w.put[0]
	assert.Equal(t, "mcp:dataset:(trino,warehouse,hive.sales.orders)", orders.Ref.String(),
		"a dataset carries the instance name, not the bound one")
	assert.Equal(t, catalog.DatasetTable, orders.Type)
	assert.Equal(t, []catalog.ColumnInput{
		{Name: "id", DataType: "bigint"}, {Name: "amount", DataType: "double", Nullable: true},
	}, orders.Columns)
	assert.Equal(t, catalog.DatasetView, w.put[1].Type)
	assert.Equal(t, []string{orders.Ref.String(), w.put[1].Ref.String()}, w.removed["trino/warehouse"])
}

func TestAnUnreadableCatalogIsReportedAndKeptFromRemoval(t *testing.T) {
	src := trinoFixture()
	src.colErr = map[string]error{"pg": errBoom}
	w, rec := &fakeWriter{markRets: 1}, &fakeRecorder{}
	out, err := New(w, fakeSettings{set: Defaults()}, rec, NewTrinoSource(src)).Scan(context.Background())
	require.NoError(t, err)
	assert.Empty(t, out[0].Error, "one unreadable catalog does not fail the connection")
	assert.Equal(t, []Unread{{Path: "pg", Error: "reading columns: boom"}}, out[0].Unread)
	assert.Equal(t, 3, out[0].Datasets, "the catalogs that could be read are still written")
	assert.Equal(t, 1, out[0].Removed, "what was read is still reconciled")
	assert.Equal(t, []string{"pg"}, w.unread["trino/warehouse"], "nothing under the unread catalog is marked removed")
	assert.Equal(t, []Unread{{Path: "pg", Error: "reading columns: boom"}}, rec.outcomes[0].Unread)
}

func TestAnOutcomeListsNoUnreadPartsAsEmpty(t *testing.T) {
	w, rec := &fakeWriter{}, &fakeRecorder{}
	out, err := New(w, fakeSettings{set: Defaults()}, rec, NewTrinoSource(trinoFixture())).Scan(context.Background())
	require.NoError(t, err)
	require.NotNil(t, out[0].Unread)
	assert.Empty(t, out[0].Unread)
	assert.Equal(t, []string{}, w.unread["trino/warehouse"])
	raw, err := json.Marshal(out[0])
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"unread":[]`)
}

// hangingTrino is a reader whose "dead" connection does not answer until its
// release is closed.
type hangingTrino struct {
	fakeTrino
	release chan struct{}
}

func (h hangingTrino) Catalogs(ctx context.Context, bound string) ([]string, error) {
	if bound == "dead" {
		<-h.release
		return nil, errBoom
	}
	return h.fakeTrino.Catalogs(ctx, bound)
}

func TestAConnectionThatDoesNotAnswerHoldsUpNoOther(t *testing.T) {
	src := trinoFixture()
	src.conns = []Connection{{Instance: "dead", Bound: "dead"}, {Instance: "warehouse", Bound: "Warehouse"}}
	h := hangingTrino{fakeTrino: src, release: make(chan struct{})}
	rec := &fakeRecorder{}
	done := make(chan []Outcome)
	go func() {
		out, _ := New(&fakeWriter{}, fakeSettings{set: Defaults()}, rec, NewTrinoSource(h)).Scan(context.Background())
		done <- out
	}()

	require.Eventually(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return len(rec.outcomes) == 1 && rec.outcomes[0].Connection == "warehouse"
	}, 5*time.Second, time.Millisecond, "the answering connection is recorded while the other still waits")

	close(h.release)
	out := <-done
	require.Len(t, out, 2)
	assert.Equal(t, "dead", out[0].Connection, "outcomes keep connection order")
	assert.Contains(t, out[0].Error, "listing catalogs")
	assert.Equal(t, "warehouse", out[1].Connection)
	assert.Empty(t, out[1].Error)
}

func TestUnreadErrorNamesEachPart(t *testing.T) {
	err := &UnreadError{Parts: []Unread{{Path: "pg", Error: "down"}, {Path: "hive", Error: "denied"}}}
	assert.Equal(t, "could not read pg: down; hive: denied", err.Error())
}

func TestScanFailures(t *testing.T) {
	ctx := context.Background()
	_, err := New(&fakeWriter{}, fakeSettings{err: errBoom}, &fakeRecorder{}).Scan(ctx)
	require.ErrorIs(t, err, errBoom)

	src := trinoFixture()
	src.catErr = errBoom
	out, err := New(&fakeWriter{}, fakeSettings{set: Defaults()}, &fakeRecorder{}, NewTrinoSource(src)).Scan(ctx)
	require.NoError(t, err)
	assert.Contains(t, out[0].Error, "listing catalogs")

	out, err = New(&fakeWriter{putErr: errBoom}, fakeSettings{set: Defaults()}, &fakeRecorder{}, NewTrinoSource(trinoFixture())).Scan(ctx)
	require.NoError(t, err)
	assert.Contains(t, out[0].Error, "writing")

	out, err = New(&fakeWriter{markErr: errBoom}, fakeSettings{set: Defaults()}, &fakeRecorder{err: errBoom}, NewTrinoSource(trinoFixture())).Scan(ctx)
	require.NoError(t, err, "a recorder failure is logged, not returned")
	assert.Contains(t, out[0].Error, "boom")
}

func TestTrinoSourceStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	src := trinoFixture()
	src.colErr = map[string]error{"hive": context.Canceled}
	err := NewTrinoSource(src).Datasets(ctx, src.conns[0], nil, func(catalog.DatasetInput) error { return nil })
	assert.ErrorIs(t, err, context.Canceled)
}

type fakeOps struct {
	conns   []Connection
	ops     []Operation
	err     error
	docs    []Document
	docsErr error
}

func (f fakeOps) Connections() []Connection { return f.conns }
func (f fakeOps) Operations(context.Context, string) ([]Operation, error) {
	return f.ops, f.err
}

func (f fakeOps) Documents(context.Context, string) ([]Document, error) {
	return f.docs, f.docsErr
}

// fakeKeeper keeps documents in memory, failing the filename named in fail.
type fakeKeeper struct {
	kept []string
	fail string
}

func (k *fakeKeeper) PutDocument(_ context.Context, in catalog.DocumentInput) (*catalog.SchemaDocument, bool, error) {
	if in.Filename == k.fail {
		return nil, false, errBoom
	}
	k.kept = append(k.kept, in.ConnectionKind+"/"+in.Connection+"/"+in.Filename+" "+in.MIMEType)
	return &catalog.SchemaDocument{}, true, nil
}

func (*fakeKeeper) GetDocument(context.Context, catalog.Ref) (*catalog.SchemaDocument, error) {
	return nil, catalog.ErrNotFound
}

func (*fakeKeeper) ListDocuments(context.Context) ([]catalog.SchemaDocument, error) {
	return []catalog.SchemaDocument{}, nil
}

// TestOperationSourceKeepsDocuments: each operation's dataset names the schema
// document it was read from; a document that cannot be kept or read leaves its
// datasets cataloged without one.
func TestOperationSourceKeepsDocuments(t *testing.T) {
	conn := Connection{Instance: "billing", Bound: "billing"}
	reader := fakeOps{
		conns: []Connection{conn},
		ops: []Operation{
			{Path: []string{"listInvoices"}, Document: "invoices"},
			{Path: []string{"listCustomers"}, Document: "customers"},
			{Path: []string{"health"}},
		},
		docs: []Document{
			{Key: "invoices", Filename: "invoices.json", MIMEType: "application/json", Content: []byte("{}")},
			{Key: "customers", Filename: "customers.json", MIMEType: "application/json", Content: []byte("{}")},
		},
	}
	keeper := &fakeKeeper{fail: "customers.json"}
	var got []catalog.DatasetInput
	err := NewAPISource(reader).KeepDocuments(keeper).Datasets(context.Background(), conn, nil,
		func(in catalog.DatasetInput) error { got = append(got, in); return nil })
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "invoices.json", got[0].SchemaDocument)
	assert.Empty(t, got[1].SchemaDocument, "a document that could not be kept links nothing")
	assert.Empty(t, got[2].SchemaDocument)
	assert.Equal(t, []string{"api/billing/invoices.json application/json"}, keeper.kept)

	reader.docsErr = errBoom
	got = nil
	err = NewAPISource(reader).KeepDocuments(keeper).Datasets(context.Background(), conn, nil,
		func(in catalog.DatasetInput) error { got = append(got, in); return nil })
	require.NoError(t, err, "an unreadable document does not fail the scan")
	assert.Len(t, got, 3)
	assert.Empty(t, got[0].SchemaDocument)
}

func TestOperationSources(t *testing.T) {
	ops := fakeOps{
		conns: []Connection{{Instance: "gh", Bound: "gh"}},
		ops: []Operation{
			{Path: []string{"query", "repository"}, Columns: []catalog.ColumnInput{{Name: "owner", DataType: "String!"}}},
			{Path: []string{"mutation", "createIssue"}},
			{},
		},
	}
	w := &fakeWriter{}
	set := Defaults()
	set.Exclusions = []Exclusion{{ConnectionKind: KindGraphQL, Connection: "gh", Entries: []string{"mutation"}}}
	out, err := New(w, fakeSettings{set: set}, &fakeRecorder{}, NewGraphQLSource(ops)).Scan(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, out[0].Datasets)
	assert.Equal(t, "mcp:dataset:(graphql,gh,query.repository)", w.put[0].Ref.String())
	assert.Equal(t, catalog.DatasetGraphQLOperation, w.put[0].Type)

	api := NewAPISource(fakeOps{conns: ops.conns, err: errBoom})
	assert.Equal(t, KindAPI, api.Kind())
	assert.Equal(t, []catalog.DatasetType{catalog.DatasetAPIOperation}, api.Types())
	err = api.Datasets(context.Background(), ops.conns[0], nil, func(catalog.DatasetInput) error { return nil })
	assert.ErrorIs(t, err, errBoom)

	err = NewAPISource(fakeOps{ops: ops.ops[:1]}).Datasets(context.Background(), ops.conns[0], nil,
		func(catalog.DatasetInput) error { return errBoom })
	assert.ErrorIs(t, err, errBoom)
}

func TestExclusions(t *testing.T) {
	x := ParseExclusions([]string{" hive.tmp ", "", "pg"})
	assert.True(t, x.Excludes("hive", "tmp", "t"))
	assert.True(t, x.Excludes("pg"))
	assert.False(t, x.Excludes("hive", "sales", "t"))
	assert.False(t, x.Excludes("hive"), "a schema exclusion does not exclude its catalog")
	assert.False(t, Exclusions(nil).Excludes("anything"))
}

func TestTableType(t *testing.T) {
	assert.Equal(t, catalog.DatasetView, tableType("VIEW"))
	assert.Equal(t, catalog.DatasetTable, tableType("BASE TABLE"))
	assert.Equal(t, []catalog.DatasetType{catalog.DatasetTable, catalog.DatasetView}, NewTrinoSource(fakeTrino{}).Types())
}
