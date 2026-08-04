package datahubapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

var (
	viewer = &portal.User{UserID: "viewer-1", Email: "viewer@example.com", Roles: []string{"analyst"}}
	admin  = &portal.User{UserID: "admin-1", Email: "admin@example.com", Roles: []string{"admin"}}
)

const dhTestURN = "urn:li:dataset:(urn:li:dataPlatform:trino,catalog.schema.orders,PROD)"

// fakeDataHub implements both Reader and Writer over shared in-memory state, so a
// write is observable on a subsequent read (proving edits reflect on reads).
type fakeDataHub struct {
	mu           sync.Mutex
	descriptions map[string]string
	tags         map[string][]string
	docs         map[string]*semantic.DocumentResult
	nextID       int
	tables       []semantic.TableSearchResult
	refs         []semantic.EntityRef
	upsertErr    error
	deleteErr    error
	resolveErr   error
	writeErr     error
	readErr      error
	columnsErr   error
	calls        []string

	// Glossary hierarchy (#1155). rootNodesErr and rootTermsErr fail one leg of
	// the concurrent roots read each, so either leg's failure can be tested on
	// its own rather than only the both-fail case.
	rootNodes    []semantic.GlossaryNode
	rootTerms    []semantic.GlossaryTerm
	rootNodesErr error
	rootTermsErr error
	children     map[string]*semantic.GlossaryChildren
	parents      map[string][]semantic.GlossaryNode
	childrenPage [2]int
	createdNode  glossaryNodeRequest

	// Tag governance (#1156).
	createdTag vocabularyRequest
	deletedTag string

	// Domain governance (#1157). setDomain/unsetDomain record the entity a
	// membership edit targeted, so a membership write is checked for what it
	// moved rather than only that a call happened.
	createdDomain vocabularyRequest
	deletedDomain string
	setDomain     [2]string
	unsetDomain   string
}

func newFakeDataHub() *fakeDataHub {
	return &fakeDataHub{
		descriptions: map[string]string{},
		tags:         map[string][]string{},
		docs:         map[string]*semantic.DocumentResult{},
	}
}

func (f *fakeDataHub) ResolveURN(_ context.Context, urn string) (*semantic.TableIdentifier, error) {
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	return &semantic.TableIdentifier{Table: urn}, nil
}

func (f *fakeDataHub) GetTableContext(_ context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return nil, f.readErr
	}
	tags := f.tags[table.Table]
	refs := make([]semantic.EntityRef, len(tags))
	for i, t := range tags {
		refs[i] = semantic.EntityRef{URN: t, Name: t}
	}
	return &semantic.TableContext{
		URN:         table.Table,
		Description: f.descriptions[table.Table],
		Tags:        tags,
		TagRefs:     refs,
	}, nil
}

func (f *fakeDataHub) GetColumnsContext(_ context.Context, _ semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	if f.columnsErr != nil {
		return nil, f.columnsErr
	}
	return map[string]*semantic.ColumnContext{}, nil
}

func (f *fakeDataHub) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return f.tables, f.readErr
}

func (f *fakeDataHub) SearchTags(_ context.Context, _ string, _ int) ([]semantic.EntityRef, error) {
	return f.refs, f.readErr
}

func (f *fakeDataHub) SearchGlossaryTerms(_ context.Context, _ string, _ int) ([]semantic.EntityRef, error) {
	return f.refs, f.readErr
}

func (f *fakeDataHub) ListDomains(_ context.Context) ([]semantic.EntityRef, error) {
	return f.refs, f.readErr
}

// --- glossary hierarchy (#1155) ---
//
// The fake mirrors the upstream client's contract: an unknown glossary node is
// ErrNotFound (DataHub answers the lookup with an empty stub, which the client
// converts), not an empty child set, so the handler's 404 path is exercised
// against the behavior the real backend has.

func (f *fakeDataHub) ListRootGlossaryNodes(_ context.Context, _, _ int) ([]semantic.GlossaryNode, int, error) {
	if f.rootNodesErr != nil {
		return nil, 0, f.rootNodesErr
	}
	if f.readErr != nil {
		return nil, 0, f.readErr
	}
	return f.rootNodes, len(f.rootNodes), nil
}

func (f *fakeDataHub) ListRootGlossaryTerms(_ context.Context, _, _ int) ([]semantic.GlossaryTerm, int, error) {
	if f.rootTermsErr != nil {
		return nil, 0, f.rootTermsErr
	}
	if f.readErr != nil {
		return nil, 0, f.readErr
	}
	return f.rootTerms, len(f.rootTerms), nil
}

