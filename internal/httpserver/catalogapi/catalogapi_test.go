package catalogapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/catalogfake"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// testStore is the in-memory catalog with the reads the fake answers with
// nothing (search, browse, lineage, schema documents) recorded and answered
// from the test's own fixtures.
type testStore struct {
	*catalogfake.Store
	searches []catalog.DatasetQuery
	browses  []catalog.BrowseQuery
	summary  []catalog.DatasetSummary
	entries  []catalog.BrowseEntry
	hops     map[catalog.Direction][]catalog.Hop
	doc      *catalog.SchemaDocument
	failWith error
	// failList and failUses fail those reads with a store error.
	failList, failUses bool
}

func (s *testStore) ListDefinitions(ctx context.Context, q catalog.DefinitionQuery) ([]catalog.DefinitionSummary, error) {
	if s.failList {
		return nil, errors.New("listing failed")
	}
	out, err := s.Store.ListDefinitions(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("fake listing: %w", err)
	}
	return out, nil
}

func (s *testStore) Uses(ctx context.Context, ref catalog.Ref, limit int) ([]catalog.Use, error) {
	if s.failUses {
		return nil, errors.New("uses failed")
	}
	out, err := s.Store.Uses(ctx, ref, limit)
	if err != nil {
		return nil, fmt.Errorf("fake uses: %w", err)
	}
	return out, nil
}

func (s *testStore) SearchDatasets(_ context.Context, q catalog.DatasetQuery) ([]catalog.DatasetSummary, error) {
	s.searches = append(s.searches, q)
	return s.summary, s.failWith
}

func (s *testStore) Browse(_ context.Context, q catalog.BrowseQuery) ([]catalog.BrowseEntry, error) {
	s.browses = append(s.browses, q)
	return s.entries, s.failWith
}

func (s *testStore) Lineage(_ context.Context, _ string, d catalog.Direction, _ int) ([]catalog.Hop, error) {
	return s.hops[d], nil
}

func (s *testStore) GetDocument(_ context.Context, ref catalog.Ref) (*catalog.SchemaDocument, error) {
	if s.doc == nil || s.doc.Reference != ref.String() {
		return nil, catalog.ErrNotFound
	}
	return s.doc, nil
}

// recordingAudit keeps every event logged.
type recordingAudit struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recordingAudit) Log(_ context.Context, e audit.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (*recordingAudit) Query(context.Context, audit.QueryFilter) ([]audit.Event, error) {
	return nil, nil
}
func (*recordingAudit) Close() error { return nil }

// The callers a test sends as: an administrator, a curator (fetch and
// apply_knowledge, reaching only the connection "acme"), a reader (fetch
// only) and someone whose persona grants neither.
const (
	roleAdmin        = "admin"
	roleCurator      = "curator"
	roleReader       = "reader"
	roleNone         = "none"
	rolePortalEditor = "portal-editor"
	rolePortalNone   = "portal-none"
)

type fixture struct {
	store *testStore
	audit *recordingAudit
	mux   *http.ServeMux
	ds    catalog.Ref
	other catalog.Ref
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		store: &testStore{Store: catalogfake.New(), hops: map[catalog.Direction][]catalog.Hop{}},
		audit: &recordingAudit{},
		mux:   http.NewServeMux(),
	}
	f.ds = f.store.AddDataset("acme", "hive.sales.orders", "order_id", "amount")
	f.other = f.store.AddDataset("elsewhere", "hive.sales.secret", "id")
	reaches := func(ref string) bool { return !strings.Contains(ref, "elsewhere") }
	New(Deps{
		Store: f.store,
		Connections: func() []Connection {
			return []Connection{{Kind: "trino", Name: "acme"}, {Kind: "trino", Name: "elsewhere"}}
		},
		PersonaResolver: func(roles []string) *portal.PersonaInfo {
			switch roles[0] {
			case roleCurator:
				return &portal.PersonaInfo{Name: "curator", Tools: []string{"fetch", "apply_knowledge"}, ReachesCatalogRef: reaches}
			case roleReader:
				return &portal.PersonaInfo{Name: "reader", Tools: []string{"fetch"}, ReachesCatalogRef: reaches}
			case roleNone:
				return &portal.PersonaInfo{Name: "none", Tools: []string{"trino_query"}}
			case rolePortalEditor:
				// The portal level decides, not the tools (#2225).
				return &portal.PersonaInfo{
					Name: "editor", Tools: []string{"trino_query"}, ReachesCatalogRef: reaches,
					Portal: persona.PortalAccess{Catalog: persona.PortalWrite},
				}
			case rolePortalNone:
				return &portal.PersonaInfo{
					Name: "withheld", Tools: []string{"fetch", "apply_knowledge"},
					Portal: persona.PortalAccess{Catalog: persona.PortalNone},
				}
			default:
				return nil
			}
		},
		AdminRoles: []string{roleAdmin},
		Audit:      f.audit,
	}).Register(f.mux)
	return f
}

