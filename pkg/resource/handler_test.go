package resource

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

// --- mock store ---

type mockStore struct {
	resources map[string]*Resource
	// aliases maps a URI a resource has vacated to the resource that vacated it,
	// modeling resource_uri_aliases: a move records one, and GetByURI resolves
	// through it only when no resource holds that address now.
	aliases map[string]string
	// lastListFilter records the filter passed to the most recent List call so
	// tests can assert the handler forwards parsed pagination params.
	lastListFilter Filter
}

func newMockStore() *mockStore {
	return &mockStore{resources: make(map[string]*Resource), aliases: make(map[string]string)}
}

func (m *mockStore) Insert(_ context.Context, r Resource) error {
	m.resources[r.ID] = &r
	return nil
}

func (m *mockStore) Get(_ context.Context, id string) (*Resource, error) {
	r, ok := m.resources[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return r, nil
}

// GetByIDs models the store's bulk read: an id with no row is simply absent
// from the map, which is what the real store answers rather than an error.
func (m *mockStore) GetByIDs(_ context.Context, ids []string) (map[string]*Resource, error) {
	out := make(map[string]*Resource, len(ids))
	for _, id := range ids {
		if r, ok := m.resources[id]; ok {
			out[id] = r
		}
	}
	return out, nil
}

func (m *mockStore) GetByURI(_ context.Context, uri string) (*Resource, error) {
	for _, r := range m.resources {
		if r.URI == uri {
			return r, nil
		}
	}
	// A live URI wins; an address a resource has vacated resolves only when no
	// resource holds it now, which is the order the Postgres store queries in.
	if id, ok := m.aliases[uri]; ok {
		if r, ok := m.resources[id]; ok {
			return r, nil
		}
	}
	return nil, fmt.Errorf("not found: %w", sql.ErrNoRows)
}

func (m *mockStore) List(_ context.Context, filter Filter) ([]Resource, int, error) {
	m.lastListFilter = filter
	var result []Resource
	for _, r := range m.resources {
		for _, sf := range filter.Scopes {
			if sf.Scope == r.Scope && (sf.Scope == ScopeGlobal || sf.ScopeID == r.ScopeID) {
				if filter.Category == "" || filter.Category == r.Category {
					result = append(result, *r)
				}
				break
			}
		}
	}
	return result, len(result), nil
}

func (m *mockStore) Update(_ context.Context, id string, u Update) error {
	r, ok := m.resources[id]
	if !ok {
		return fmt.Errorf("not found")
	}
	if u.DisplayName != nil {
		r.DisplayName = *u.DisplayName
	}
	if u.Description != nil {
		r.Description = *u.Description
	}
	if u.Tags != nil {
		r.Tags = u.Tags
	}
	if u.Category != nil {
		r.Category = *u.Category
	}
	return nil
}

// Move models the real store's refile: the three columns that say where the
// resource lives are rewritten, the address it leaves becomes an alias, and the
// address it takes stops being one. GetByURI above resolves a live URI only, so
// the alias map is read there too -- a fake that dropped the alias would let a
// test pass that the real store fails.
func (m *mockStore) Move(_ context.Context, id string, mv Move) error {
	r, ok := m.resources[id]
	if !ok {
		return fmt.Errorf("resource not found: %s", id)
	}
	for other, res := range m.resources {
		if other != id && res.URI == mv.URI {
			return ErrURIConflict
		}
	}
	if m.aliases == nil {
		m.aliases = map[string]string{}
	}
	if mv.FromURI != "" && mv.FromURI != mv.URI {
		m.aliases[mv.FromURI] = id
	}
	delete(m.aliases, mv.URI)
	r.Scope, r.ScopeID, r.URI = mv.Scope, mv.ScopeID, mv.URI
	r.UpdatedAt = time.Now().UTC()
	return nil
}

func (m *mockStore) Delete(_ context.Context, id string) error {
	if _, ok := m.resources[id]; !ok {
		return fmt.Errorf("not found")
	}
	delete(m.resources, id)
	return nil
}

// --- mock S3 client ---

type mockS3 struct {
	objects map[string][]byte
}

func newMockS3() *mockS3 {
	return &mockS3{objects: make(map[string][]byte)}
}

func (m *mockS3) PutObject(_ context.Context, _, key string, data []byte, _ string) error {
	m.objects[key] = data
	return nil
}

func (m *mockS3) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	data, ok := m.objects[key]
	if !ok {
		return nil, "", fmt.Errorf("not found")
	}
	// Return empty content type so the handler falls back to resource MIMEType.
	return data, "", nil
}

func (m *mockS3) DeleteObject(_ context.Context, _, key string) error {
	delete(m.objects, key)
	return nil
}

// --- helpers ---

