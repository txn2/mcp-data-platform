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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/thumbtypes"
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
		// AllScopes is every library, whatever Scopes holds -- the same reading
		// the postgres store's visibility clause gives it (#1553). A fake that
		// only understood the scope set would report an unrestricted listing as
		// empty.
		if filter.AllScopes {
			if PathUnder(r.Path, filter.Path) {
				result = append(result, *r)
			}
			continue
		}
		if visibleTo(filter.Scopes, r) && PathUnder(r.Path, filter.Path) {
			result = append(result, *r)
		}
	}
	return result, len(result), nil
}

// Folders derives the ancestor chain of every visible resource's path and
// counts each prefix, which is what the grouped query does (#1555). A fake that
// returned nothing would let a handler test pass over a library with folders.
func (m *mockStore) Folders(_ context.Context, filter Filter) ([]Folder, error) {
	counts := map[string]int{}
	for _, r := range m.resources {
		if !filter.AllScopes && !visibleTo(filter.Scopes, r) {
			continue
		}
		parts := strings.Split(r.Path, "/")
		for i := range parts {
			prefix := strings.Join(parts[:i+1], "/")
			if prefix == "" {
				continue
			}
			counts[prefix]++
		}
	}
	paths := make([]string, 0, len(counts))
	for p := range counts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	folders := make([]Folder, 0, len(paths))
	for _, p := range paths {
		folders = append(folders, Folder{Path: p, Count: counts[p]})
	}
	return folders, nil
}

