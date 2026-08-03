package datahubapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	glossaryBase        = "/api/v1/portal/datahub/primary/catalog/glossary"
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
	return f
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
	} {
		rec := serve(h, viewer, "GET", glossaryBase+path, "")
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403 (%s)", path, rec.Code, rec.Body.String())
		}
	}
}

// TestCreateGlossaryNode creates a node under a parent, returns its URN, and
// records the create in the audit log.
func TestCreateGlossaryNode(t *testing.T) {
	backend := glossaryBackend()
	log := &fakeAuditLogger{}
	h := newTestHandler(backend, true, writerResolver(), log)

	body := `{"name":"Revenue","definition":"top line","parent_node":"` + testFinanceNodeURN + `"}`
	rec := serve(h, viewer, "POST", glossaryBase+"/nodes", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusCreated, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf(glossaryDecodeError, err)
	}
	if got["urn"] != "urn:li:glossaryNode:Revenue" {
		t.Errorf("urn = %q", got["urn"])
	}
	want := glossaryNodeRequest{Name: "Revenue", Definition: "top line", ParentNode: testFinanceNodeURN}
	if backend.createdNode != want {
		t.Errorf("writer received %+v, want %+v", backend.createdNode, want)
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
}

// TestCreateGlossaryNode_Root creates at the root when parent_node is omitted.
func TestCreateGlossaryNode_Root(t *testing.T) {
	backend := glossaryBackend()
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
	rec := serve(h, viewer, "POST", glossaryBase+"/nodes", `{"name":"Corporate"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusCreated, rec.Body.String())
	}
	if backend.createdNode.ParentNode != "" {
		t.Errorf("parent = %q, want empty for a root node", backend.createdNode.ParentNode)
	}
}

// TestCreateGlossaryNode_Invalid rejects a request the backend would refuse, and
// proves nothing reached the writer.
func TestCreateGlossaryNode_Invalid(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"no name", `{"definition":"x"}`},
		{"blank name", `{"name":"   "}`},
		{"malformed parent", `{"name":"Revenue","parent_node":"finance"}`},
		{"parent is a term", `{"name":"Revenue","parent_node":"` + testRevenueTermURN + `"}`},
		{"malformed body", `{"name":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := glossaryBackend()
			h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
			rec := serve(h, viewer, "POST", glossaryBase+"/nodes", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if len(backend.calls) != 0 {
				t.Fatalf("writer called on an invalid request: %v", backend.calls)
			}
		})
	}
}

// TestCreateGlossaryNode_WriteGate covers the two refusals a create can hit: a
// persona without the datahub_create grant, and a read-only connection.
func TestCreateGlossaryNode_WriteGate(t *testing.T) {
	body := `{"name":"Revenue"}`

	backend := glossaryBackend()
	h := newTestHandler(backend, true, readerResolver(), &fakeAuditLogger{})
	if rec := serve(h, viewer, "POST", glossaryBase+"/nodes", body); rec.Code != http.StatusForbidden {
		t.Errorf("without the grant: status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}

	backend = glossaryBackend()
	h = newTestHandler(backend, false, writerResolver(), &fakeAuditLogger{})
	if rec := serve(h, viewer, "POST", glossaryBase+"/nodes", body); rec.Code != http.StatusForbidden {
		t.Errorf("read-only connection: status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if len(backend.calls) != 0 {
		t.Errorf("writer called on a denied request: %v", backend.calls)
	}
}

// TestCreateGlossaryNode_BackendFailure surfaces the failure as a 502 and still
// records the failed attempt in the audit log.
func TestCreateGlossaryNode_BackendFailure(t *testing.T) {
	backend := glossaryBackend()
	backend.writeErr = errors.New("permission denied")
	log := &fakeAuditLogger{}
	h := newTestHandler(backend, true, writerResolver(), log)

	rec := serve(h, viewer, "POST", glossaryBase+"/nodes", `{"name":"Revenue"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf(glossaryStatusTmpl, rec.Code, http.StatusBadGateway, rec.Body.String())
	}
	if len(log.events) != 1 || log.events[0].Success {
		t.Fatalf("audit must record the failed create: %+v", log.events)
	}
}