// do sends one request as role ("" sends none) and decodes a JSON body.
func (f *fixture) do(t *testing.T, role, method, path string, body any) (status int, out map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, reader)
	if role != "" {
		req = req.WithContext(portal.ContextWithUser(req.Context(), &portal.User{
			UserID: role + "-id", Email: role + "@example.com", Roles: []string{role},
		}))
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// headOf is the first element of a decoded JSON array as an object, empty when
// there is none or it is not one.
func headOf(list []any) map[string]any {
	if len(list) == 0 {
		return map[string]any{}
	}
	m, _ := list[0].(map[string]any)
	return m
}

// str is a decoded JSON value as a string, empty when it is not one.
func str(v any) string {
	s, _ := v.(string)
	return s
}

// isTrue reports whether a decoded JSON value is the boolean true.
func isTrue(v any) bool {
	b, _ := v.(bool)
	return b
}

func q(ref string) string { return url.QueryEscape(ref) }

func TestReadersAndWritersAreGatedByPersonaTools(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		role, method, path string
		body               any
		want               int
	}{
		{"", http.MethodGet, prefix + "/connections", nil, http.StatusUnauthorized},
		{roleNone, http.MethodGet, prefix + "/connections", nil, http.StatusForbidden},
		{"nobody", http.MethodGet, prefix + "/connections", nil, http.StatusForbidden},
		{roleReader, http.MethodGet, prefix + "/connections", nil, http.StatusOK},
		{roleReader, http.MethodPut, prefix + "/aspect", map[string]any{"reference": f.ds.String(), "aspect": "tags", "value": []string{"x"}}, http.StatusForbidden},
		{roleReader, http.MethodPost, prefix + "/definitions", map[string]any{"kind": "tag", "name": "x"}, http.StatusForbidden},
		{roleAdmin, http.MethodGet, prefix + "/connections", nil, http.StatusOK},
	} {
		if got, out := f.do(t, tc.role, tc.method, tc.path, tc.body); got != tc.want {
			t.Errorf("%s %s %s: %d %v, want %d", tc.role, tc.method, tc.path, got, out, tc.want)
		}
	}
}

func TestListConnectionsKeepsTheReachableOnes(t *testing.T) {
	f := newFixture(t)
	_, out := f.do(t, roleReader, http.MethodGet, prefix+"/connections", nil)
	conns, _ := out["connections"].([]any)
	if len(conns) != 1 || headOf(conns)["name"] != "acme" || isTrue(out["writable"]) {
		t.Fatalf("reader sees %v", out)
	}
	_, out = f.do(t, roleAdmin, http.MethodGet, prefix+"/connections", nil)
	if conns, _ := out["connections"].([]any); len(conns) != 2 || !isTrue(out["writable"]) {
		t.Fatalf("admin sees %v", out)
	}
}