// Tags collects the distinct tags of the visible resources, which is what the
// rollup does. A fake returning nothing would let a facet test pass over a
// library whose files are tagged.
func (m *mockStore) Tags(_ context.Context, filter Filter) ([]string, error) {
	seen := map[string]bool{}
	for _, r := range m.resources {
		if !filter.AllScopes && !visibleTo(filter.Scopes, r) {
			continue
		}
		for _, t := range r.Tags {
			seen[t] = true
		}
	}
	tags := make([]string, 0, len(seen))
	for t := range seen {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags, nil
}

// SetThumbnail, ClearThumbnail and PendingThumbnails model the capture contract
// (#1554): a capture records a key and a time WITHOUT touching updated_at, and
// pending is a capture that is missing or older than the row it came from. A
// fake that bumped updated_at here would hide the loop the real write is
// written to avoid.
func (m *mockStore) SetThumbnail(_ context.Context, id string, t ThumbnailCapture) error {
	r, ok := m.resources[id]
	if !ok {
		return fmt.Errorf("resource not found: %s", id)
	}
	at := t.CapturedAt
	if t.Variant == ThumbnailVariantDark {
		r.ThumbnailDarkS3Key, r.ThumbnailDarkCapturedAt = t.S3Key, &at
		return nil
	}
	r.ThumbnailS3Key, r.ThumbnailCapturedAt = t.S3Key, &at
	return nil
}

func (m *mockStore) ClearThumbnail(_ context.Context, id, variant string) error {
	r, ok := m.resources[id]
	if !ok {
		return fmt.Errorf("resource not found: %s", id)
	}
	if variant == ThumbnailVariantDark {
		r.ThumbnailDarkS3Key, r.ThumbnailDarkCapturedAt = "", nil
		return nil
	}
	r.ThumbnailS3Key, r.ThumbnailCapturedAt = "", nil
	return nil
}

func (m *mockStore) PendingThumbnails(_ context.Context, filter Filter, limit int) ([]Resource, error) {
	var out []Resource
	for _, r := range m.resources {
		if !filter.AllScopes && !visibleTo(filter.Scopes, r) {
			continue
		}
		if !capturableType(r.MIMEType) || r.SizeBytes > MaxThumbnailSourceBytes {
			continue
		}
		if thumbnailBehind(r, ThumbnailVariantLight) ||
			(themeableType(r.MIMEType) && thumbnailBehind(r, ThumbnailVariantDark)) {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// thumbnailBehind is the per-variant half of the pending rule.
func thumbnailBehind(r *Resource, variant string) bool {
	key, at := r.ThumbnailS3Key, r.ThumbnailCapturedAt
	if variant == ThumbnailVariantDark {
		key, at = r.ThumbnailDarkS3Key, r.ThumbnailDarkCapturedAt
	}
	return key == "" || at == nil || at.Before(r.UpdatedAt)
}

func capturableType(mime string) bool {
	for _, fragment := range thumbtypes.Capturable {
		if strings.Contains(strings.ToLower(mime), fragment) {
			return true
		}
	}
	return false
}

func themeableType(mime string) bool {
	for _, fragment := range thumbtypes.Themeable {
		if strings.Contains(strings.ToLower(mime), fragment) {
			return true
		}
	}
	return false
}

// visibleTo is the scope arm of the fake's List, Folders and Tags, stated once.
func visibleTo(scopes []ScopeFilter, r *Resource) bool {
	for _, sf := range scopes {
		if sf.Scope == r.Scope && (sf.Scope == ScopeGlobal || sf.ScopeID == r.ScopeID) {
			return true
		}
	}
	return false
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
	// Deliberately not the path: the real buildUpdate writes no path column,
	// because refiling a resource in another folder rewrites its URI and takes
	// the Move transaction. A fake that wrote it here would let a handler that
	// never called Move pass.
	return nil
}

// Move models the real store's refile: the four columns that say where each
// resource lives are rewritten, the address it leaves becomes an alias, and the
// address it takes stops being one. GetByURI above resolves a live URI only, so
// the alias map is read there too -- a fake that dropped the alias would let a
// test pass that the real store fails.
//
// The batch is all-or-nothing, like the transaction it stands for: every
// destination is checked against every resource outside the batch before
// anything is written, so a refused folder rename leaves the library untouched
// here exactly as it does in PostgreSQL.
func (m *mockStore) Move(_ context.Context, moves []Move) error {
	moving := make(map[string]bool, len(moves))
	for _, mv := range moves {
		if _, ok := m.resources[mv.ID]; !ok {
			return fmt.Errorf("resource not found: %s", mv.ID)
		}
		moving[mv.ID] = true
	}
	taken := make(map[string]bool, len(moves))
	for _, mv := range moves {
		if taken[mv.URI] {
			return ErrURIConflict
		}
		taken[mv.URI] = true
		for other, res := range m.resources {
			if !moving[other] && res.URI == mv.URI {
				return ErrURIConflict
			}
		}
	}
	if m.aliases == nil {
		m.aliases = map[string]string{}
	}
	for _, mv := range moves {
		r := m.resources[mv.ID]
		if mv.FromURI != "" && mv.FromURI != mv.URI {
			m.aliases[mv.FromURI] = mv.ID
		}
		delete(m.aliases, mv.URI)
		r.Scope, r.ScopeID, r.Path, r.URI = mv.Scope, mv.ScopeID, mv.Path, mv.URI
		r.UpdatedAt = time.Now().UTC()
	}
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

// PutObjectStream models the real client's streaming write: it draws the
// reader to its end, keeps what it read, and reports that count. A fake that
// ignored the reader would let a handler that never streams the body pass,
// which is the whole property the streaming path has to hold (#1631).
func (m *mockS3) PutObjectStream(
	_ context.Context, _, key string, body io.Reader, _ string,
) (int64, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, fmt.Errorf("reading the streamed body: %w", err)
	}
	m.objects[key] = data
	return int64(len(data)), nil
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
		Path:          "samples",
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
		"path":         "samples",
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
	if resp["path"] != "samples" {
		t.Errorf("category = %v", resp["path"])
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
		"path":         "samples",
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
		"path":         "samples",
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
				"path":        "samples",
				"description": "A sample.",
			},
		},
		{
			name: "missing description",
			fields: map[string]string{
				"scope":        "global",
				"path":         "samples",
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
				"path":         "samples",
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
		"path":         "samples",
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

// The listing predicate the route runs under (#1553). What the mock store
// returns is not the point -- the filter it was handed is, since that is what a
// real store's visibility clause is built from.
func TestHandleList_ScopePredicate(t *testing.T) {
	tests := []struct {
		name      string
		extractor ClaimsExtractor
		query     string
		wantAll   bool
		wantScope []ScopeFilter
	}{
		{
			name:      "an administrator's unfiltered listing spans every library",
			extractor: okExtractor, query: "", wantAll: true,
		},
		{
			name:      "an ordinary caller's unfiltered listing is their own scopes",
			extractor: memberExtractor, query: "",
			wantScope: VisibleScopes(*memberClaims()),
		},
		{
			name:      "an administrator lists a persona they do not belong to",
			extractor: okExtractor, query: "?scope=persona&scope_id=finance",
			wantScope: []ScopeFilter{{Scope: ScopePersona, ScopeID: "finance"}},
		},
		{
			name:      "an ordinary caller naming that persona lists nothing",
			extractor: memberExtractor, query: "?scope=persona&scope_id=finance",
			wantScope: nil,
		},
		{
			name:      "an ordinary caller lists a persona they belong to",
			extractor: memberExtractor, query: "?scope=persona&scope_id=analyst",
			wantScope: []ScopeFilter{{Scope: ScopePersona, ScopeID: "analyst"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMockStore()
			h := newTestHandler(store, nil, tt.extractor)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources"+tt.query, http.NoBody)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			got := store.lastListFilter
			if got.AllScopes != tt.wantAll {
				t.Errorf("AllScopes = %v, want %v", got.AllScopes, tt.wantAll)
			}
			if tt.wantAll {
				return
			}
			if len(got.Scopes) != len(tt.wantScope) {
				t.Fatalf("Scopes = %v, want %v", got.Scopes, tt.wantScope)
			}
			for i, want := range tt.wantScope {
				if got.Scopes[i] != want {
					t.Errorf("Scopes[%d] = %v, want %v", i, got.Scopes[i], want)
				}
			}
		})
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
	if err := validateUpdate(Update{Path: &badCat}); err == nil {
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
		"path":         "samples",
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
				Path:          "samples",
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
		"path":         "samples",
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

// PutObjectStream draws the body first, the way the transfer manager does,
// then refuses: a fake that failed without reading would leave the request
// body unconsumed and would not be the failure the route sees.
func (*errPutS3) PutObjectStream(
	_ context.Context, _, _ string, body io.Reader, _ string,
) (int64, error) {
	_, _ = io.Copy(io.Discard, body)
	return 0, errors.New("s3 put error")
}

func TestHandleCreate_S3PutError(t *testing.T) {
	store := newMockStore()
	s3err := &errPutS3{}
	deps := Deps{Store: store, S3Client: s3err, S3Bucket: "test-bucket", URIScheme: "mcp"}
	h := NewHandler(deps, okExtractor, nil)

	fields := map[string]string{
		"scope":        "global",
		"path":         "samples",
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
		"path":         "samples",
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
		"path":         "samples",
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
		"path":         "samples",
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
	_ = w.WriteField("path", "samples")
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
		Path:        &cat,
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
	_ = w.WriteField("path", "samples")
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
	_ = w.WriteField("path", "samples")
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

// --- The facets a library's controls are drawn from (#1555) ---

// folderCounts reads the facets envelope's folders into path -> count. It reads
// every field with the comma-ok form: a shape that is not what the route
// promises is a failed assertion here rather than a panic in the middle of the
// case that was meant to explain it.
func folderCounts(t *testing.T, body []byte) map[string]float64 {
	t.Helper()
	var env struct {
		Folders []struct {
			Path  string  `json:"path"`
			Count float64 `json:"count"`
		} `json:"folders"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decoding the facets envelope: %v\n%s", err, body)
	}
	counts := make(map[string]float64, len(env.Folders))
	for _, f := range env.Folders {
		counts[f.Path] = f.Count
	}
	return counts
}

// stringsField reads one array-of-strings field out of a JSON envelope, leaving
// the envelope's other fields undecoded: they have shapes of their own and are
// not what this is being asked about.
func stringsField(t *testing.T, body []byte, field string) []string {
	t.Helper()
	var env map[string]json.RawMessage
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decoding the envelope: %v\n%s", err, body)
	}
	raw, ok := env[field]
	if !ok {
		t.Fatalf("the envelope carries no %q: %s", field, body)
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding %s: %v\n%s", field, err, raw)
	}
	return out
}

// seedIn files a resource in one library at one path, with tags. The library is
// one value rather than a scope and an id side by side, which is how every
// other rule in this package names one.
func seedIn(store *mockStore, id, path string, lib ScopeFilter, tags ...string) {
	store.resources[id] = &Resource{
		ID: id, Scope: lib.Scope, ScopeID: lib.ScopeID, Path: path,
		Filename: id + ".md", DisplayName: id, MIMEType: "text/markdown", Tags: tags,
	}
}

func TestHandleFacets_FoldersCountEveryDepth(t *testing.T) {
	store := newMockStore()
	seedIn(store, "a", "data", ScopeFilter{Scope: ScopeGlobal})
	seedIn(store, "b", "data/media-manager", ScopeFilter{Scope: ScopeGlobal})
	seedIn(store, "c", "data/media-manager/shows", ScopeFilter{Scope: ScopeGlobal})
	seedIn(store, "d", "other", ScopeFilter{Scope: ScopeGlobal})
	h := newTestHandler(store, nil, okExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/facets", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	counts := folderCounts(t, rec.Body.Bytes())
	// Everything beneath, at every depth: three files are under "data".
	for path, want := range map[string]float64{
		"data":                     3,
		"data/media-manager":       2,
		"data/media-manager/shows": 1,
		"other":                    1,
	} {
		if counts[path] != want {
			t.Errorf("count for %q = %v, want %v (all: %v)", path, counts[path], want, counts)
		}
	}
}

func TestHandleFacets_TagsAreTheLibrarys(t *testing.T) {
	store := newMockStore()
	seedIn(store, "a", "data", ScopeFilter{Scope: ScopeGlobal}, "finance", "q3")
	seedIn(store, "b", "data", ScopeFilter{Scope: ScopeGlobal}, "finance")
	seedIn(store, "c", "other", ScopeFilter{Scope: ScopeGlobal})
	h := newTestHandler(store, nil, okExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/facets", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	got := stringsField(t, rec.Body.Bytes(), "tags")
	// Each tag once, in order, whatever how many files carry it.
	if len(got) != 2 || got[0] != "finance" || got[1] != "q3" {
		t.Errorf("tags = %v, want [finance q3]", got)
	}
}

// The facets run under the listing's own visibility rule, so an administrator
// reaches a persona they are not in and an ordinary caller does not (#1553).
func TestHandleFacets_FollowsTheListingAuthority(t *testing.T) {
	tests := []struct {
		name      string
		extractor ClaimsExtractor
		wantEmpty bool
	}{
		{"an administrator reaches a persona they are not in", okExtractor, false},
		{"an ordinary caller does not", memberExtractor, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMockStore()
			seedIn(store, "a", "data", ScopeFilter{Scope: ScopePersona, ScopeID: "finance"}, "q3")
			h := newTestHandler(store, nil, tt.extractor)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
				"/api/v1/resources/facets?scope=persona&scope_id=finance", http.NoBody)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
			}
			body := decodeJSON(t, rec.Body)
			list, _ := body["folders"].([]any)
			empty := len(list) == 0
			if empty != tt.wantEmpty {
				t.Errorf("folders empty = %v, want %v: %v", empty, tt.wantEmpty, body)
			}
		})
	}
}

// A rollup that fails is reported, not answered with a half-drawn library: a
// tree missing folders reads as a library missing files.
func TestHandleFacets_ReportsAFailedRollup(t *testing.T) {
	for _, tt := range []struct {
		name  string
		store Store
	}{
		{"folders", failingFolders{newMockStore()}},
		{"tags", failingTags{newMockStore()}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler(Deps{Store: tt.store, URIScheme: "mcp"}, okExtractor, nil)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/facets", http.NoBody)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// failingFolders and failingTags are the mock store with one rollup broken.
type failingFolders struct{ *mockStore }

func (failingFolders) Folders(_ context.Context, _ Filter) ([]Folder, error) {
	return nil, errors.New("rollup failed")
}

type failingTags struct{ *mockStore }

func (failingTags) Tags(_ context.Context, _ Filter) ([]string, error) {
	return nil, errors.New("rollup failed")
}

// A caller with no identity is refused before any rollup runs.
func TestHandleFacets_RefusesAnUnauthenticatedCaller(t *testing.T) {
	h := newTestHandler(newMockStore(), nil, failExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/facets", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

// An envelope is never null: a page that has to test for it before iterating is
// a page that will eventually forget.
func TestHandleFacets_EmptyLibraryAnswersWithArrays(t *testing.T) {
	h := newTestHandler(newMockStore(), nil, okExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/facets", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := decodeJSON(t, rec.Body)
	if _, ok := body["folders"].([]any); !ok {
		t.Errorf("folders = %v, want an array", body["folders"])
	}
	if _, ok := body["tags"].([]any); !ok {
		t.Errorf("tags = %v, want an array", body["tags"])
	}
}