func (f *fakeDataHub) ListGlossaryNodeChildren(_ context.Context, nodeURN string, offset, limit int) (*semantic.GlossaryChildren, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	children, ok := f.children[nodeURN]
	if !ok {
		return nil, fmt.Errorf("glossary node %s: %w", nodeURN, dhclient.ErrNotFound)
	}
	f.mu.Lock()
	f.childrenPage = [2]int{offset, limit}
	f.mu.Unlock()
	return children, nil
}

func (f *fakeDataHub) GetGlossaryParentChain(_ context.Context, urn string) ([]semantic.GlossaryNode, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.parents[urn], nil
}

func (f *fakeDataHub) CreateGlossaryNode(_ context.Context, name, definition, parentNode string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "CreateGlossaryNode")
	if f.writeErr != nil {
		return "", f.writeErr
	}
	f.createdNode = glossaryNodeRequest{Name: name, Definition: definition, ParentNode: parentNode}
	return "urn:li:glossaryNode:" + name, nil
}

// --- tags (#1156) ---

func (f *fakeDataHub) CreateTag(_ context.Context, name, description string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "CreateTag")
	if f.writeErr != nil {
		return "", f.writeErr
	}
	f.createdTag = vocabularyRequest{Name: name, Description: description}
	return "urn:li:tag:" + name, nil
}

func (f *fakeDataHub) DeleteTag(_ context.Context, tagURN string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "DeleteTag")
	if f.writeErr != nil {
		return f.writeErr
	}
	f.deletedTag = tagURN
	return nil
}

func (f *fakeDataHub) SearchDocuments(_ context.Context, _ string, _ int) ([]semantic.DocumentResult, error) {
	return f.docList(), f.readErr
}

func (f *fakeDataHub) BrowseDocuments(_ context.Context, _, _ int) ([]semantic.DocumentResult, int, error) {
	if f.readErr != nil {
		return nil, 0, f.readErr
	}
	docs := f.docList()
	return docs, len(docs), nil
}

func (f *fakeDataHub) GetDocument(_ context.Context, urn string) (*semantic.DocumentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.docs[strings.TrimPrefix(urn, documentURNPrefix)], nil
}

func (f *fakeDataHub) docList() []semantic.DocumentResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]semantic.DocumentResult, 0, len(f.docs))
	for _, d := range f.docs {
		out = append(out, *d)
	}
	return out
}

func (f *fakeDataHub) UpdateDescription(_ context.Context, urn, description string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "UpdateDescription")
	if f.writeErr != nil {
		return f.writeErr
	}
	f.descriptions[urn] = description
	return nil
}

func (f *fakeDataHub) ApplyTagChanges(_ context.Context, urn string, add, remove []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "ApplyTagChanges")
	if f.writeErr != nil {
		return f.writeErr
	}
	removeSet := map[string]bool{}
	for _, t := range remove {
		removeSet[t] = true
	}
	kept := make([]string, 0, len(f.tags[urn]))
	for _, t := range f.tags[urn] {
		if !removeSet[t] {
			kept = append(kept, t)
		}
	}
	f.tags[urn] = append(kept, add...)
	return nil
}

func (f *fakeDataHub) ApplyGlossaryTermChanges(_ context.Context, _ string, _, _ []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "ApplyGlossaryTermChanges")
	return f.writeErr
}

func (f *fakeDataHub) ApplyOwnerChanges(_ context.Context, _ string, _ []OwnerChange, _ []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "ApplyOwnerChanges")
	return f.writeErr
}

func (f *fakeDataHub) SetDomain(_ context.Context, entityURN, domainURN string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "SetDomain")
	if f.writeErr != nil {
		return f.writeErr
	}
	f.setDomain = [2]string{entityURN, domainURN}
	return nil
}

func (f *fakeDataHub) UnsetDomain(_ context.Context, entityURN string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "UnsetDomain")
	if f.writeErr != nil {
		return f.writeErr
	}
	f.unsetDomain = entityURN
	return nil
}

// --- domains (#1157) ---

func (f *fakeDataHub) CreateDomain(_ context.Context, name, description string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "CreateDomain")
	if f.writeErr != nil {
		return "", f.writeErr
	}
	f.createdDomain = vocabularyRequest{Name: name, Description: description}
	return "urn:li:domain:" + name, nil
}

func (f *fakeDataHub) DeleteDomain(_ context.Context, domainURN string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "DeleteDomain")
	if f.writeErr != nil {
		return f.writeErr
	}
	f.deletedDomain = domainURN
	return nil
}