func TestSearchDatasetsScopesToTheReachableConnections(t *testing.T) {
	f := newFixture(t)
	f.store.summary = []catalog.DatasetSummary{{Reference: f.ds.String(), Name: "orders"}}
	status, out := f.do(t, roleReader, http.MethodGet,
		prefix+"/datasets?q=ord&connection_kind=trino&connection=acme&tag=pii&tag=gold&limit=500&offset=-3", nil)
	if status != http.StatusOK {
		t.Fatalf("%d %v", status, out)
	}
	got := f.store.searches[0]
	if !got.Scoped || len(got.Within) != 1 || got.Within[0] != "trino/acme" || got.Text != "ord" ||
		got.Limit != maxLimit || got.Offset != 0 || len(got.Tags) != 2 || got.Connection != "acme" {
		t.Fatalf("reader's search: %+v", got)
	}
	if results, _ := out["results"].([]any); len(results) != 1 {
		t.Fatalf("results %v", out)
	}

	f.do(t, roleAdmin, http.MethodGet, prefix+"/datasets?limit=x", nil)
	if admin := f.store.searches[1]; admin.Scoped || admin.Limit != defaultLimit {
		t.Fatalf("admin's search: %+v", admin)
	}

	f.store.failWith = errors.New("boom")
	if status, _ := f.do(t, roleAdmin, http.MethodGet, prefix+"/datasets", nil); status != http.StatusInternalServerError {
		t.Fatalf("a store failure answered %d", status)
	}
}

func TestBrowse(t *testing.T) {
	f := newFixture(t)
	f.store.entries = []catalog.BrowseEntry{{Name: "sales", Reference: "mcp:dataset:(trino,acme,hive.sales)", Datasets: 2}}
	for _, tc := range []struct {
		role, query string
		want        int
	}{
		{roleReader, "connection_kind=trino&connection=acme&path=hive", http.StatusOK},
		{roleReader, "connection_kind=trino", http.StatusBadRequest},
		{roleReader, "connection_kind=trino&connection=acme&path=" + q(`a."b`), http.StatusBadRequest},
		{roleReader, "connection_kind=trino&connection=elsewhere", http.StatusNotFound},
	} {
		if status, out := f.do(t, tc.role, http.MethodGet, prefix+"/browse?"+tc.query, nil); status != tc.want {
			t.Errorf("browse %s: %d %v, want %d", tc.query, status, out, tc.want)
		}
	}
	if b := f.store.browses[0]; b.Connection != "acme" || len(b.Prefix) != 1 || b.Prefix[0] != "hive" {
		t.Fatalf("browse query %+v", b)
	}
}

