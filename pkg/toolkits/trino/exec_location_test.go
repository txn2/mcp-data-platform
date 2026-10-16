package trino

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCoordinator answers each statement with the one column and rows the
// test assigns to the first statement prefix it matches, and records every
// statement it was sent.
type fakeCoordinator struct {
	mu      sync.Mutex
	sent    []string
	answers map[string][][]any
	// fails names the statement prefixes the coordinator refuses.
	fails []string
	// columns names the answer's columns per statement prefix; one column
	// named c when none is given.
	columns map[string][]string
}

func (f *fakeCoordinator) start(t *testing.T) *Toolkit {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	var (
		n      int
		rows   = map[string][][]any{}
		cols   = map[string][]string{}
		failed = map[string]bool{}
	)
	mux.HandleFunc("POST /v1/statement", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stmt := string(body)
		f.mu.Lock()
		n++
		id := "q" + strconv.Itoa(n)
		f.sent = append(f.sent, stmt)
		rows[id] = [][]any{}
		for prefix, answer := range f.answers {
			if strings.HasPrefix(stmt, prefix) {
				rows[id] = answer
			}
		}
		cols[id] = []string{"c"}
		for prefix, names := range f.columns {
			if strings.HasPrefix(stmt, prefix) {
				cols[id] = names
			}
		}
		for _, prefix := range f.fails {
			failed[id] = failed[id] || strings.HasPrefix(stmt, prefix)
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"infoUri":"%s/ui/%s","nextUri":"%s/v1/statement/%s/1","stats":{"state":"QUEUED"}}`,
			id, srv.URL, id, srv.URL, id)
	})
	mux.HandleFunc("GET /v1/statement/{id}/1", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		f.mu.Lock()
		data, _ := json.Marshal(rows[id])
		refused := failed[id]
		colJSON := make([]string, 0, len(cols[id]))
		for _, c := range cols[id] {
			colJSON = append(colJSON, fmt.Sprintf(`{"name":%q,"type":"varchar","typeSignature":{"rawType":"varchar","arguments":[]}}`, c))
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if refused {
			_, _ = fmt.Fprintf(w, `{"id":%q,"infoUri":"%s/ui/%s","stats":{"state":"FAILED"},"error":{"message":"Access Denied: Cannot show create table","errorCode":4,"errorName":"PERMISSION_DENIED","errorType":"USER_ERROR"}}`,
				id, srv.URL, id)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"infoUri":"%s/ui/%s","columns":[%s],"data":%s,"stats":{"state":"FINISHED"}}`,
			id, srv.URL, id, strings.Join(colJSON, ","), data)
	})

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	off := false
	tk, err := NewMulti(MultiConfig{
		DefaultConnection: "scratch",
		Instances: map[string]Config{
			"scratch": {Host: "127.0.0.1", Port: port, User: "u", SSL: &off},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })
	return tk
}

const showCreatePrefix = `SHOW CREATE TABLE "scratch"."uploads"."admin_x"`

// TestTableLocation_ReadsTheDefinition pins the lookup a registration makes of
// a table it has no record of (#2131), against a coordinator that answers: the
// existence check first, then SHOW CREATE TABLE with the quoted identifiers,
// and the external_location read out of what it returns.
func TestTableLocation_ReadsTheDefinition(t *testing.T) {
	f := &fakeCoordinator{answers: map[string][][]any{
		"SELECT 1 AS present": {{"1"}},
		showCreatePrefix: {{"CREATE TABLE scratch.uploads.admin_x (\n   a varchar\n)\nWITH (\n" +
			"   external_location = 's3://resources/resources/user/u1/r1/v/rev1/',\n   format = 'CSV'\n)"}},
	}}
	tk := f.start(t)

	location, found, err := tk.TableLocation(context.Background(), "scratch", "scratch", "uploads", "admin_x")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "s3://resources/resources/user/u1/r1/v/rev1/", location)
	require.Len(t, f.sent, 2)
	assert.Equal(t, showCreatePrefix, f.sent[1])
}

// TestTableLocation_AbsentTableIsNotFound: a table the catalog does not hold
// is answered from the existence check alone, with no definition read.
func TestTableLocation_AbsentTableIsNotFound(t *testing.T) {
	f := &fakeCoordinator{}
	tk := f.start(t)

	location, found, err := tk.TableLocation(context.Background(), "scratch", "scratch", "uploads", "admin_x")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, location)
	assert.Len(t, f.sent, 1)
}