func (f *fakeDataHub) UpsertContextDocument(_ context.Context, in DocumentInput) (*semantic.DocumentResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "UpsertContextDocument")
	if f.upsertErr != nil {
		return nil, f.upsertErr
	}
	id := in.ID
	if id == "" {
		f.nextID++
		id = fmt.Sprintf("doc-%d", f.nextID)
	}
	doc := &semantic.DocumentResult{URN: documentURNPrefix + id, Title: in.Title, Body: in.Content, SubType: in.Category}
	f.docs[id] = doc
	return doc, nil
}

func (f *fakeDataHub) DeleteContextDocument(_ context.Context, documentID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "DeleteContextDocument")
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.docs, documentID)
	return nil
}

// fakeAuditLogger captures logged events.
type fakeAuditLogger struct {
	mu     sync.Mutex
	events []audit.Event
}

func (l *fakeAuditLogger) Log(_ context.Context, e audit.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
	return nil
}

func (*fakeAuditLogger) Query(context.Context, audit.QueryFilter) ([]audit.Event, error) {
	return nil, nil
}
func (*fakeAuditLogger) Close() error { return nil }
func (l *fakeAuditLogger) last() *audit.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.events) == 0 {
		return nil
	}
	return &l.events[len(l.events)-1]
}

func writerResolver() portal.PersonaResolver {
	return func([]string) *portal.PersonaInfo {
		return &portal.PersonaInfo{Name: "curator", Tools: []string{datahubCreateTool, datahubUpdateTool, datahubDeleteTool}}
	}
}

func readerResolver() portal.PersonaResolver {
	return func([]string) *portal.PersonaInfo {
		return &portal.PersonaInfo{Name: "analyst", Tools: []string{"datahub_browse"}}
	}
}

func noAccessResolver() portal.PersonaResolver {
	return func([]string) *portal.PersonaInfo {
		return &portal.PersonaInfo{Name: "outsider", Tools: []string{"s3_list_objects"}}
	}
}

func newTestHandler(backend *fakeDataHub, writable bool, resolver portal.PersonaResolver, log audit.Logger) *Handler {
	bridge := NewStaticBridge()
	if writable {
		bridge.Add("primary", backend, backend)
	} else {
		bridge.Add("primary", backend, nil)
	}
	return NewHandler(Deps{Bridge: bridge, PersonaResolver: resolver, AdminRoles: []string{"admin"}, Audit: log})
}

func serve(h *Handler, user *portal.User, method, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.Register(mux)
	ctx := context.Background()
	if user != nil {
		ctx = portal.ContextWithUser(ctx, user)
	}
	var reader io.Reader = http.NoBody
	if body != "" {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequestWithContext(ctx, method, path, reader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, r)
	return rec
}

// --- criterion 1: write without the tool grant -> 403 ---

func TestWriteWithoutToolGrant_Forbidden(t *testing.T) {
	backend := newFakeDataHub()
	h := newTestHandler(backend, true, readerResolver(), &fakeAuditLogger{})
	body := fmt.Sprintf(`{"urn":%q,"description":"new"}`, dhTestURN)
	rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/description", body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if len(backend.calls) != 0 {
		t.Fatalf("writer must not be called on a denied request, got %v", backend.calls)
	}
}

func TestWriteReadOnlyConnection_Forbidden(t *testing.T) {
	backend := newFakeDataHub()
	h := newTestHandler(backend, false, writerResolver(), &fakeAuditLogger{})
	body := fmt.Sprintf(`{"urn":%q,"description":"new"}`, dhTestURN)
	rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/description", body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
}