func TestGetDatasetResolvesNamesAndFiltersLineage(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	term, err := f.store.PutDefinition(ctx, catalog.DefinitionInput{Kind: catalog.KindGlossaryTerm, Name: "Revenue"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.WriteAspect(ctx, catalog.ColumnRef(f.ds, "amount"), catalog.AspectGlossaryTerms,
		json.RawMessage(`["`+term.Reference+`","mcp:glossary_term:gone"]`), "x"); err != nil {
		t.Fatal(err)
	}
	f.store.hops[catalog.Upstream] = []catalog.Hop{
		{Edge: catalog.Edge{Source: "mcp:dataset:(trino,acme,raw.a)", Target: f.ds.String(), WrittenAt: time.Now()}, Depth: 1},
		{Edge: catalog.Edge{Source: f.other.String(), Target: f.ds.String(), WrittenAt: time.Now()}, Depth: 1},
		// Two hops out: a reachable source writing an unreachable target.
		{Edge: catalog.Edge{Source: "mcp:dataset:(trino,acme,raw.b)", Target: f.other.String(), WrittenAt: time.Now()}, Depth: 2},
	}

	status, out := f.do(t, roleCurator, http.MethodGet, prefix+"/dataset?ref="+q(f.ds.String()), nil)
	if status != http.StatusOK {
		t.Fatalf("%d %v", status, out)
	}
	names, _ := out["names"].(map[string]any)
	if names[term.Reference] != "Revenue" || names["mcp:glossary_term:gone"] != nil || !isTrue(out["writable"]) {
		t.Fatalf("names %v writable %v", names, out["writable"])
	}
	if up, _ := out["upstream"].([]any); len(up) != 1 {
		t.Fatalf("upstream %v, want the unreachable edge withheld", up)
	}

	for _, tc := range []struct{ ref string }{{f.other.String()}, {"mcp:tag:pii"}, {"mcp:dataset:(trino,acme,no.such.table)"}} {
		if status, _ := f.do(t, roleReader, http.MethodGet, prefix+"/dataset?ref="+q(tc.ref), nil); status != http.StatusNotFound {
			t.Errorf("dataset %s answered %d, want 404", tc.ref, status)
		}
	}
	if status, _ := f.do(t, roleReader, http.MethodGet, prefix+"/dataset?ref=urn:li:dataset:x", nil); status != http.StatusBadRequest {
		t.Errorf("a non-catalog reference answered %d, want 400", status)
	}
}

func TestGetSchemaDocument(t *testing.T) {
	f := newFixture(t)
	ref := catalog.SchemaDocumentRef("api", "acme", "openapi.json")
	f.store.doc = &catalog.SchemaDocument{Reference: ref.String(), Filename: "openapi.json", Content: []byte(`{"openapi":"3.0.3"}`)}
	status, out := f.do(t, roleReader, http.MethodGet, prefix+"/schema-document?ref="+q(ref.String()), nil)
	if status != http.StatusOK || out["content"] != `{"openapi":"3.0.3"}` {
		t.Fatalf("%d %v", status, out)
	}
	for _, r := range []string{f.ds.String(), catalog.SchemaDocumentRef("api", "acme", "other.json").String()} {
		if status, _ := f.do(t, roleReader, http.MethodGet, prefix+"/schema-document?ref="+q(r), nil); status != http.StatusNotFound {
			t.Errorf("%s answered %d", r, status)
		}
	}
}

func TestWriteAspectHistoryAndRollback(t *testing.T) {
	f := newFixture(t)
	write := func(value any) map[string]any {
		status, out := f.do(t, roleCurator, http.MethodPut, prefix+"/aspect",
			map[string]any{"reference": f.ds.String(), "aspect": "description", "value": value})
		if status != http.StatusOK {
			t.Fatalf("write %v: %d %v", value, status, out)
		}
		return out
	}
	first := write("First.")
	write("Second.")
	if _, err := f.store.WriteAspect(t.Context(), f.ds, catalog.AspectTags, json.RawMessage(`["pii"]`), "x"); err != nil {
		t.Fatal(err)
	}

	_, all := f.do(t, roleCurator, http.MethodGet, prefix+"/history?ref="+q(f.ds.String()), nil)
	if changes, _ := all["changes"].([]any); len(changes) != 3 {
		t.Fatalf("history of every aspect: %v", all)
	}
	_, one := f.do(t, roleCurator, http.MethodGet, prefix+"/history?ref="+q(f.ds.String())+"&aspect=description", nil)
	changes, _ := one["changes"].([]any)
	if len(changes) != 2 || headOf(changes)["value"] != "Second." {
		t.Fatalf("description history, newest first: %v", one)
	}

	// The first change was written over, so undoing it would discard the
	// second: refused, naming the newer write. The latest one rolls back.
	status, out := f.do(t, roleCurator, http.MethodPost, prefix+"/rollback",
		map[string]any{"reference": f.ds.String(), "aspect": "description", "change_id": first["id"]})
	if status != http.StatusConflict || !strings.Contains(fmt.Sprint(out["detail"]), "was written again") {
		t.Fatalf("rollback of a superseded change: %d %v", status, out)
	}
	latest := headOf(changes)
	status, out = f.do(t, roleCurator, http.MethodPost, prefix+"/rollback",
		map[string]any{"reference": f.ds.String(), "aspect": "description", "change_id": latest["id"]})
	if status != http.StatusOK || out["reverts"] != latest["id"] {
		t.Fatalf("rollback: %d %v", status, out)
	}
	if status, _ := f.do(t, roleCurator, http.MethodPost, prefix+"/rollback",
		map[string]any{"reference": f.ds.String(), "aspect": "tags", "change_id": first["id"]}); status != http.StatusNotFound {
		t.Fatalf("a change of another aspect rolled back with %d", status)
	}

	if len(f.audit.events) != 4 {
		t.Fatalf("audited %d writes, want 4", len(f.audit.events))
	}
	ev := f.audit.events[0]
	if ev.ToolName != auditTool || ev.Source != "portal" || ev.Persona != "curator" || ev.Parameters["action"] != "write_aspect" || !ev.Success {
		t.Fatalf("audit event %+v", ev)
	}
}

func TestWriteAspectRefusals(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct {
		name string
		body any
		want int
	}{
		{"bad reference", map[string]any{"reference": "urn:li:tag:x", "aspect": "tags", "value": []string{"x"}}, http.StatusBadRequest},
		{"unreachable", map[string]any{"reference": f.other.String(), "aspect": "tags", "value": []string{"x"}}, http.StatusNotFound},
		{"unknown field", map[string]any{"reference": f.ds.String(), "aspect": "tags", "value": []string{"x"}, "extra": 1}, http.StatusBadRequest},
		{"wrong shape", map[string]any{"reference": f.ds.String(), "aspect": "domain", "value": "mcp:tag:pii"}, http.StatusBadRequest},
	} {
		if status, out := f.do(t, roleCurator, http.MethodPut, prefix+"/aspect", tc.body); status != tc.want {
			t.Errorf("%s: %d %v, want %d", tc.name, status, out, tc.want)
		}
	}
	if status, _ := f.do(t, roleCurator, http.MethodPost, prefix+"/rollback",
		map[string]any{"reference": "nope", "aspect": "tags", "change_id": "c"}); status != http.StatusBadRequest {
		t.Errorf("rollback of a bad reference answered %d", status)
	}
	if status, _ := f.do(t, roleCurator, http.MethodPost, prefix+"/rollback",
		map[string]any{"reference": f.other.String(), "aspect": "tags", "change_id": "c"}); status != http.StatusNotFound {
		t.Errorf("rollback on an unreachable dataset answered %d", status)
	}
	if status, _ := f.do(t, roleCurator, http.MethodGet, prefix+"/history?ref="+q(f.other.String()), nil); status != http.StatusNotFound {
		t.Errorf("history of an unreachable dataset answered %d", status)
	}
}

func TestDefinitions(t *testing.T) {
	f := newFixture(t)
	status, node := f.do(t, roleCurator, http.MethodPost, prefix+"/definitions",
		map[string]any{"kind": "glossary_node", "name": "Finance"})
	if status != http.StatusCreated {
		t.Fatalf("create node: %d %v", status, node)
	}
	status, term := f.do(t, roleCurator, http.MethodPost, prefix+"/definitions",
		map[string]any{"kind": "glossary_term", "name": "Revenue", "parent": node["reference"], "description": " Money in. "})
	if status != http.StatusCreated {
		t.Fatalf("create term: %d %v", status, term)
	}
	if a, _ := term["aspects"].(map[string]any); a["description"] != "Money in." {
		t.Fatalf("term created without its description: %v", term)
	}
	if status, _ := f.do(t, roleCurator, http.MethodPost, prefix+"/definitions", map[string]any{"kind": "tag", "name": " "}); status != http.StatusBadRequest {
		t.Fatalf("a blank name answered %d", status)
	}

	_, roots := f.do(t, roleReader, http.MethodGet, prefix+"/definitions?roots=true&kind=glossary_node&kind=glossary_term", nil)
	if results, _ := roots["results"].([]any); len(results) != 1 || headOf(results)["name"] != "Finance" {
		t.Fatalf("roots: %v", roots)
	}

	// Two columns carry the term, one on a connection the reader does not reach.
	ref := str(term["reference"])
	for _, col := range []catalog.Ref{catalog.ColumnRef(f.ds, "amount"), catalog.ColumnRef(f.other, "id")} {
		if _, err := f.store.WriteAspect(t.Context(), col, catalog.AspectGlossaryTerms, json.RawMessage(`["`+ref+`"]`), "x"); err != nil {
			t.Fatal(err)
		}
	}
	_, view := f.do(t, roleReader, http.MethodGet, prefix+"/definition?ref="+q(ref), nil)
	if uses, _ := view["uses"].([]any); len(uses) != 1 || view["uses_withheld"] != float64(1) || isTrue(view["writable"]) {
		t.Fatalf("term view: %v", view)
	}
	_, nodeView := f.do(t, roleReader, http.MethodGet, prefix+"/definition?ref="+q(str(node["reference"])), nil)
	if children, _ := nodeView["children"].([]any); len(children) != 1 {
		t.Fatalf("node view: %v", nodeView)
	}

	if status, out := f.do(t, roleCurator, http.MethodDelete, prefix+"/definition?ref="+q(ref), nil); status != http.StatusConflict {
		t.Fatalf("deleting a term in use: %d %v", status, out)
	}
	if status, _ := f.do(t, roleCurator, http.MethodDelete, prefix+"/definition?ref="+q("mcp:tag:never"), nil); status != http.StatusNotFound {
		t.Fatalf("deleting a missing tag answered %d", status)
	}
	if status, _ := f.do(t, roleReader, http.MethodGet, prefix+"/definition?ref="+q("mcp:domain:none"), nil); status != http.StatusNotFound {
		t.Fatalf("a missing definition answered %d", status)
	}
	_, tag := f.do(t, roleCurator, http.MethodPost, prefix+"/definitions", map[string]any{"kind": "tag", "name": "spare"})
	if status, _ := f.do(t, roleCurator, http.MethodDelete, prefix+"/definition?ref="+q(str(tag["reference"])), nil); status != http.StatusNoContent {
		t.Fatalf("deleting an unused tag answered %d", status)
	}
}

func TestDatasetNamesItsDomain(t *testing.T) {
	f := newFixture(t)
	domain, err := f.store.PutDefinition(t.Context(), catalog.DefinitionInput{Kind: catalog.KindDomain, Name: "Sales"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.WriteAspect(t.Context(), f.ds, catalog.AspectDomain, json.RawMessage(`"`+domain.Reference+`"`), "x"); err != nil {
		t.Fatal(err)
	}
	_, out := f.do(t, roleReader, http.MethodGet, prefix+"/dataset?ref="+q(f.ds.String()), nil)
	if names, _ := out["names"].(map[string]any); names[domain.Reference] != "Sales" {
		t.Fatalf("names %v", out["names"])
	}
}

func TestDefinitionReadsReportStoreFailures(t *testing.T) {
	f := newFixture(t)
	_, node := f.do(t, roleCurator, http.MethodPost, prefix+"/definitions", map[string]any{"kind": "glossary_node", "name": "Finance"})
	_, tag := f.do(t, roleCurator, http.MethodPost, prefix+"/definitions", map[string]any{"kind": "tag", "name": "pii"})

	f.store.failList = true
	if status, _ := f.do(t, roleReader, http.MethodGet, prefix+"/definitions", nil); status != http.StatusInternalServerError {
		t.Errorf("a failed listing answered %d", status)
	}
	if status, _ := f.do(t, roleReader, http.MethodGet, prefix+"/definition?ref="+q(str(node["reference"])), nil); status != http.StatusInternalServerError {
		t.Errorf("a node whose entries failed to list answered %d", status)
	}
	f.store.failList, f.store.failUses = false, true
	if status, _ := f.do(t, roleReader, http.MethodGet, prefix+"/definition?ref="+q(str(tag["reference"])), nil); status != http.StatusInternalServerError {
		t.Errorf("a tag whose uses failed to read answered %d", status)
	}
	if status, _ := f.do(t, roleReader, http.MethodGet, prefix+"/definition?ref=nope", nil); status != http.StatusBadRequest {
		t.Errorf("a malformed reference answered %d", status)
	}
}

func TestWriteStoreErrorMapsEachError(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{catalog.ErrNotFound, http.StatusNotFound},
		{&catalog.InUseError{Reference: "mcp:tag:x", Entities: 2}, http.StatusConflict},
		{&catalog.ConflictError{Change: "c1", Reference: "mcp:tag:x", Aspect: catalog.AspectDescription}, http.StatusConflict},
		{catalog.ErrInvalidRef, http.StatusBadRequest},
		{catalog.ErrInvalidAspect, http.StatusBadRequest},
		{catalog.ErrAspectNotAllowed, http.StatusBadRequest},
		{errors.New("boom"), http.StatusInternalServerError},
	} {
		rec := httptest.NewRecorder()
		writeStoreError(rec, tc.err, "thing")
		if rec.Code != tc.want {
			t.Errorf("%v answered %d, want %d", tc.err, rec.Code, tc.want)
		}
	}
}

// An edit carries the value it was made from; one made from a read another
// write has since replaced is refused, so neither editor loses the other's
// change silently.
func TestWriteAspectRefusesAnEditMadeFromAStaleRead(t *testing.T) {
	f := newFixture(t)
	put := func(value, expect any, withExpect bool) (int, map[string]any) {
		body := map[string]any{"reference": f.ds.String(), "aspect": "tags", "value": value}
		if withExpect {
			body["expect"] = expect
		}
		return f.do(t, roleCurator, http.MethodPut, prefix+"/aspect", body)
	}
	if status, out := put([]string{"pii"}, nil, true); status != http.StatusOK {
		t.Fatalf("an edit from an absent aspect: %d %v", status, out)
	}
	if status, out := put([]string{"pii", "gold"}, []string{"pii"}, true); status != http.StatusOK {
		t.Fatalf("an edit from the current value: %d %v", status, out)
	}
	status, out := put([]string{"pii", "finance"}, []string{"pii"}, true)
	if status != http.StatusConflict || !strings.Contains(str(out["detail"]), "changed since you read it") {
		t.Fatalf("an edit from a stale read: %d %v", status, out)
	}
	if got := f.store.Aspects(f.ds).Tags; len(got) != 2 || got[1] != "gold" {
		t.Fatalf("the stale edit replaced the newer write: %v", got)
	}
	if status, _ := put([]string{"x"}, nil, false); status != http.StatusOK {
		t.Fatalf("an edit that names no read value is written as sent: %d", status)
	}
}

// The store keys a definition by kind and slug, and storing a second of the
// same slug updates the first; a create never does that to an entry someone
// else filed, so it is refused naming the one that exists.
func TestCreateDefinitionRefusesAnExistingName(t *testing.T) {
	f := newFixture(t)
	status, first := f.do(t, roleCurator, http.MethodPost, prefix+"/definitions",
		map[string]any{"kind": "glossary_term", "name": "Net Revenue", "description": "Kept."})
	if status != http.StatusCreated {
		t.Fatalf("first create: %d %v", status, first)
	}
	status, out := f.do(t, roleCurator, http.MethodPost, prefix+"/definitions",
		map[string]any{"kind": "glossary_term", "name": "net revenue", "description": "Replaced?"})
	if status != http.StatusConflict || !strings.Contains(str(out["detail"]), str(first["reference"])) {
		t.Fatalf("a create of an existing name: %d %v", status, out)
	}
	_, view := f.do(t, roleReader, http.MethodGet, prefix+"/definition?ref="+q(str(first["reference"])), nil)
	if a, _ := view["aspects"].(map[string]any); a["description"] != "Kept." {
		t.Fatalf("the existing term was changed: %v", view)
	}
	if status, _ := f.do(t, roleCurator, http.MethodPost, prefix+"/definitions",
		map[string]any{"kind": "tag", "name": "pii", "parent": "mcp:glossary_node:n"}); status != http.StatusBadRequest {
		t.Fatalf("a tag with a parent answered %d, want 400", status)
	}
}

func TestDefinitionSaysWhenMoreCarryItThanAreListed(t *testing.T) {
	f := newFixture(t)
	_, tag := f.do(t, roleCurator, http.MethodPost, prefix+"/definitions", map[string]any{"kind": "tag", "name": "wide"})
	for i := range usesLimit + 1 {
		ref := f.store.AddDataset("acme", fmt.Sprintf("hive.wide.t%03d", i))
		if _, err := f.store.WriteAspect(t.Context(), ref, catalog.AspectTags, json.RawMessage(`["wide"]`), "x"); err != nil {
			t.Fatal(err)
		}
	}
	_, view := f.do(t, roleAdmin, http.MethodGet, prefix+"/definition?ref="+q(str(tag["reference"])), nil)
	if uses, _ := view["uses"].([]any); len(uses) != usesLimit || !isTrue(view["more_uses"]) {
		t.Fatalf("%d uses listed, more_uses %v", len(uses), view["more_uses"])
	}
}

// TestReader_TheAdminRuleDecides: a wired admin rule, the platform's one
// (#2228), decides who reads the catalog unrestricted, not the admin roles.
func TestReader_TheAdminRuleDecides(t *testing.T) {
	h := New(Deps{AdminRoles: []string{roleAdmin}, IsAdmin: func(roles []string) bool { return len(roles) == 1 && roles[0] == "root" }})
	for _, tc := range []struct {
		roles []string
		want  bool
	}{
		{[]string{"root"}, true},
		{[]string{roleAdmin}, false},
	} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
		req = req.WithContext(portal.ContextWithUser(req.Context(), &portal.User{UserID: "u", Roles: tc.roles}))
		w := httptest.NewRecorder()
		_, ok := h.reader(w, req)
		if ok != tc.want {
			t.Errorf("roles %v: admitted %v, want %v (%d)", tc.roles, ok, tc.want, w.Code)
		}
	}
}

// The portal catalog level decides who reads and edits the catalog (#2225): a
// persona set to write edits without either tool, and one set to none reads
// nothing although its tools would have let it.
func TestCatalogFollowsThePortalCatalogLevel(t *testing.T) {
	f := newFixture(t)
	status, body := f.do(t, rolePortalEditor, http.MethodGet, prefix+"/connections", nil)
	if writable, _ := body["writable"].(bool); status != http.StatusOK || !writable {
		t.Fatalf("editor: %d %v, want 200 writable", status, body)
	}
	if status, _ := f.do(t, rolePortalNone, http.MethodGet, prefix+"/connections", nil); status != http.StatusForbidden {
		t.Fatalf("catalog none: %d, want 403", status)
	}
}

// The catalog's connection picker lists them in name order, whatever order
// the toolkits were registered in (#2389).
func TestReachableListsByName(t *testing.T) {
	h := &Handler{deps: Deps{Connections: func() []Connection {
		return []Connection{{Kind: "trino", Name: "conn-10"}, {Kind: "s3", Name: "Beta"}, {Kind: "trino", Name: "conn-2"}}
	}}}
	got := make([]string, 0, 3)
	for _, c := range h.reachable(&caller{admin: true}) {
		got = append(got, c.Name)
	}
	if want := []string{"Beta", "conn-2", "conn-10"}; !slices.Equal(got, want) {
		t.Fatalf("reachable = %v, want %v", got, want)
	}
}