// testClaims returns a Claims for a test user who can see/write global scope.
func testClaims() *Claims {
	return &Claims{
		Sub:      "user-123",
		Email:    "user@example.com",
		Personas: []string{"analyst"},
		Roles:    []string{"admin"},
		IsAdmin:  true,
	}
}

func okExtractor(_ *http.Request) (*Claims, error) {
	return testClaims(), nil
}

// memberClaims is an ordinary caller: no platform-admin authority and no
// persona-admin grant, so visibility is exactly their own scopes. The
// not-visible tests use it, since an admin is deliberately NOT confined to their
// visible scopes on a resource named by id (see CanAccessResource).
func memberClaims() *Claims {
	return &Claims{
		Sub:      "user-123",
		Email:    "user@example.com",
		Personas: []string{"analyst"},
		Roles:    []string{"analyst"},
	}
}

func memberExtractor(_ *http.Request) (*Claims, error) {
	return memberClaims(), nil
}

func failExtractor(_ *http.Request) (*Claims, error) {
	return nil, fmt.Errorf("no auth")
}

// newTestHandler creates a handler with mock deps and the given extractor.
func newTestHandler(store *mockStore, s3 *mockS3, extractFn ClaimsExtractor) *Handler {
	deps := Deps{
		Store:     store,
		S3Client:  s3,
		S3Bucket:  "test-bucket",
		URIScheme: "mcp",
	}
	return NewHandler(deps, extractFn, nil)
}

// buildMultipartRequest builds a multipart form POST request with the given fields and file.
func buildMultipartRequest(t *testing.T, fields map[string]string, fileContent []byte, filename string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}

	if fileContent != nil {
		part, err := w.CreateFormFile("file", filename)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write(fileContent); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/resources", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func decodeJSON(t *testing.T, body io.Reader) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(body).Decode(&m); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	return m
}

// seedResource inserts a test resource into the mock store and S3.
func seedResource(store *mockStore, s3 *mockS3, id string, scope Scope, scopeID, uploaderSub string) *Resource { //nolint:revive // test helper clarity
	r := &Resource{
		ID:            id,
		Scope:         scope,
		ScopeID:       scopeID,
		Category:      "samples",
		Filename:      "test.csv",
		DisplayName:   "Test Resource",
		Description:   "A test resource.",
		MIMEType:      "text/csv",
		SizeBytes:     12,
		S3Key:         "resources/" + string(scope) + "/" + id + "/test.csv",
		URI:           BuildURI("mcp", scope, scopeID, "samples", "test.csv"),
		Tags:          []string{"test"},
		UploaderSub:   uploaderSub,
		UploaderEmail: "owner@example.com",
	}
	store.resources[id] = r
	if s3 != nil {
		s3.objects[r.S3Key] = []byte("hello,world\n")
	}
	return r
}

// --- Create tests ---

func TestHandleCreate_Success(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	fields := map[string]string{
		"scope":        "global",
		"category":     "samples",
		"display_name": "My File",
		"description":  "A sample CSV.",
	}
	req := buildMultipartRequest(t, fields, []byte("col1,col2\na,b"), "data.csv")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeJSON(t, rec.Body)
	if resp["display_name"] != "My File" {
		t.Errorf("display_name = %v", resp["display_name"])
	}
	if resp["category"] != "samples" {
		t.Errorf("category = %v", resp["category"])
	}

	// Verify S3 received the object.
	if len(s3.objects) != 1 {
		t.Errorf("expected 1 S3 object, got %d", len(s3.objects))
	}

	// Verify store has the resource.
	if len(store.resources) != 1 {
		t.Errorf("expected 1 resource in store, got %d", len(store.resources))
	}
}

func TestOnCreate_CalledOnCreate(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	var created *Resource
	deps := Deps{
		Store:     store,
		S3Client:  s3,
		S3Bucket:  "test-bucket",
		URIScheme: "mcp",
		OnCreate:  func(res *Resource) { created = res },
	}
	h := NewHandler(deps, okExtractor, nil)

	fields := map[string]string{
		"scope":        "global",
		"category":     "samples",
		"display_name": "Notify Test",
		"description":  "Testing notify callback",
	}
	req := buildMultipartRequest(t, fields, []byte("data"), "test.txt")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if created == nil {
		t.Fatal("expected OnCreate to be called with the resource")
	}
	if created.DisplayName != "Notify Test" {
		t.Errorf("created.DisplayName = %q, want Notify Test", created.DisplayName)
	}
}