func TestUnknownConnection_NotFound(t *testing.T) {
	backend := newFakeDataHub()
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
	rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/missing/catalog/browse", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}

// --- criterion 2: context doc on unsupported entity -> 4xx ---

func TestContextDocUnsupportedEntity_BadRequest(t *testing.T) {
	backend := newFakeDataHub()
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
	body := `{"entity_urn":"urn:li:dashboard:(looker,42)","title":"note","content":"x"}`
	rec := serve(h, viewer, "POST", "/api/v1/portal/datahub/primary/documents", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (%s)", rec.Code, rec.Body.String())
	}
	if len(backend.calls) != 0 {
		t.Fatalf("writer must not be called for an unsupported entity, got %v", backend.calls)
	}
}

// --- criterion 3: context-doc lifecycle + audit ---

func TestContextDocLifecycle(t *testing.T) {
	backend := newFakeDataHub()
	log := &fakeAuditLogger{}
	h := newTestHandler(backend, true, writerResolver(), log)

	create := `{"entity_urn":"` + dhTestURN + `","title":"Orders note","content":"body","category":"note"}`
	rec := serve(h, viewer, "POST", "/api/v1/portal/datahub/primary/documents", create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d (%s)", rec.Code, rec.Body.String())
	}
	var created semantic.DocumentResult
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Title != "Orders note" {
		t.Fatalf("created title = %q", created.Title)
	}
	if ev := log.last(); ev == nil || ev.ToolName != datahubCreateTool || ev.Connection != "primary" || ev.UserID != viewer.UserID {
		t.Fatalf("create audit event incomplete: %+v", ev)
	}

	if rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/documents/doc-1", ""); rec.Code != http.StatusOK {
		t.Fatalf("get status = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/documents/doc-1", `{"title":"v2","content":"b2"}`); rec.Code != http.StatusOK {
		t.Fatalf("update status = %d (%s)", rec.Code, rec.Body.String())
	}
	if got := backend.docs["doc-1"]; got == nil || got.Title != "v2" {
		t.Fatalf("update did not persist: %+v", got)
	}
	if rec := serve(h, viewer, "DELETE", "/api/v1/portal/datahub/primary/documents/doc-1", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d (%s)", rec.Code, rec.Body.String())
	}
	if _, ok := backend.docs["doc-1"]; ok {
		t.Fatalf("document still present after delete")
	}
	if ev := log.last(); ev == nil || ev.ToolName != datahubDeleteTool {
		t.Fatalf("delete audit event missing: %+v", ev)
	}
}

// --- criterion 4: edit reflected on read ---

func TestDescriptionEditReflectedOnRead(t *testing.T) {
	backend := newFakeDataHub()
	log := &fakeAuditLogger{}
	h := newTestHandler(backend, true, writerResolver(), log)
	body := fmt.Sprintf(`{"urn":%q,"description":"authoritative"}`, dhTestURN)
	if rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/description", body); rec.Code != http.StatusOK {
		t.Fatalf("edit status = %d (%s)", rec.Code, rec.Body.String())
	}
	if ev := log.last(); ev == nil || ev.ToolName != datahubUpdateTool || !ev.Success {
		t.Fatalf("update audit incomplete: %+v", ev)
	}
	rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/catalog/entity?urn="+dhTestURN, "")
	var resp catalogEntityResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Context == nil || resp.Context.Description != "authoritative" {
		t.Fatalf("edit not reflected: %+v", resp.Context)
	}
}

func TestTagEditReflectedOnRead(t *testing.T) {
	backend := newFakeDataHub()
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
	body := fmt.Sprintf(`{"urn":%q,"add":["urn:li:tag:PII"]}`, dhTestURN)
	if rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/tags", body); rec.Code != http.StatusOK {
		t.Fatalf("tag edit status = %d (%s)", rec.Code, rec.Body.String())
	}
	rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/catalog/entity?urn="+dhTestURN, "")
	var resp catalogEntityResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Context == nil || len(resp.Context.Tags) != 1 || resp.Context.Tags[0] != "urn:li:tag:PII" {
		t.Fatalf("tag edit not reflected: %+v", resp.Context)
	}
	// tag_refs carries the URN so the editor can remove/dedupe by URN (#785 review).
	if len(resp.Context.TagRefs) != 1 || resp.Context.TagRefs[0].URN != "urn:li:tag:PII" {
		t.Fatalf("tag_refs not populated: %+v", resp.Context.TagRefs)
	}
}

// --- reads + auth ---

