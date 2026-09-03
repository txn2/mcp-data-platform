package datahubapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	dhCatalogBase       = "/api/v1/portal/datahub/primary/catalog"
	glossaryBase        = dhCatalogBase + "/glossary"
	testFinanceNodeURN  = "urn:li:glossaryNode:finance"
	testRevenueTermURN  = "urn:li:glossaryTerm:revenue"
	glossaryStatusTmpl  = "status = %d, want %d (%s)"
	glossaryDecodeError = "decoding response: %v"
)

// glossaryBackend returns a fake carrying a small tree: one root node with one
// child node and one child term, plus one root term.
func glossaryBackend() *fakeDataHub {
	f := newFakeDataHub()
	f.rootNodes = []semantic.GlossaryNode{
		{URN: testFinanceNodeURN, Name: "Finance", Description: "money", TermsCount: 1, NodesCount: 1},
	}
	f.rootTerms = []semantic.GlossaryTerm{
		{URN: "urn:li:glossaryTerm:orphan", Name: "Orphan"},
	}
	f.children = map[string]*semantic.GlossaryChildren{
		testFinanceNodeURN: {
			Nodes: []semantic.GlossaryNode{{URN: "urn:li:glossaryNode:revenue", Name: "Revenue", ParentNode: testFinanceNodeURN}},
			Terms: []semantic.GlossaryTerm{{URN: testRevenueTermURN, Name: "ARR"}},
			Start: 0,
			Count: 2,
			Total: 2,
		},
	}
	f.parents = map[string][]semantic.GlossaryNode{
		testRevenueTermURN: {
			{URN: "urn:li:glossaryNode:revenue", Name: "Revenue", ParentNode: testFinanceNodeURN},
			{URN: testFinanceNodeURN, Name: "Finance"},
		},
	}
	f.terms = map[string]*semantic.GlossaryTerm{
		testRevenueTermURN: {URN: testRevenueTermURN, Name: "ARR", Description: "Annual recurring revenue."},
	}
	return f
}

// TestGlossaryTerm reads a term by URN, the read that opens a term a knowledge
// page cites: nothing else can reach a term from the URN a citation carries.
func TestGlossaryTerm(t *testing.T) {
	h := newTestHandler(glossaryBackend(), false, readerResolver(), &fakeAuditLogger{})

	rec := serve(h, viewer, "GET", glossaryBase+"/term?urn="+testRevenueTermURN, "")
	if rec.Code != http.StatusOK {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	var got semantic.GlossaryTerm
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf(glossaryDecodeError, err)
	}
	if got.URN != testRevenueTermURN || got.Name != "ARR" || got.Description != "Annual recurring revenue." {
		t.Fatalf("term = %+v, want the term with its name and definition", got)
	}
}