// TestTableLocation_NoDefinitionRowIsFoundWithNoLocation: a table that exists
// and whose definition came back empty is reported found, with no location to
// compare, which no caller reads as its own.
func TestTableLocation_NoDefinitionRowIsFoundWithNoLocation(t *testing.T) {
	f := &fakeCoordinator{answers: map[string][][]any{"SELECT 1 AS present": {{"1"}}}}
	tk := f.start(t)

	location, found, err := tk.TableLocation(context.Background(), "scratch", "scratch", "uploads", "admin_x")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Empty(t, location)
}

// TestTableLocation_UnconfiguredConnectionRefuses: the existence check's
// failure is the answer.
func TestTableLocation_UnconfiguredConnectionRefuses(t *testing.T) {
	tk := &Toolkit{name: "x"}
	_, _, err := tk.TableLocation(context.Background(), "x", "c", "s", "t")
	require.Error(t, err)
}

func TestExternalLocationOf(t *testing.T) {
	for _, tc := range []struct{ name, ddl, want string }{
		{"plain", "WITH (external_location = 's3://b/d/', format = 'CSV')", "s3://b/d/"},
		{"doubled quote", "WITH (external_location = 's3://b/it''s/')", "s3://b/it's/"},
		{"none declared", "WITH (format = 'ORC')", ""},
		{"unterminated", "WITH (external_location = 's3://b/d/", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, externalLocationOf(tc.ddl))
		})
	}
}

// TestListTables reads a schema's tables and views from information_schema
// (#2136), with the schema quoted, and a view marked as one.
func TestListTables(t *testing.T) {
	f := &fakeCoordinator{answers: map[string][][]any{
		"SELECT table_name, table_type": {{"admin_daily", "BASE TABLE"}, {"webhook_esp", "VIEW"}, {"", "BASE TABLE"}},
	}, columns: listColumns}
	tk := f.start(t)
	got, truncated, err := tk.ListTables(context.Background(), "scratch", "scratch", "up'loads")
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Equal(t, []SchemaTable{{Name: "admin_daily"}, {Name: "webhook_esp", View: true}}, got)
	assert.Contains(t, f.sent[0], `FROM "scratch".information_schema.tables WHERE table_schema = 'up''loads'`)
}

// TestListTables_CutAtTheBound: a schema with more tables than the bound is
// returned cut, and says so.
func TestListTables_CutAtTheBound(t *testing.T) {
	rows := make([][]any, 0, maxSchemaTables+1)
	for i := range maxSchemaTables + 1 {
		rows = append(rows, []any{fmt.Sprintf("t%06d", i), "BASE TABLE"})
	}
	f := &fakeCoordinator{answers: map[string][][]any{"SELECT table_name, table_type": rows}, columns: listColumns}
	tk := f.start(t)
	got, truncated, err := tk.ListTables(context.Background(), "scratch", "scratch", "uploads")
	require.NoError(t, err)
	assert.True(t, truncated)
	assert.Len(t, got, maxSchemaTables)
}

// TestListTables_Failures: no client, and a statement the coordinator refuses.
func TestListTables_Failures(t *testing.T) {
	_, _, err := (&Toolkit{name: "x"}).ListTables(context.Background(), "x", "c", "s")
	require.Error(t, err)

	f := &fakeCoordinator{}
	tk := f.start(t)
	tk.manager = nil
	_, _, err = tk.ListTables(context.Background(), "scratch", "c", "s")
	require.Error(t, err)
}

// TestTableLocation_ARefusedDefinitionReadIsAnError: a table that exists and
// whose definition the coordinator will not show is found, with the error.
func TestTableLocation_ARefusedDefinitionReadIsAnError(t *testing.T) {
	f := &fakeCoordinator{answers: map[string][][]any{"SELECT 1 AS present": {{"1"}}}, fails: []string{"SHOW CREATE TABLE"}}
	tk := f.start(t)
	_, found, err := tk.TableLocation(context.Background(), "scratch", "scratch", "uploads", "admin_x")
	require.Error(t, err)
	assert.True(t, found)
	assert.Contains(t, err.Error(), "reading the definition of scratch.uploads.admin_x")
}

// TestListTables_ARefusedListingIsAnError: a listing the coordinator refuses
// names the schema.
func TestListTables_ARefusedListingIsAnError(t *testing.T) {
	f := &fakeCoordinator{fails: []string{"SELECT table_name"}}
	tk := f.start(t)
	_, _, err := tk.ListTables(context.Background(), "scratch", "scratch", "uploads")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing the tables of scratch.uploads")
}

// listColumns is what information_schema.tables answers ListTables with.
var listColumns = map[string][]string{"SELECT table_name, table_type": {"table_name", "table_type"}}