func TestReadUnauthenticated_401(t *testing.T) {
	h := newTestHandler(newFakeDataHub(), true, writerResolver(), &fakeAuditLogger{})
	rec := serve(h, nil, "GET", "/api/v1/portal/datahub/primary/catalog/browse", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestReadWithoutDataHubAccess_403(t *testing.T) {
	h := newTestHandler(newFakeDataHub(), true, noAccessResolver(), &fakeAuditLogger{})
	paths := []string{
		"/api/v1/portal/datahub/primary/catalog/browse",
		"/api/v1/portal/datahub/primary/catalog/search?q=x",
		"/api/v1/portal/datahub/primary/catalog/entity?urn=" + dhTestURN,
		"/api/v1/portal/datahub/primary/documents/search?q=x",
		"/api/v1/portal/datahub/primary/documents/browse",
		"/api/v1/portal/datahub/primary/documents/d1",
	}
	for _, p := range paths {
		if rec := serve(h, viewer, "GET", p, ""); rec.Code != http.StatusForbidden {
			t.Errorf("%s status = %d, want 403", p, rec.Code)
		}
	}
	rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/connections", "")
	var resp struct {
		Connections []Connection `json:"connections"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if rec.Code != http.StatusOK || len(resp.Connections) != 0 {
		t.Fatalf("no-access persona should see 200 empty connections, got %d %+v", rec.Code, resp.Connections)
	}
}

func TestBrowseAndSearchAndConnections(t *testing.T) {
	backend := newFakeDataHub()
	backend.tables = []semantic.TableSearchResult{{URN: dhTestURN, Name: "orders"}}
	backend.docs["d1"] = &semantic.DocumentResult{URN: "urn:li:document:d1", Title: "Note"}
	h := newTestHandler(backend, false, readerResolver(), &fakeAuditLogger{})

	if rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/catalog/search?q=ord&tags=urn:li:tag:PII&limit=5&offset=2", ""); rec.Code != http.StatusOK {
		t.Fatalf("search status = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/catalog/browse?limit=10", ""); rec.Code != http.StatusOK {
		t.Fatalf("browse status = %d", rec.Code)
	}
	if rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/documents/search?q=note", ""); rec.Code != http.StatusOK {
		t.Fatalf("doc search status = %d", rec.Code)
	}
	if rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/documents/search", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("doc search missing q status = %d, want 400", rec.Code)
	}
	if rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/documents/browse?offset=-5&limit=0", ""); rec.Code != http.StatusOK {
		t.Fatalf("doc browse status = %d", rec.Code)
	}
	rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/connections", "")
	var resp struct {
		Connections []Connection `json:"connections"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Connections) != 1 || resp.Connections[0].Name != "primary" || resp.Connections[0].Writable {
		t.Fatalf("unexpected connections: %+v", resp.Connections)
	}
}

func TestUpdateGlossaryTermsAndOwners(t *testing.T) {
	backend := newFakeDataHub()
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
	terms := fmt.Sprintf(`{"urn":%q,"add":["urn:li:glossaryTerm:Revenue"]}`, dhTestURN)
	if rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/glossary-terms", terms); rec.Code != http.StatusOK {
		t.Fatalf("glossary status = %d", rec.Code)
	}
	owners := fmt.Sprintf(`{"urn":%q,"add_owners":[{"owner_urn":"urn:li:corpuser:alice","ownership_type":"TECHNICAL_OWNER"}]}`, dhTestURN)
	if rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/owners", owners); rec.Code != http.StatusOK {
		t.Fatalf("owners status = %d", rec.Code)
	}
	if !hasCall(backend.calls, "ApplyGlossaryTermChanges") || !hasCall(backend.calls, "ApplyOwnerChanges") {
		t.Fatalf("expected glossary+owner writes, got %v", backend.calls)
	}
}

func TestAdminCanWrite(t *testing.T) {
	backend := newFakeDataHub()
	h := newTestHandler(backend, true, readerResolver(), &fakeAuditLogger{})
	body := fmt.Sprintf(`{"urn":%q,"description":"by admin"}`, dhTestURN)
	if rec := serve(h, admin, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/description", body); rec.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestDomainSetClearAndValidation(t *testing.T) {
	backend := newFakeDataHub()
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
	set := fmt.Sprintf(`{"urn":%q,"domain":"urn:li:domain:finance"}`, dhTestURN)
	if rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/domain", set); rec.Code != http.StatusOK {
		t.Fatalf("set domain status = %d", rec.Code)
	}
	clr := fmt.Sprintf(`{"urn":%q,"clear_domain":true}`, dhTestURN)
	if rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/domain", clr); rec.Code != http.StatusOK {
		t.Fatalf("clear domain status = %d", rec.Code)
	}
	if len(backend.calls) != 2 || backend.calls[0] != "SetDomain" || backend.calls[1] != "UnsetDomain" {
		t.Fatalf("unexpected domain calls: %v", backend.calls)
	}
	// set with empty domain and no clear -> 400
	backend.calls = nil
	if rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/domain", fmt.Sprintf(`{"urn":%q}`, dhTestURN)); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty domain status = %d, want 400", rec.Code)
	}
	if len(backend.calls) != 0 {
		t.Fatalf("no write should occur for invalid domain, got %v", backend.calls)
	}
}