// TestGlossaryTerm_Unknown proves a term the catalog does not hold is a 404, not
// a 502: the request was well-formed and the backend answered.
func TestGlossaryTerm_Unknown(t *testing.T) {
	h := newTestHandler(glossaryBackend(), false, readerResolver(), &fakeAuditLogger{})
	rec := serve(h, viewer, "GET", glossaryBase+"/term?urn=urn:li:glossaryTerm:missing", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// TestGlossaryTerm_UnknownThroughTheCache proves the same 404 when the miss
// arrives as the provider abstraction's sentinel alone, which is what
// CachedProvider replays for a URN the catalog has already reported it does not
// hold (#1610). Keying the status on the upstream client's sentinel only would
// answer 502 for every repeat of the same lookup.
func TestGlossaryTerm_UnknownThroughTheCache(t *testing.T) {
	backend := glossaryBackend()
	backend.readErr = fmt.Errorf("glossary term: %w", semantic.ErrNotFound)
	h := newTestHandler(backend, false, readerResolver(), &fakeAuditLogger{})
	rec := serve(h, viewer, "GET", glossaryBase+"/term?urn="+testRevenueTermURN, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// TestGlossaryTerm_RejectsOtherKinds proves the route takes only a term URN. A
// node has no by-URN read upstream, so accepting one would forward a request
// that can only fail as a misleading 502.
func TestGlossaryTerm_RejectsOtherKinds(t *testing.T) {
	h := newTestHandler(glossaryBackend(), false, readerResolver(), &fakeAuditLogger{})
	for _, urn := range []string{testFinanceNodeURN, "urn:li:tag:pii", "not-a-urn", ""} {
		rec := serve(h, viewer, "GET", glossaryBase+"/term?urn="+urn, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("urn %q: status = %d, want 400 (%s)", urn, rec.Code, rec.Body.String())
		}
	}
}

// TestGlossaryTerm_BackendFailure surfaces an upstream failure as a 502, which
// is what keeps "this term is gone" distinct from "the catalog did not answer".
func TestGlossaryTerm_BackendFailure(t *testing.T) {
	backend := glossaryBackend()
	backend.readErr = errors.New("datahub down")
	h := newTestHandler(backend, false, readerResolver(), &fakeAuditLogger{})
	rec := serve(h, viewer, "GET", glossaryBase+"/term?urn="+testRevenueTermURN, "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

// TestGlossaryRoots returns nodes and terms with their own totals, since DataHub
// pages the two independently.
func TestGlossaryRoots(t *testing.T) {
	h := newTestHandler(glossaryBackend(), false, readerResolver(), &fakeAuditLogger{})
	rec := serve(h, viewer, "GET", glossaryBase+"/roots", "")
	if rec.Code != http.StatusOK {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	var got glossaryRootsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf(glossaryDecodeError, err)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].URN != testFinanceNodeURN {
		t.Errorf("nodes = %+v", got.Nodes)
	}
	if got.Nodes[0].TermsCount != 1 || got.Nodes[0].NodesCount != 1 {
		t.Errorf("child tallies lost: %+v", got.Nodes[0])
	}
	if got.NodesTotal != 1 || got.TermsTotal != 1 {
		t.Errorf("totals = (%d, %d), want (1, 1)", got.NodesTotal, got.TermsTotal)
	}
	if len(got.Terms) != 1 || got.Terms[0].Name != "Orphan" {
		t.Errorf("terms = %+v", got.Terms)
	}
}

// TestGlossaryRoots_EmptyTree proves an empty glossary serializes as [] rather
// than null, so the UI can iterate the response without a nil guard.
func TestGlossaryRoots_EmptyTree(t *testing.T) {
	h := newTestHandler(newFakeDataHub(), false, readerResolver(), &fakeAuditLogger{})
	rec := serve(h, viewer, "GET", glossaryBase+"/roots", "")
	if rec.Code != http.StatusOK {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf(glossaryDecodeError, err)
	}
	for _, key := range []string{"nodes", "terms"} {
		if string(raw[key]) != "[]" {
			t.Errorf("%s = %s, want []", key, raw[key])
		}
	}
}

// TestGlossaryRoots_LegFailure proves the concurrent roots read fails the whole
// request when either leg fails on its own. A partial tree reported as complete
// would read as "the glossary has no terms" (or no nodes), which is worse than
// an error: the caller cannot tell it from an empty glossary.
func TestGlossaryRoots_LegFailure(t *testing.T) {
	tests := []struct {
		name string
		fail func(*fakeDataHub)
	}{
		{"nodes leg fails", func(f *fakeDataHub) { f.rootNodesErr = errors.New("datahub down") }},
		{"terms leg fails", func(f *fakeDataHub) { f.rootTermsErr = errors.New("datahub down") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := glossaryBackend()
			tt.fail(backend)
			h := newTestHandler(backend, false, readerResolver(), &fakeAuditLogger{})
			rec := serve(h, viewer, "GET", glossaryBase+"/roots", "")
			if rec.Code != http.StatusBadGateway {
				t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusBadGateway, rec.Body.String())
			}
		})
	}
}

// TestGlossaryChildren returns the mixed page and passes the paging through.
func TestGlossaryChildren(t *testing.T) {
	backend := glossaryBackend()
	h := newTestHandler(backend, false, readerResolver(), &fakeAuditLogger{})
	rec := serve(h, viewer, "GET", glossaryBase+"/children?urn="+testFinanceNodeURN+"&offset=5&limit=30", "")
	if rec.Code != http.StatusOK {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	var got semantic.GlossaryChildren
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf(glossaryDecodeError, err)
	}
	if len(got.Nodes) != 1 || len(got.Terms) != 1 {
		t.Errorf("children = %+v", got)
	}
	if got.Total != 2 {
		t.Errorf("total = %d, want 2", got.Total)
	}
	if backend.childrenPage != [2]int{5, 30} {
		t.Errorf("paging reached backend as %v, want [5 30]", backend.childrenPage)
	}
}

// TestGlossaryChildren_EmptyLeaf proves a childless node serializes its slices
// as [] rather than null, and that serving the response leaves the reader's own
// page untouched — a caching reader would otherwise see its page rewritten.
func TestGlossaryChildren_EmptyLeaf(t *testing.T) {
	const leaf = "urn:li:glossaryNode:leaf"
	backend := glossaryBackend()
	page := &semantic.GlossaryChildren{}
	backend.children[leaf] = page
	h := newTestHandler(backend, false, readerResolver(), &fakeAuditLogger{})

	rec := serve(h, viewer, "GET", glossaryBase+"/children?urn="+leaf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf(glossaryDecodeError, err)
	}
	for _, key := range []string{"nodes", "terms"} {
		if string(raw[key]) != "[]" {
			t.Errorf("%s = %s, want []", key, raw[key])
		}
	}
	if page.Nodes != nil || page.Terms != nil {
		t.Errorf("handler mutated the reader's page: %+v", page)
	}
}

// TestGlossaryChildren_NilPage covers the defensive branch for a reader that
// reports neither a page nor an error: the handler answers with an empty page
// rather than dereferencing nil.
func TestGlossaryChildren_NilPage(t *testing.T) {
	const leaf = "urn:li:glossaryNode:nilpage"
	backend := glossaryBackend()
	backend.children[leaf] = nil
	h := newTestHandler(backend, false, readerResolver(), &fakeAuditLogger{})

	rec := serve(h, viewer, "GET", glossaryBase+"/children?urn="+leaf, "")
	if rec.Code != http.StatusOK {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	var got semantic.GlossaryChildren
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf(glossaryDecodeError, err)
	}
	if len(got.Nodes) != 0 || len(got.Terms) != 0 || got.Total != 0 {
		t.Errorf("nil page = %+v, want an empty page", got)
	}
}

// TestGlossaryChildren_UnknownNode maps the backend's not-found to a 404: the
// request was well-formed and DataHub answered, the node simply is not there.
func TestGlossaryChildren_UnknownNode(t *testing.T) {
	h := newTestHandler(glossaryBackend(), false, readerResolver(), &fakeAuditLogger{})
	rec := serve(h, viewer, "GET", glossaryBase+"/children?urn=urn:li:glossaryNode:ghost", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

// TestGlossaryChildren_BackendFailure keeps a genuine upstream failure a 502, so
// it is not confused with the not-found case.
func TestGlossaryChildren_BackendFailure(t *testing.T) {
	backend := glossaryBackend()
	backend.readErr = errors.New("datahub down")
	h := newTestHandler(backend, false, readerResolver(), &fakeAuditLogger{})
	rec := serve(h, viewer, "GET", glossaryBase+"/children?urn="+testFinanceNodeURN, "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

// TestGlossaryURNValidation rejects a missing or wrong-typed URN with a 400
// rather than forwarding it to DataHub and returning a misleading 502.
func TestGlossaryURNValidation(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"children without urn", "/children"},
		{"children with a term urn", "/children?urn=" + testRevenueTermURN},
		{"children with a dataset urn", "/children?urn=" + dhTestURN},
		{"parents without urn", "/parents"},
		{"parents with a dataset urn", "/parents?urn=" + dhTestURN},
		{"parents with a tag urn", "/parents?urn=urn:li:tag:PII"},
	}
	h := newTestHandler(glossaryBackend(), false, readerResolver(), &fakeAuditLogger{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(h, viewer, "GET", glossaryBase+tt.path, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusBadRequest, rec.Body.String())
			}
		})
	}
}

// TestGlossaryParents returns the chain direct-parent first for a term and for a
// node, and an empty array for an entity at the root.
func TestGlossaryParents(t *testing.T) {
	h := newTestHandler(glossaryBackend(), false, readerResolver(), &fakeAuditLogger{})

	rec := serve(h, viewer, "GET", glossaryBase+"/parents?urn="+testRevenueTermURN, "")
	if rec.Code != http.StatusOK {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		Parents []semantic.GlossaryNode `json:"parents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf(glossaryDecodeError, err)
	}
	if len(got.Parents) != 2 || got.Parents[0].Name != "Revenue" || got.Parents[1].Name != "Finance" {
		t.Fatalf("chain = %+v, want direct parent first", got.Parents)
	}

	// A node URN is accepted on the same endpoint, and a root entity has no
	// parents: the response must still be [] rather than null.
	rec = serve(h, viewer, "GET", glossaryBase+"/parents?urn="+testFinanceNodeURN, "")
	if rec.Code != http.StatusOK {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf(glossaryDecodeError, err)
	}
	if string(raw["parents"]) != "[]" {
		t.Errorf("parents = %s, want []", raw["parents"])
	}
}

// TestGlossaryParents_BackendFailure surfaces an upstream failure as a 502.
func TestGlossaryParents_BackendFailure(t *testing.T) {
	backend := glossaryBackend()
	backend.readErr = errors.New("datahub down")
	h := newTestHandler(backend, false, readerResolver(), &fakeAuditLogger{})
	rec := serve(h, viewer, "GET", glossaryBase+"/parents?urn="+testRevenueTermURN, "")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusBadGateway, rec.Body.String())
	}
}

// TestGlossaryReadRequiresDataHubAccess confirms the hierarchy reads sit behind
// the same persona gate as the rest of the catalog surface.
func TestGlossaryReadRequiresDataHubAccess(t *testing.T) {
	h := newTestHandler(glossaryBackend(), false, noAccessResolver(), &fakeAuditLogger{})
	for _, path := range []string{
		"/roots",
		"/children?urn=" + testFinanceNodeURN,
		"/parents?urn=" + testRevenueTermURN,
		"/term?urn=" + testRevenueTermURN,
	} {
		rec := serve(h, viewer, "GET", glossaryBase+path, "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403 (%s)", path, rec.Code, rec.Body.String())
		}
	}
}

// glossaryCreateKinds is the create surface under test: a node (#1155) and a
// term (#1158). Both run through one handler, so every case below runs against
// both rather than being written twice — a copy would also drift.
var glossaryCreateKinds = []struct {
	name       string
	path       string
	entityType string
	urnPrefix  string
	// created reads back what the writer received for this kind.
	created func(*fakeDataHub) glossaryEntityRequest
	// call is the writer method the kind must reach.
	call string
}{
	{
		name:       "node",
		path:       "/nodes",
		entityType: "glossaryNode",
		urnPrefix:  "urn:li:glossaryNode:",
		created:    func(f *fakeDataHub) glossaryEntityRequest { return f.createdNode },
		call:       "CreateGlossaryNode",
	},
	{
		name:       "term",
		path:       "/terms",
		entityType: "glossaryTerm",
		urnPrefix:  "urn:li:glossaryTerm:",
		created:    func(f *fakeDataHub) glossaryEntityRequest { return f.createdTerm },
		call:       "CreateGlossaryTerm",
	},
}

// TestCreateGlossaryEntity creates each kind under a parent, returns its URN,
// reaches the writer method for that kind, and records the create in the audit
// log with the kind's own entity type.
func TestCreateGlossaryEntity(t *testing.T) {
	for _, kind := range glossaryCreateKinds {
		t.Run(kind.name, func(t *testing.T) {
			backend := glossaryBackend()
			log := &fakeAuditLogger{}
			h := newTestHandler(backend, true, writerResolver(), log)

			body := `{"name":"Revenue","definition":"top line","parent_node":"` + testFinanceNodeURN + `"}`
			rec := serve(h, viewer, "POST", glossaryBase+kind.path, body)
			if rec.Code != http.StatusCreated {
				t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusCreated, rec.Body.String())
			}
			var got map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf(glossaryDecodeError, err)
			}
			if got["urn"] != kind.urnPrefix+"Revenue" {
				t.Errorf("urn = %q, want %q", got["urn"], kind.urnPrefix+"Revenue")
			}
			want := glossaryEntityRequest{Name: "Revenue", Definition: "top line", ParentNode: testFinanceNodeURN}
			if kind.created(backend) != want {
				t.Errorf("writer received %+v, want %+v", kind.created(backend), want)
			}
			// A term created through the node route (or the reverse) would still
			// answer 201 with a plausible URN, so the writer call is asserted.
			if len(backend.calls) != 1 || backend.calls[0] != kind.call {
				t.Errorf("writer calls = %v, want [%s]", backend.calls, kind.call)
			}
			if len(log.events) != 1 {
				t.Fatalf("audit events = %d, want 1", len(log.events))
			}
			ev := log.events[0]
			if ev.ToolName != datahubCreateTool || !ev.Success {
				t.Errorf("audit event = %+v", ev)
			}
			if ev.Parameters["name"] != "Revenue" || ev.Parameters["parent_node"] != testFinanceNodeURN {
				t.Errorf("audit parameters = %+v", ev.Parameters)
			}
			if ev.Parameters["entity_type"] != kind.entityType {
				t.Errorf("audit entity_type = %v, want %q", ev.Parameters["entity_type"], kind.entityType)
			}
		})
	}
}

// TestCreateGlossaryEntity_Root creates at the root when parent_node is omitted.
func TestCreateGlossaryEntity_Root(t *testing.T) {
	for _, kind := range glossaryCreateKinds {
		t.Run(kind.name, func(t *testing.T) {
			backend := glossaryBackend()
			h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
			rec := serve(h, viewer, "POST", glossaryBase+kind.path, `{"name":"Corporate"}`)
			if rec.Code != http.StatusCreated {
				t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusCreated, rec.Body.String())
			}
			if kind.created(backend).ParentNode != "" {
				t.Errorf("parent = %q, want empty at the root", kind.created(backend).ParentNode)
			}
		})
	}
}

// TestCreateGlossaryEntity_Invalid rejects a request the backend would refuse,
// and proves nothing reached the writer.
func TestCreateGlossaryEntity_Invalid(t *testing.T) {
	bodies := []struct {
		name string
		body string
	}{
		{"no name", `{"definition":"x"}`},
		{"blank name", `{"name":"   "}`},
		{"malformed parent", `{"name":"Revenue","parent_node":"finance"}`},
		{"parent is a term", `{"name":"Revenue","parent_node":"` + testRevenueTermURN + `"}`},
		{"malformed body", `{"name":`},
	}
	for _, kind := range glossaryCreateKinds {
		for _, tt := range bodies {
			t.Run(kind.name+"/"+tt.name, func(t *testing.T) {
				backend := glossaryBackend()
				h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
				rec := serve(h, viewer, "POST", glossaryBase+kind.path, tt.body)
				if rec.Code != http.StatusBadRequest {
					t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusBadRequest, rec.Body.String())
				}
				if len(backend.calls) != 0 {
					t.Fatalf("writer called on an invalid request: %v", backend.calls)
				}
			})
		}
	}
}

// TestCreateGlossaryEntity_WriteGate covers the two refusals a create can hit: a
// persona without the datahub_create grant, and a read-only connection.
func TestCreateGlossaryEntity_WriteGate(t *testing.T) {
	const body = `{"name":"Revenue"}`
	for _, kind := range glossaryCreateKinds {
		t.Run(kind.name, func(t *testing.T) {
			backend := glossaryBackend()
			h := newTestHandler(backend, true, readerResolver(), &fakeAuditLogger{})
			if rec := serve(h, viewer, "POST", glossaryBase+kind.path, body); rec.Code != http.StatusForbidden {
				t.Errorf("without the grant: status = %d, want 403 (%s)", rec.Code, rec.Body.String())
			}

			backend = glossaryBackend()
			h = newTestHandler(backend, false, writerResolver(), &fakeAuditLogger{})
			if rec := serve(h, viewer, "POST", glossaryBase+kind.path, body); rec.Code != http.StatusForbidden {
				t.Errorf("read-only connection: status = %d, want 403 (%s)", rec.Code, rec.Body.String())
			}
			if len(backend.calls) != 0 {
				t.Errorf("writer called on a denied request: %v", backend.calls)
			}
		})
	}
}

// TestCreateGlossaryEntity_BackendFailure surfaces the failure as a 502 and
// still records the failed attempt in the audit log.
func TestCreateGlossaryEntity_BackendFailure(t *testing.T) {
	for _, kind := range glossaryCreateKinds {
		t.Run(kind.name, func(t *testing.T) {
			backend := glossaryBackend()
			backend.writeErr = errors.New("permission denied")
			log := &fakeAuditLogger{}
			h := newTestHandler(backend, true, writerResolver(), log)

			rec := serve(h, viewer, "POST", glossaryBase+kind.path, `{"name":"Revenue"}`)
			if rec.Code != http.StatusBadGateway {
				t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusBadGateway, rec.Body.String())
			}
			if len(log.events) != 1 || log.events[0].Success {
				t.Fatalf("audit must record the failed create: %+v", log.events)
			}
		})
	}
}

// --- delete (#1158) ---

// TestDeleteGlossaryEntity retires either kind through the one route and audits
// the delete with the entity type read back from the URN.
func TestDeleteGlossaryEntity(t *testing.T) {
	for _, tt := range []struct {
		name       string
		urn        string
		entityType string
	}{
		{"term", testRevenueTermURN, "glossaryTerm"},
		{"node", testFinanceNodeURN, "glossaryNode"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			backend := glossaryBackend()
			log := &fakeAuditLogger{}
			h := newTestHandler(backend, true, writerResolver(), log)

			rec := serve(h, viewer, "DELETE", glossaryBase+"/entity?urn="+tt.urn, "")
			if rec.Code != http.StatusOK {
				t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
			}
			if backend.deletedGlossry != tt.urn {
				t.Errorf("deleted %q, want %q", backend.deletedGlossry, tt.urn)
			}
			if len(log.events) != 1 {
				t.Fatalf("audit events = %d, want 1", len(log.events))
			}
			ev := log.events[0]
			if ev.ToolName != datahubDeleteTool || !ev.Success {
				t.Errorf("audit event = %+v", ev)
			}
			if ev.Parameters["entity_type"] != tt.entityType || ev.Parameters["urn"] != tt.urn {
				t.Errorf("audit parameters = %+v", ev.Parameters)
			}
		})
	}
}

// TestDeleteGlossaryEntity_Invalid rejects a missing or wrong-kinded URN with a
// 400 rather than forwarding it to DataHub as a misleading 502.
func TestDeleteGlossaryEntity_Invalid(t *testing.T) {
	for _, tt := range []struct {
		name string
		urn  string
	}{
		{"no urn", ""},
		{"a tag urn", "urn:li:tag:PII"},
		{"a dataset urn", dhTestURN},
		{"an empty glossary id", "urn:li:glossaryTerm:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			backend := glossaryBackend()
			h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
			rec := serve(h, viewer, "DELETE", glossaryBase+"/entity?urn="+tt.urn, "")
			if rec.Code != http.StatusBadRequest {
				t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if len(backend.calls) != 0 {
				t.Fatalf("writer called on an invalid delete: %v", backend.calls)
			}
		})
	}
}

// TestDeleteGlossaryEntity_Gates covers the delete-grant refusal, the read-only
// connection, and an upstream failure that must still be audited.
func TestDeleteGlossaryEntity_Gates(t *testing.T) {
	path := glossaryBase + "/entity?urn=" + testRevenueTermURN

	// A persona with create/update but not delete must be refused: the delete
	// gate is its own grant, not "any write".
	backend := glossaryBackend()
	h := newTestHandler(backend, true, updaterResolver(), &fakeAuditLogger{})
	if rec := serve(h, viewer, "DELETE", path, ""); rec.Code != http.StatusForbidden {
		t.Errorf("without the delete grant: status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}

	backend = glossaryBackend()
	h = newTestHandler(backend, false, writerResolver(), &fakeAuditLogger{})
	if rec := serve(h, viewer, "DELETE", path, ""); rec.Code != http.StatusForbidden {
		t.Errorf("read-only connection: status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if len(backend.calls) != 0 {
		t.Errorf("writer called on a denied delete: %v", backend.calls)
	}

	backend = glossaryBackend()
	backend.writeErr = errors.New("permission denied")
	log := &fakeAuditLogger{}
	h = newTestHandler(backend, true, writerResolver(), log)
	if rec := serve(h, viewer, "DELETE", path, ""); rec.Code != http.StatusBadGateway {
		t.Errorf("upstream failure: status = %d, want 502 (%s)", rec.Code, rec.Body.String())
	}
	if len(log.events) != 1 || log.events[0].Success {
		t.Fatalf("audit must record the failed delete: %+v", log.events)
	}
}

// --- term usage filters (#1158) ---

// TestGlossaryTermSearchFilters proves the term-usage parameters reach the
// reader as the DataHub filter fields the usage lists depend on. The two are
// distinct: glossaryTerms matches a table carrying the term on the table OR on
// one of its columns, fieldGlossaryTerms only the column-level assignments, so
// swapping them would silently report every carrier as a column carrier.
func TestGlossaryTermSearchFilters(t *testing.T) {
	for _, tt := range []struct {
		name  string
		query string
		want  []semantic.FieldFilter
	}{
		{
			name:  "term usage",
			query: "&" + qpGlossaryTerm + "=" + testRevenueTermURN,
			want:  []semantic.FieldFilter{{Field: filterFieldGlossaryTerms, Values: []string{testRevenueTermURN}}},
		},
		{
			name:  "column usage",
			query: "&" + qpColumnGlossaryTerm + "=" + testRevenueTermURN,
			want:  []semantic.FieldFilter{{Field: filterFieldColumnGlossaryTerms, Values: []string{testRevenueTermURN}}},
		},
		{
			name:  "blank value is not a filter",
			query: "&" + qpGlossaryTerm + "=%20",
			want:  nil,
		},
		{
			name:  "absent",
			query: "",
			want:  nil,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			backend := glossaryBackend()
			h := newTestHandler(backend, false, readerResolver(), &fakeAuditLogger{})
			rec := serve(h, viewer, "GET", dhCatalogBase+"/search?q=*"+tt.query, "")
			if rec.Code != http.StatusOK {
				t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
			}
			if !reflect.DeepEqual(backend.searchFilter.Filters, tt.want) {
				t.Errorf("filters = %+v, want %+v", backend.searchFilter.Filters, tt.want)
			}
		})
	}
}

// --- entity documents (#1158) ---

// TestEntityDocuments returns the documents attached to a glossary term, and an
// empty array (not null) for an entity with none.
func TestEntityDocuments(t *testing.T) {
	backend := glossaryBackend()
	backend.relatedDocs = map[string][]semantic.DocumentResult{
		testRevenueTermURN: {{URN: "urn:li:document:1", Title: "How revenue is recognized"}},
	}
	h := newTestHandler(backend, false, readerResolver(), &fakeAuditLogger{})

	rec := serve(h, viewer, "GET", dhCatalogBase+"/entity/documents?urn="+testRevenueTermURN, "")
	if rec.Code != http.StatusOK {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusOK, rec.Body.String())
	}
	var got struct {
		Documents []semantic.DocumentResult `json:"documents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf(glossaryDecodeError, err)
	}
	if len(got.Documents) != 1 || got.Documents[0].Title != "How revenue is recognized" {
		t.Fatalf("documents = %+v", got.Documents)
	}

	rec = serve(h, viewer, "GET", dhCatalogBase+"/entity/documents?urn="+testFinanceNodeURN, "")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf(glossaryDecodeError, err)
	}
	if string(raw["documents"]) != "[]" {
		t.Errorf("documents = %s, want []", raw["documents"])
	}
}

// TestEntityDocuments_Errors covers the three refusals the read can hit: no URN,
// no DataHub access on the persona, and an upstream failure.
func TestEntityDocuments_Errors(t *testing.T) {
	backend := glossaryBackend()
	h := newTestHandler(backend, false, readerResolver(), &fakeAuditLogger{})
	if rec := serve(h, viewer, "GET", dhCatalogBase+"/entity/documents", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("no urn: status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}

	h = newTestHandler(backend, false, noAccessResolver(), &fakeAuditLogger{})
	rec := serve(h, viewer, "GET", dhCatalogBase+"/entity/documents?urn="+testRevenueTermURN, "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("without datahub access: status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}

	backend = glossaryBackend()
	backend.readErr = errors.New("datahub down")
	h = newTestHandler(backend, false, readerResolver(), &fakeAuditLogger{})
	rec = serve(h, viewer, "GET", dhCatalogBase+"/entity/documents?urn="+testRevenueTermURN, "")
	if rec.Code != http.StatusBadGateway {
		t.Errorf("upstream failure: status = %d, want 502 (%s)", rec.Code, rec.Body.String())
	}
}