func TestOnDelete_CalledOnDelete(t *testing.T) {
	store := newMockStore()
	store.resources["r1"] = &Resource{
		ID: "r1", Scope: ScopeGlobal, UploaderSub: "user-123",
		URI: "mcp://global/test/file.txt", S3Key: "resources/global/r1/file.txt",
	}
	s3 := newMockS3()
	s3.objects["resources/global/r1/file.txt"] = []byte("data")
	var deletedURI string
	deps := Deps{
		Store:     store,
		S3Client:  s3,
		S3Bucket:  "test-bucket",
		URIScheme: "mcp",
		OnDelete:  func(uri string) { deletedURI = uri },
	}
	h := NewHandler(deps, okExtractor, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/resources/r1", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if deletedURI != "mcp://global/test/file.txt" {
		t.Errorf("deletedURI = %q, want mcp://global/test/file.txt", deletedURI)
	}
}

func TestHandleCreate_Unauthorized(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, failExtractor)

	fields := map[string]string{
		"scope":        "global",
		"category":     "samples",
		"display_name": "My File",
		"description":  "A sample.",
	}
	req := buildMultipartRequest(t, fields, []byte("data"), "file.csv")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreate_ValidationErrors(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	tests := []struct {
		name   string
		fields map[string]string
	}{
		{
			name: "missing display_name",
			fields: map[string]string{
				"scope":       "global",
				"category":    "samples",
				"description": "A sample.",
			},
		},
		{
			name: "missing description",
			fields: map[string]string{
				"scope":        "global",
				"category":     "samples",
				"display_name": "My File",
			},
		},
		{
			name: "missing category",
			fields: map[string]string{
				"scope":        "global",
				"display_name": "My File",
				"description":  "A sample.",
			},
		},
		{
			name: "invalid scope",
			fields: map[string]string{
				"scope":        "bogus",
				"category":     "samples",
				"display_name": "My File",
				"description":  "A sample.",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := buildMultipartRequest(t, tt.fields, []byte("data"), "file.csv")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleCreate_PermissionDenied(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	// Create a non-admin extractor.
	nonAdmin := func(_ *http.Request) (*Claims, error) {
		return &Claims{
			Sub:      "user-456",
			Email:    "other@example.com",
			Personas: []string{"analyst"},
			Roles:    []string{"analyst"},
		}, nil
	}
	h := newTestHandler(store, s3, nonAdmin)

	// Non-admin user tries to write to global scope.
	fields := map[string]string{
		"scope":        "global",
		"category":     "samples",
		"display_name": "My File",
		"description":  "A sample.",
	}
	req := buildMultipartRequest(t, fields, []byte("data"), "file.csv")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- List tests ---

func TestHandleList_Success(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")
	seedResource(store, s3, "res-2", ScopeGlobal, "", "user-123")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeJSON(t, rec.Body)
	resources, ok := resp["resources"].([]any)
	if !ok {
		t.Fatalf("resources not an array: %T", resp["resources"])
	}
	if len(resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(resources))
	}
	if resp["total"] != float64(2) {
		t.Errorf("total = %v, want 2", resp["total"])
	}
}

func TestHandleList_ForwardsLimitAndOffset(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantLimit int
		wantOff   int
	}{
		{"client limit and offset honored", "?limit=25&offset=50", 25, 50},
		{"absent limit falls back to default", "?offset=10", DefaultListLimit, 10},
		{"non-positive limit falls back to default", "?limit=0", DefaultListLimit, 0},
		{"invalid limit falls back to default", "?limit=abc", DefaultListLimit, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMockStore()
			h := newTestHandler(store, nil, okExtractor)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources"+tt.query, http.NoBody)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			if store.lastListFilter.Limit != tt.wantLimit {
				t.Errorf("Limit = %d, want %d", store.lastListFilter.Limit, tt.wantLimit)
			}
			if store.lastListFilter.Offset != tt.wantOff {
				t.Errorf("Offset = %d, want %d", store.lastListFilter.Offset, tt.wantOff)
			}
		})
	}
}

func TestHandleList_Empty(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, okExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeJSON(t, rec.Body)
	resources, ok := resp["resources"].([]any)
	if !ok {
		t.Fatalf("resources not an array: %T", resp["resources"])
	}
	if len(resources) != 0 {
		t.Errorf("expected 0 resources, got %d", len(resources))
	}
}

// --- Get tests ---

func TestHandleGet_Success(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	resp := decodeJSON(t, rec.Body)
	if resp["id"] != "res-1" {
		t.Errorf("id = %v, want res-1", resp["id"])
	}
}

func TestHandleGet_NotFound(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, okExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/no-such", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGet_NotVisible(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, memberExtractor)

	// Seed a user-scoped resource owned by a different user.
	seedResource(store, nil, "res-private", ScopeUser, "other-user", "other-user")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-private", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (not visible), got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- Get Content tests ---

func TestHandleGetContent_Success(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/content", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if ct := rec.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "test.csv") {
		t.Errorf("Content-Disposition = %q, want filename=test.csv", rec.Header().Get("Content-Disposition"))
	}
	if rec.Body.String() != "hello,world\n" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestHandleGetContent_NoS3(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, okExtractor)
	// nil S3Client in deps.
	h.deps.S3Client = nil

	seedResource(store, nil, "res-1", ScopeGlobal, "", "user-123")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/content", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- Update tests ---

func TestHandleUpdate_Success(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

	newName := "Updated Name"
	body, _ := json.Marshal(Update{DisplayName: &newName})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/resources/res-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeJSON(t, rec.Body)
	if resp["display_name"] != "Updated Name" {
		t.Errorf("display_name = %v", resp["display_name"])
	}
}

func TestHandleUpdate_NotFound(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, okExtractor)

	newName := "Updated"
	body, _ := json.Marshal(Update{DisplayName: &newName})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/resources/no-such", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdate_PermissionDenied(t *testing.T) {
	store := newMockStore()
	nonAdmin := func(_ *http.Request) (*Claims, error) {
		return &Claims{
			Sub:      "other-user",
			Email:    "other@example.com",
			Personas: []string{"analyst"},
			Roles:    []string{"analyst"},
		}, nil
	}
	h := newTestHandler(store, nil, nonAdmin)

	// Resource owned by user-123, but our caller is other-user (non-admin).
	seedResource(store, nil, "res-1", ScopeGlobal, "", "user-123")

	newName := "Hacked"
	body, _ := json.Marshal(Update{DisplayName: &newName})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/resources/res-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdate_ValidationError(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, okExtractor)

	seedResource(store, nil, "res-1", ScopeGlobal, "", "user-123")

	// Empty display name is invalid.
	empty := "   "
	body, _ := json.Marshal(Update{DisplayName: &empty})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/resources/res-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- Delete tests ---

func TestHandleDelete_Success(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	r := seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/resources/res-1", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify removed from store.
	if _, ok := store.resources["res-1"]; ok {
		t.Error("resource still in store after delete")
	}

	// Verify removed from S3.
	if _, ok := s3.objects[r.S3Key]; ok {
		t.Error("S3 object still exists after delete")
	}
}

func TestHandleDelete_NotFound(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, okExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/resources/no-such", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleDelete_PermissionDenied(t *testing.T) {
	store := newMockStore()
	nonAdmin := func(_ *http.Request) (*Claims, error) {
		return &Claims{
			Sub:      "other-user",
			Email:    "other@example.com",
			Personas: []string{"analyst"},
			Roles:    []string{"analyst"},
		}, nil
	}
	h := newTestHandler(store, nil, nonAdmin)

	// Resource owned by user-123, caller is other-user (non-admin).
	seedResource(store, nil, "res-1", ScopeGlobal, "", "user-123")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/resources/res-1", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- Helper tests ---

func TestNarrowScopes(t *testing.T) {
	visible := []ScopeFilter{
		{Scope: ScopeGlobal},
		{Scope: ScopeUser, ScopeID: "user-1"},
		{Scope: ScopePersona, ScopeID: "analyst"},
	}

	// Narrow to persona scope.
	result := narrowScopes(visible, "persona", "")
	if len(result) != 1 || result[0].Scope != ScopePersona {
		t.Errorf("narrowed = %v, want [persona/analyst]", result)
	}

	// Narrow to persona with specific ID.
	result = narrowScopes(visible, "persona", "analyst")
	if len(result) != 1 || result[0].ScopeID != "analyst" {
		t.Errorf("narrowed with ID = %v", result)
	}

	// No match returns empty (never expands visibility).
	result = narrowScopes(visible, "bogus", "")
	if len(result) != 0 {
		t.Errorf("no-match should return empty, got %d", len(result))
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, http.StatusOK, map[string]string{"key": "value"})

	if rec.Code != http.StatusOK {
		t.Errorf("code = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	resp := decodeJSON(t, rec.Body)
	if resp["key"] != "value" {
		t.Errorf("body = %v", resp)
	}
}

func TestWriteError_Sanitizes500(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusInternalServerError, "database connection failed: dial tcp 127.0.0.1:5432")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("code = %d", rec.Code)
	}
	resp := decodeJSON(t, rec.Body)
	msg, _ := resp["error"].(string)
	if strings.Contains(msg, "127.0.0.1") {
		t.Errorf("500 error should sanitize internal details, got %q", msg)
	}
}

func TestValidateUpdate(t *testing.T) {
	// Valid update.
	name := "Valid Name"
	if err := validateUpdate(Update{DisplayName: &name}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}

	// Invalid category.
	badCat := "INVALID"
	if err := validateUpdate(Update{Category: &badCat}); err == nil {
		t.Error("expected error for invalid category")
	}

	// Invalid tags.
	if err := validateUpdate(Update{Tags: []string{"INVALID TAG"}}); err == nil {
		t.Error("expected error for invalid tags")
	}
}

func TestHandleCreate_NoFile(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	// Send multipart form without a file.
	fields := map[string]string{
		"scope":        "global",
		"category":     "samples",
		"display_name": "My File",
		"description":  "A sample.",
	}
	req := buildMultipartRequest(t, fields, nil, "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleList_WithScopeParam(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, okExtractor)

	seedResource(store, nil, "res-1", ScopeGlobal, "", "user-123")
	seedResource(store, nil, "res-2", ScopeUser, "user-123", "user-123")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources?scope=user&scope_id=user-123", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeJSON(t, rec.Body)
	resources, _ := resp["resources"].([]any)
	if len(resources) != 1 {
		t.Errorf("expected 1 user-scoped resource, got %d", len(resources))
	}
}

func TestHandleGetContent_TextInline(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/content", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	// For text content, Content-Disposition should be "inline".
	disp := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(disp, "inline") {
		t.Errorf("expected inline disposition for text, got %q", disp)
	}
}

func TestHandleUpdate_InvalidJSON(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, okExtractor)

	seedResource(store, nil, "res-1", ScopeGlobal, "", "user-123")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/resources/res-1", strings.NewReader("{invalid"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- Additional tests to improve coverage ---

func TestNewHandler_WithAuthMiddleware(t *testing.T) {
	store := newMockStore()
	deps := Deps{Store: store, URIScheme: "mcp"}

	authCalled := false
	authMiddle := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authCalled = true
			next.ServeHTTP(w, r)
		})
	}

	h := NewHandler(deps, okExtractor, authMiddle)

	seedResource(store, nil, "res-1", ScopeGlobal, "", "user-123")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !authCalled {
		t.Error("auth middleware was not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleList_Unauthorized(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, failExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleGet_Unauthorized(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, failExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleGetContent_Unauthorized(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, failExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/content", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleGetContent_NotFound(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/no-such/content", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleGetContent_NotVisible(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, memberExtractor)

	seedResource(store, s3, "res-priv", ScopeUser, "other-user", "other-user")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-priv/content", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleUpdate_Unauthorized(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, failExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/resources/res-1", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleUpdate_NotVisible(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, memberExtractor)

	seedResource(store, nil, "res-priv", ScopeUser, "other-user", "other-user")

	name := "New Name"
	body, _ := json.Marshal(Update{DisplayName: &name})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/resources/res-priv", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleDelete_Unauthorized(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, failExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/resources/res-1", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandleDelete_NotVisible(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, memberExtractor)

	seedResource(store, nil, "res-priv", ScopeUser, "other-user", "other-user")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/resources/res-priv", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// errStore always returns errors for specific operations.
type errStore struct {
	mockStore
	listErr   error
	updateErr error
	deleteErr error
}

func (e *errStore) List(_ context.Context, _ Filter) ([]Resource, int, error) {
	if e.listErr != nil {
		return nil, 0, e.listErr
	}
	return e.mockStore.List(context.Background(), Filter{})
}

func (e *errStore) Update(_ context.Context, _ string, _ Update) error {
	if e.updateErr != nil {
		return e.updateErr
	}
	return nil
}

func (e *errStore) Delete(_ context.Context, _ string) error {
	if e.deleteErr != nil {
		return e.deleteErr
	}
	return nil
}

func TestHandleList_StoreError(t *testing.T) {
	store := &errStore{
		mockStore: *newMockStore(),
		listErr:   fmt.Errorf("db connection lost"),
	}
	deps := Deps{Store: store, URIScheme: "mcp"}
	h := NewHandler(deps, okExtractor, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleUpdate_StoreError(t *testing.T) {
	es := &errStore{
		mockStore: *newMockStore(),
		updateErr: fmt.Errorf("update failed"),
	}
	seedResource(&es.mockStore, nil, "res-1", ScopeGlobal, "", "user-123")

	deps := Deps{Store: es, URIScheme: "mcp"}
	h := NewHandler(deps, okExtractor, nil)

	name := "Updated"
	body, _ := json.Marshal(Update{DisplayName: &name})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/resources/res-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleDelete_StoreError(t *testing.T) {
	es := &errStore{
		mockStore: *newMockStore(),
		deleteErr: fmt.Errorf("delete failed"),
	}
	seedResource(&es.mockStore, nil, "res-1", ScopeGlobal, "", "user-123")

	deps := Deps{Store: es, URIScheme: "mcp"}
	h := NewHandler(deps, okExtractor, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/resources/res-1", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// errS3 returns errors from GetObject.
type errS3 struct {
	mockS3
}

func (*errS3) GetObject(_ context.Context, _, _ string) (body []byte, ct string, err error) {
	return nil, "", fmt.Errorf("s3 error")
}

func TestHandleGetContent_S3Error(t *testing.T) {
	store := newMockStore()
	s3err := &errS3{}
	deps := Deps{Store: store, S3Client: s3err, S3Bucket: "test-bucket", URIScheme: "mcp"}
	h := NewHandler(deps, okExtractor, nil)

	seedResource(store, nil, "res-1", ScopeGlobal, "", "user-123")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/content", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "did not answer") {
		t.Errorf("body does not say what happened: %q", rec.Body.String())
	}
}

// orphanS3 answers the way a blob store answers for a key that is not there.
type orphanS3 struct {
	mockS3
}

func (*orphanS3) GetObject(_ context.Context, _, _ string) (body []byte, ct string, err error) {
	return nil, "", errors.New("s3 get: failed to get object: NoSuchKey: The specified key does not exist")
}

// A resource whose row outlived its stored file is a 404 about the CONTENT, not
// a server error. The resources/read middleware and the search-index consumer
// have always drawn this distinction through IsObjectNotFound; the REST route
// did not, so every metadata-only row answered the portal with a 500.
func TestHandleGetContent_OrphanedBlob(t *testing.T) {
	store := newMockStore()
	deps := Deps{Store: store, S3Client: &orphanS3{}, S3Bucket: "test-bucket", URIScheme: "mcp"}
	h := NewHandler(deps, okExtractor, nil)

	seedResource(store, nil, "res-1", ScopeGlobal, "", "user-123")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/content", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
	// It must not read as "no such resource": the record is still listed, still
	// editable and still deletable on the page the reader is standing on.
	body := rec.Body.String()
	if !strings.Contains(body, "stored file is missing") {
		t.Errorf("body does not say the file is what is missing: %q", body)
	}
	if !strings.Contains(body, "record is still here") {
		t.Errorf("body does not say the record survives: %q", body)
	}
}

func TestHandleGetContent_Disposition(t *testing.T) {
	// Passive families are previewable, so they are served inline; active
	// families execute when a browser renders them and must always download.
	cases := []struct {
		name     string
		filename string
		mime     string
		body     []byte
		wantDisp string
	}{
		{"image previews inline", "image.png", "image/png", []byte{0x89, 0x50, 0x4E, 0x47}, "inline"},
		{"json previews inline", "results.json", "application/json", []byte(`{"a":1}`), "inline"},
		{"text previews inline", "notes.txt", "text/plain", []byte("hello"), "inline"},
		{"html always downloads", "page.html", "text/html", []byte("<b>x</b>"), "attachment"},
		{"svg always downloads", "chart.svg", "image/svg+xml", []byte("<svg/>"), "attachment"},
		{"unknown binary downloads", "blob.bin", "application/octet-stream", []byte{0x00, 0x01}, "inline"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMockStore()
			s3 := newMockS3()
			h := newTestHandler(store, s3, okExtractor)

			r := &Resource{
				ID:            "res-bin",
				Scope:         ScopeGlobal,
				Category:      "samples",
				Filename:      tc.filename,
				DisplayName:   "A File",
				Description:   "A test file.",
				MIMEType:      tc.mime,
				SizeBytes:     int64(len(tc.body)),
				S3Key:         "resources/global/res-bin/" + tc.filename,
				URI:           BuildURI("mcp", ScopeGlobal, "", "samples", tc.filename),
				Tags:          []string{},
				UploaderSub:   "user-123",
				UploaderEmail: "user@example.com",
			}
			store.resources["res-bin"] = r
			s3.objects[r.S3Key] = tc.body

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-bin/content", http.NoBody)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", rec.Code)
			}
			if disp := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, tc.wantDisp) {
				t.Errorf("expected %s disposition, got %q", tc.wantDisp, disp)
			}
			// Every raw content response must refuse browser content sniffing.
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
		})
	}
}

// errInsertStore fails on Insert only.
type errInsertStore struct {
	mockStore
}

func (*errInsertStore) Insert(_ context.Context, _ Resource) error {
	return fmt.Errorf("insert failed")
}

func TestHandleCreate_StoreInsertError(t *testing.T) {
	es := &errInsertStore{mockStore: *newMockStore()}
	s3 := newMockS3()
	deps := Deps{Store: es, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp"}
	h := NewHandler(deps, okExtractor, nil)

	fields := map[string]string{
		"scope":        "global",
		"category":     "samples",
		"display_name": "My File",
		"description":  "A sample.",
	}
	req := buildMultipartRequest(t, fields, []byte("data"), "file.csv")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// errPutS3 fails on PutObject.
type errPutS3 struct {
	mockS3
}

func (*errPutS3) PutObject(_ context.Context, _, _ string, _ []byte, _ string) error {
	return fmt.Errorf("s3 put error")
}

func TestHandleCreate_S3PutError(t *testing.T) {
	store := newMockStore()
	s3err := &errPutS3{}
	deps := Deps{Store: store, S3Client: s3err, S3Bucket: "test-bucket", URIScheme: "mcp"}
	h := NewHandler(deps, okExtractor, nil)

	fields := map[string]string{
		"scope":        "global",
		"category":     "samples",
		"display_name": "My File",
		"description":  "A sample.",
	}
	req := buildMultipartRequest(t, fields, []byte("data"), "file.csv")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// 503, not 500: the cause is outside the platform and nothing was created,
	// so retrying is the right response.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}

	// The body has to say what happened. writeError truncates a 5xx message at
	// its first colon, which reduced the old "storing file: s3 put: ..." to the
	// bare fragment "storing file" -- a storage outage that read, to the person
	// who hit it, as having been refused permission.
	body := rec.Body.String()
	for _, want := range []string{"did not accept the file", "Nothing was saved"} {
		if !strings.Contains(body, want) {
			t.Errorf("body %q does not state %q", body, want)
		}
	}
	if strings.Contains(body, "storing file") {
		t.Errorf("body still carries the truncated fragment: %q", body)
	}
}

func TestHandleCreate_UserScope(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	fields := map[string]string{
		"scope":        "user",
		"scope_id":     "user-123",
		"category":     "samples",
		"display_name": "My File",
		"description":  "A sample.",
	}
	req := buildMultipartRequest(t, fields, []byte("data"), "file.csv")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreate_NoS3Client(t *testing.T) {
	store := newMockStore()
	// No S3 client -- persistResource should skip the S3 put.
	deps := Deps{Store: store, URIScheme: "mcp"}
	h := NewHandler(deps, okExtractor, nil)

	fields := map[string]string{
		"scope":        "global",
		"category":     "samples",
		"display_name": "My File",
		"description":  "A sample.",
	}
	req := buildMultipartRequest(t, fields, []byte("data"), "file.csv")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

// errGetAfterInsertStore inserts OK but fails on Get after insert.
type errGetAfterInsertStore struct {
	mockStore
	insertCalled bool
}

func (e *errGetAfterInsertStore) Insert(ctx context.Context, r Resource) error {
	e.insertCalled = true
	return e.mockStore.Insert(ctx, r)
}

func (e *errGetAfterInsertStore) Get(_ context.Context, id string) (*Resource, error) {
	if e.insertCalled {
		// After insert, fail the re-fetch.
		return nil, fmt.Errorf("get after insert failed")
	}
	return e.mockStore.Get(context.Background(), id)
}

func TestHandleCreate_GetAfterInsertFails(t *testing.T) {
	es := &errGetAfterInsertStore{mockStore: *newMockStore()}
	deps := Deps{Store: es, URIScheme: "mcp"}
	h := NewHandler(deps, okExtractor, nil)

	fields := map[string]string{
		"scope":        "global",
		"category":     "samples",
		"display_name": "My File",
		"description":  "A sample.",
	}
	req := buildMultipartRequest(t, fields, []byte("data"), "file.csv")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Should still return 201 (fallback to pre-read resource).
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

// errGetAfterUpdateStore updates OK but fails on the second Get.
type errGetAfterUpdateStore struct {
	mockStore
	getCalls int
}

func (e *errGetAfterUpdateStore) Get(_ context.Context, id string) (*Resource, error) {
	e.getCalls++
	if e.getCalls > 1 {
		return nil, fmt.Errorf("get after update failed")
	}
	return e.mockStore.Get(context.Background(), id)
}

func TestHandleUpdate_GetAfterUpdateFails(t *testing.T) {
	es := &errGetAfterUpdateStore{mockStore: *newMockStore()}
	seedResource(&es.mockStore, nil, "res-1", ScopeGlobal, "", "user-123")

	deps := Deps{Store: es, URIScheme: "mcp"}
	h := NewHandler(deps, okExtractor, nil)

	name := "Updated"
	body, _ := json.Marshal(Update{DisplayName: &name})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/resources/res-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleDelete_NoS3Client(t *testing.T) {
	store := newMockStore()
	deps := Deps{Store: store, URIScheme: "mcp"}
	h := NewHandler(deps, okExtractor, nil)

	seedResource(store, nil, "res-1", ScopeGlobal, "", "user-123")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/resources/res-1", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleList_NilResources(t *testing.T) {
	// Test that store returning nil resources produces empty array.
	store := newMockStore()
	h := newTestHandler(store, nil, okExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	resp := decodeJSON(t, rec.Body)
	resources, _ := resp["resources"].([]any)
	if len(resources) != 0 {
		t.Errorf("expected empty array, got %d items", len(resources))
	}
}

func TestHandleCreate_InvalidMultipart(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, okExtractor)

	// Send a non-multipart request.
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/resources", strings.NewReader("not multipart"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreate_DeniedMIMEType(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	// Build a multipart request with a denied MIME type file.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("scope", "global")
	_ = w.WriteField("category", "samples")
	_ = w.WriteField("display_name", "Evil Script")
	_ = w.WriteField("description", "A shell script.")

	// Create a file part with denied MIME type.
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="file"; filename="evil.sh"`)
	partHeader.Set("Content-Type", "application/x-shellscript")
	part, err := w.CreatePart(partHeader)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	_, _ = part.Write([]byte("#!/bin/bash\nrm -rf /"))
	_ = w.Close()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/resources", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for denied MIME type, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestValidateUpdate_AllFields(t *testing.T) {
	name := "Valid Name"
	desc := "A valid description."
	cat := "samples"
	tags := []string{"tag1", "tag2"}
	u := Update{
		DisplayName: &name,
		Description: &desc,
		Category:    &cat,
		Tags:        tags,
	}
	if err := validateUpdate(u); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestValidateUpdate_InvalidDescription(t *testing.T) {
	empty := "   "
	if err := validateUpdate(Update{Description: &empty}); err == nil {
		t.Error("expected error for empty description")
	}
}

func TestHandleCreate_EmptyMIMETypeDefaultsToOctetStream(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	// Build multipart with file that has no Content-Type header.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("scope", "global")
	_ = w.WriteField("category", "samples")
	_ = w.WriteField("display_name", "Binary File")
	_ = w.WriteField("description", "A binary file.")

	// CreateFormFile sets Content-Type to application/octet-stream by default.
	part, err := w.CreateFormFile("file", "data.bin")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = part.Write([]byte{0x00, 0x01, 0x02})
	_ = w.Close()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/resources", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleCreate_FileWithNoContentTypeHeader(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	// Build multipart with file part that has empty Content-Type.
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("scope", "global")
	_ = w.WriteField("category", "samples")
	_ = w.WriteField("display_name", "No MIME")
	_ = w.WriteField("description", "No MIME type.")

	// Create part with explicitly empty Content-Type.
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", `form-data; name="file"; filename="data.dat"`)
	// No Content-Type header set.
	part, err := w.CreatePart(partHeader)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	_, _ = part.Write([]byte("some data"))
	_ = w.Close()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/resources", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetContent_EmptyS3ContentType(t *testing.T) {
	store := newMockStore()
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	seedResource(store, s3, "res-1", ScopeGlobal, "", "user-123")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/content", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	// S3 mock returns empty content type, handler falls back to resource MIMEType.
	ct := rec.Header().Get("Content-Type")
	if ct != "text/csv" {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
}

// personaAdminExtractor is a caller who administers the "finance" persona but
// belongs to it in no other sense: VisibleScopes grants them nothing there.
func personaAdminExtractor(_ *http.Request) (*Claims, error) {
	return &Claims{
		Sub:             "pa-1",
		Email:           "pa@example.com",
		Personas:        []string{"analyst"},
		Roles:           []string{"dp_persona-admin:finance"},
		AdminOfPersonas: []string{"finance"},
	}, nil
}

// The acceptance criterion of the CanAccessResource fix, at the layer that
// actually broke: an admin uploads persona material (CanWriteScope permits it)
// and must then be able to read, download, edit and delete it. Before the fix
// every one of these returned 404, so an admin could create material they could
// neither manage nor remove.
func TestByIDHandlers_AdminReachesPersonaResource(t *testing.T) {
	for _, tc := range []struct {
		name      string
		extractor ClaimsExtractor
	}{
		{"platform admin", okExtractor},
		{"persona admin", personaAdminExtractor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scopeID := "finance"
			store := newMockStore()
			s3 := newMockS3()
			h := newTestHandler(store, s3, tc.extractor)
			seedResource(store, s3, "res-persona", ScopePersona, scopeID, "uploader-sub")

			get := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-persona", http.NoBody)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, get)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET = %d, want 200: %s", rec.Code, rec.Body.String())
			}

			content := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-persona/content", http.NoBody)
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, content)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET content = %d, want 200", rec.Code)
			}

			name := "Renamed"
			body, _ := json.Marshal(Update{DisplayName: &name})
			patch := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/resources/res-persona", bytes.NewReader(body))
			patch.Header.Set("Content-Type", "application/json")
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, patch)
			if rec.Code != http.StatusOK {
				t.Fatalf("PATCH = %d, want 200: %s", rec.Code, rec.Body.String())
			}

			del := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/resources/res-persona", http.NoBody)
			rec = httptest.NewRecorder()
			h.ServeHTTP(rec, del)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("DELETE = %d, want 204: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// A persona admin's reach stops at the persona they administer.
func TestByIDHandlers_PersonaAdminConfinedToTheirPersona(t *testing.T) {
	store := newMockStore()
	h := newTestHandler(store, nil, personaAdminExtractor)
	seedResource(store, nil, "res-other", ScopePersona, "engineering", "uploader-sub")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-other", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET = %d, want 404 for a persona they do not administer", rec.Code)
	}
}