func TestValidationErrors(t *testing.T) {
	backend := newFakeDataHub()
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
	// invalid JSON
	if rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/description", "{bad"); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid body status = %d, want 400", rec.Code)
	}
	// missing urn (write)
	if rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/tags", `{"add":["a"]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing urn status = %d, want 400", rec.Code)
	}
	// missing urn (entity read)
	if rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/catalog/entity", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("entity missing urn status = %d, want 400", rec.Code)
	}
	// unauth write
	if rec := serve(h, nil, "PUT", "/api/v1/portal/datahub/primary/catalog/entity/tags", `{"urn":"x"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauth write status = %d, want 401", rec.Code)
	}
	// create doc missing title / entity_urn
	if rec := serve(h, viewer, "POST", "/api/v1/portal/datahub/primary/documents", `{"entity_urn":"`+dhTestURN+`","content":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing title status = %d, want 400", rec.Code)
	}
	if rec := serve(h, viewer, "POST", "/api/v1/portal/datahub/primary/documents", `{"title":"t"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing entity_urn status = %d, want 400", rec.Code)
	}
	// update doc missing title
	if rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/documents/d1", `{"content":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("update missing title status = %d, want 400", rec.Code)
	}
}

func TestUpstreamErrors_502(t *testing.T) {
	// Each write carries a WELL-FORMED payload so the request passes validation and
	// reaches the writer: a 502 here proves the genuine-upstream-failure path, as
	// distinct from the malformed-value 400 path covered by TestMalformedValue_400.
	writeCases := []struct{ ep, body string }{
		{"/api/v1/portal/datahub/primary/catalog/entity/description", fmt.Sprintf(`{"urn":%q,"description":"x"}`, dhTestURN)},
		{"/api/v1/portal/datahub/primary/catalog/entity/tags", fmt.Sprintf(`{"urn":%q,"add":["urn:li:tag:PII"]}`, dhTestURN)},
		{"/api/v1/portal/datahub/primary/catalog/entity/glossary-terms", fmt.Sprintf(`{"urn":%q,"add":["urn:li:glossaryTerm:Revenue"]}`, dhTestURN)},
		{"/api/v1/portal/datahub/primary/catalog/entity/owners", fmt.Sprintf(`{"urn":%q,"add_owners":[{"owner_urn":"urn:li:corpuser:alice"}]}`, dhTestURN)},
		{"/api/v1/portal/datahub/primary/catalog/entity/domain", fmt.Sprintf(`{"urn":%q,"domain":"urn:li:domain:finance"}`, dhTestURN)},
	}
	for _, tc := range writeCases {
		backend := newFakeDataHub()
		backend.writeErr = fmt.Errorf("datahub down")
		log := &fakeAuditLogger{}
		h := newTestHandler(backend, true, writerResolver(), log)
		if rec := serve(h, viewer, "PUT", tc.ep, tc.body); rec.Code != http.StatusBadGateway {
			t.Errorf("%s status = %d, want 502", tc.ep, rec.Code)
		}
		if ev := log.last(); ev == nil || ev.Success {
			t.Errorf("%s: expected unsuccessful audit event", tc.ep)
		}
	}
	readPaths := []string{
		"/api/v1/portal/datahub/primary/catalog/browse",
		"/api/v1/portal/datahub/primary/catalog/search?q=x",
		"/api/v1/portal/datahub/primary/catalog/entity?urn=" + dhTestURN,
		"/api/v1/portal/datahub/primary/documents/search?q=x",
		"/api/v1/portal/datahub/primary/documents/browse",
		"/api/v1/portal/datahub/primary/documents/d1",
	}
	for _, p := range readPaths {
		backend := newFakeDataHub()
		backend.readErr = fmt.Errorf("datahub down")
		h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
		if rec := serve(h, viewer, "GET", p, ""); rec.Code != http.StatusBadGateway {
			t.Errorf("%s status = %d, want 502", p, rec.Code)
		}
	}
	// document upsert/delete upstream errors
	backend := newFakeDataHub()
	backend.upsertErr = fmt.Errorf("boom")
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
	if rec := serve(h, viewer, "POST", "/api/v1/portal/datahub/primary/documents", `{"entity_urn":"`+dhTestURN+`","title":"t"}`); rec.Code != http.StatusBadGateway {
		t.Errorf("create upstream error status = %d, want 502", rec.Code)
	}
	if rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/documents/d1", `{"title":"t"}`); rec.Code != http.StatusBadGateway {
		t.Errorf("update upstream error status = %d, want 502", rec.Code)
	}
	backend2 := newFakeDataHub()
	backend2.deleteErr = fmt.Errorf("boom")
	h2 := newTestHandler(backend2, true, writerResolver(), &fakeAuditLogger{})
	if rec := serve(h2, viewer, "DELETE", "/api/v1/portal/datahub/primary/documents/d1", ""); rec.Code != http.StatusBadGateway {
		t.Errorf("delete upstream error status = %d, want 502", rec.Code)
	}
}

// TestMalformedValue_400 is the #785 acceptance criterion: a malformed metadata
// value (e.g. "test") is a client error rejected with 400 and a human-readable
// message, and never reaches the writer/DataHub (which would otherwise 502).
func TestMalformedValue_400(t *testing.T) {
	cases := []struct{ name, ep, body string }{
		{"tag", "/api/v1/portal/datahub/primary/catalog/entity/tags", fmt.Sprintf(`{"urn":%q,"add":["test"]}`, dhTestURN)},
		{"tag-remove", "/api/v1/portal/datahub/primary/catalog/entity/tags", fmt.Sprintf(`{"urn":%q,"remove":["nope"]}`, dhTestURN)},
		{"glossary", "/api/v1/portal/datahub/primary/catalog/entity/glossary-terms", fmt.Sprintf(`{"urn":%q,"add":["Revenue"]}`, dhTestURN)},
		{"owner", "/api/v1/portal/datahub/primary/catalog/entity/owners", fmt.Sprintf(`{"urn":%q,"add_owners":[{"owner_urn":"alice"}]}`, dhTestURN)},
		{"domain", "/api/v1/portal/datahub/primary/catalog/entity/domain", fmt.Sprintf(`{"urn":%q,"domain":"finance"}`, dhTestURN)},
		{"empty-tag-urn", "/api/v1/portal/datahub/primary/catalog/entity/tags", fmt.Sprintf(`{"urn":%q,"add":["urn:li:tag:"]}`, dhTestURN)},
	}
	for _, tc := range cases {
		backend := newFakeDataHub()
		h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
		rec := serve(h, viewer, "PUT", tc.ep, tc.body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", tc.name, rec.Code, rec.Body.String())
		}
		var pd problemDetail
		_ = json.Unmarshal(rec.Body.Bytes(), &pd)
		if !strings.Contains(pd.Detail, "must be a") {
			t.Errorf("%s: detail = %q, want a human-readable URN hint", tc.name, pd.Detail)
		}
		if len(backend.calls) != 0 {
			t.Errorf("%s: writer must not be called for a malformed value, got %v", tc.name, backend.calls)
		}
	}
}

// TestCatalogLookups exercises the picker lookup endpoints: name-searchable
// results on success, read-gating, and a 502 on genuine upstream failure.
func TestCatalogLookups(t *testing.T) {
	refs := []semantic.EntityRef{{URN: "urn:li:tag:PII", Name: "PII"}, {URN: "urn:li:domain:finance", Name: "Finance"}}
	paths := []string{
		"/api/v1/portal/datahub/primary/catalog/lookup/tags?q=PI",
		"/api/v1/portal/datahub/primary/catalog/lookup/glossary-terms?q=Rev",
		"/api/v1/portal/datahub/primary/catalog/lookup/domains",
	}
	for _, p := range paths {
		backend := newFakeDataHub()
		backend.refs = refs
		h := newTestHandler(backend, false, readerResolver(), &fakeAuditLogger{})
		rec := serve(h, viewer, "GET", p, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d (%s)", p, rec.Code, rec.Body.String())
		}
		var resp struct {
			Results []semantic.EntityRef `json:"results"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Results) != 2 || resp.Results[0].Name != "PII" {
			t.Fatalf("%s unexpected results: %+v", p, resp.Results)
		}
		// no DataHub access -> 403
		hNo := newTestHandler(backend, false, noAccessResolver(), &fakeAuditLogger{})
		if rec := serve(hNo, viewer, "GET", p, ""); rec.Code != http.StatusForbidden {
			t.Errorf("%s no-access status = %d, want 403", p, rec.Code)
		}
		// upstream failure -> 502
		backendErr := newFakeDataHub()
		backendErr.readErr = fmt.Errorf("datahub down")
		hErr := newTestHandler(backendErr, false, readerResolver(), &fakeAuditLogger{})
		if rec := serve(hErr, viewer, "GET", p, ""); rec.Code != http.StatusBadGateway {
			t.Errorf("%s upstream-error status = %d, want 502", p, rec.Code)
		}
	}
}

func TestGetMissingDocument_404AndInvalidURN_400(t *testing.T) {
	backend := newFakeDataHub()
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
	if rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/documents/nope", ""); rec.Code != http.StatusNotFound {
		t.Errorf("missing doc status = %d, want 404", rec.Code)
	}
	backend.resolveErr = fmt.Errorf("bad urn")
	if rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/catalog/entity?urn=junk", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid urn status = %d, want 400", rec.Code)
	}
}

// TestCatalogEntityColumnsFailure_StillOK proves the best-effort columns read:
// when GetTableContext succeeds but GetColumnsContext fails, the entity read still
// returns 200 with the table context and no columns (the failure is warned and
// swallowed, never surfaced to the caller).
func TestCatalogEntityColumnsFailure_StillOK(t *testing.T) {
	backend := newFakeDataHub()
	backend.descriptions[dhTestURN] = "orders table"
	backend.columnsErr = fmt.Errorf("columns backend unavailable")
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})

	rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/primary/catalog/entity?urn="+dhTestURN, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	var resp catalogEntityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Context == nil || resp.Context.Description != "orders table" {
		t.Fatalf("table context not returned despite columns failure: %+v", resp.Context)
	}
	if resp.Columns != nil {
		t.Fatalf("columns should be nil when the columns read fails, got %+v", resp.Columns)
	}
}

// review fix: full-URN id form round-trips through update/delete.
func TestDocumentURNFormIdRoundTrips(t *testing.T) {
	backend := newFakeDataHub()
	backend.docs["d1"] = &semantic.DocumentResult{URN: "urn:li:document:d1", Title: "Note"}
	h := newTestHandler(backend, true, writerResolver(), &fakeAuditLogger{})
	if rec := serve(h, viewer, "PUT", "/api/v1/portal/datahub/primary/documents/urn:li:document:d1", `{"title":"v2","content":"x"}`); rec.Code != http.StatusOK {
		t.Fatalf("update status = %d (%s)", rec.Code, rec.Body.String())
	}
	if got := backend.docs["d1"]; got == nil || got.Title != "v2" {
		t.Fatalf("update did not target the bare id: %+v", got)
	}
	if rec := serve(h, viewer, "DELETE", "/api/v1/portal/datahub/primary/documents/urn:li:document:d1", ""); rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rec.Code)
	}
	if _, ok := backend.docs["d1"]; ok {
		t.Fatalf("delete via URN-form id did not remove the doc")
	}
}

func TestPureHelpers(t *testing.T) {
	cases := map[string]string{
		dhTestURN:                     "dataset",
		"urn:li:glossaryTerm:Revenue": "glossaryTerm",
		"urn:li:container:(abc)":      "container",
		"urn:li:dashboard:(looker,1)": "dashboard",
		"not-a-urn":                   "",
		"urn:li:":                     "",
	}
	for urn, want := range cases {
		if got := datahubEntityType(urn); got != want {
			t.Errorf("datahubEntityType(%q) = %q, want %q", urn, got, want)
		}
	}
	if documentURN("abc") != "urn:li:document:abc" || documentURN("urn:li:document:abc") != "urn:li:document:abc" {
		t.Error("documentURN mismatch")
	}
	if bareDocumentID("urn:li:document:abc") != "abc" || bareDocumentID("abc") != "abc" {
		t.Error("bareDocumentID mismatch")
	}
	if clampLimit("") != datahubDefaultLimit || clampLimit("500") != datahubMaxLimit || clampLimit("7") != 7 {
		t.Error("clampLimit mismatch")
	}
	if parseOffset("-1") != 0 || parseOffset("3") != 3 {
		t.Error("parseOffset mismatch")
	}
}

func TestListConnections_Unauthenticated_401(t *testing.T) {
	h := newTestHandler(newFakeDataHub(), true, writerResolver(), &fakeAuditLogger{})
	if rec := serve(h, nil, "GET", "/api/v1/portal/datahub/connections", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestListConnections_EmptyBridge(t *testing.T) {
	h := NewHandler(Deps{Bridge: NewStaticBridge(), PersonaResolver: readerResolver(), AdminRoles: []string{"admin"}})
	rec := serve(h, viewer, "GET", "/api/v1/portal/datahub/connections", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp struct {
		Connections []Connection `json:"connections"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Connections) != 0 {
		t.Fatalf("empty bridge should list no connections, got %+v", resp.Connections)
	}
}

func hasCall(calls []string, want string) bool {
	return slices.Contains(calls, want)
}
