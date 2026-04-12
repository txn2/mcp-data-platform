package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// --- mock store for middleware tests ---

type mockResourceStore struct {
	resources map[string]*resource.Resource
}

func newMockResourceStore() *mockResourceStore {
	return &mockResourceStore{resources: make(map[string]*resource.Resource)}
}

func (m *mockResourceStore) List(_ context.Context, filter resource.Filter) ([]resource.Resource, int, error) {
	var result []resource.Resource
	for _, r := range m.resources {
		for _, sf := range filter.Scopes {
			if sf.Scope == r.Scope && (sf.Scope == resource.ScopeGlobal || sf.ScopeID == r.ScopeID) {
				result = append(result, *r)
				break
			}
		}
	}
	return result, len(result), nil
}

func (m *mockResourceStore) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	for _, r := range m.resources {
		if r.URI == uri {
			return r, nil
		}
	}
	return nil, fmt.Errorf("not found")
}

// --- mock S3 blob reader ---

type mockBlobReader struct {
	objects map[string][]byte
}

func newMockBlobReader() *mockBlobReader {
	return &mockBlobReader{objects: make(map[string][]byte)}
}

func (m *mockBlobReader) GetObject(_ context.Context, _, key string) (body []byte, ct string, err error) {
	data, ok := m.objects[key]
	if !ok {
		return nil, "", fmt.Errorf("not found")
	}
	return data, "text/plain", nil
}

func TestIsTextMIME(t *testing.T) {
	tests := []struct {
		mime string
		want bool
	}{
		{"text/plain", true},
		{"text/csv", true},
		{"text/html", true},
		{"application/json", true},
		{"application/xml", true},
		{"application/yaml", true},
		{"application/sql", true},
		{"image/png", false},
		{"application/pdf", false},
		{"application/octet-stream", false},
	}
	for _, tt := range tests {
		got := isTextMIME(tt.mime)
		if got != tt.want {
			t.Errorf("isTextMIME(%q) = %v, want %v", tt.mime, got, tt.want)
		}
	}
}

func TestExtractResourceURI(t *testing.T) {
	// Build a mock request matching resources/read format.
	reqData := map[string]any{
		"params": map[string]any{
			"uri": "mcp://global/samples/test.csv",
		},
	}
	raw, _ := json.Marshal(reqData)
	var req mcp.ReadResourceRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	uri, err := extractResourceURI(&req)
	if err != nil {
		t.Fatalf("extractResourceURI error: %v", err)
	}
	if uri != "mcp://global/samples/test.csv" {
		t.Errorf("got %q, want %q", uri, "mcp://global/samples/test.csv")
	}
}

func TestScopesFromPlatformContext(t *testing.T) {
	pc := &PlatformContext{
		UserID:      "user-1",
		Roles:       []string{"analyst"},
		PersonaName: "analyst",
	}

	cfg := ManagedResourceConfig{
		PersonasForRoles: func(_ []string) []string {
			return []string{"analyst", "viewer"}
		},
	}
	scopes := scopesFromPlatformContext(pc, cfg)

	// Should have: global + user/user-1 + persona/analyst + persona/viewer = 4
	if len(scopes) != 4 {
		t.Errorf("expected 4 scopes, got %d: %v", len(scopes), scopes)
	}
}

func TestClaimsFromPC(t *testing.T) {
	pc := &PlatformContext{
		UserID:      "u1",
		Roles:       []string{"admin"},
		PersonaName: "admin",
	}

	// Without PersonasForRoles, falls back to PersonaName
	cfg := ManagedResourceConfig{}
	claims := claimsFromPC(pc, cfg)
	if claims.Sub != "u1" {
		t.Errorf("Sub = %q, want %q", claims.Sub, "u1")
	}
	if len(claims.Personas) != 1 || claims.Personas[0] != "admin" {
		t.Errorf("Personas = %v, want [admin]", claims.Personas)
	}

	// With PersonasForRoles
	cfg.PersonasForRoles = func(_ []string) []string { return []string{"a", "b"} }
	claims = claimsFromPC(pc, cfg)
	if len(claims.Personas) != 2 {
		t.Errorf("Personas = %v, want [a b]", claims.Personas)
	}

	// IsAdmin propagated from PlatformContext.
	pc.IsAdmin = true
	cfg.PersonasForRoles = nil
	claims = claimsFromPC(pc, cfg)
	if !claims.IsAdmin {
		t.Error("expected IsAdmin=true when PlatformContext.IsAdmin is true")
	}

	// IsAdmin=false propagated.
	pc.IsAdmin = false
	claims = claimsFromPC(pc, cfg)
	if claims.IsAdmin {
		t.Error("expected IsAdmin=false when PlatformContext.IsAdmin is false")
	}

	// AdminOfPersonas extracted from prefixed roles.
	pc.Roles = []string{"dp_persona-admin:finance", "dp_analyst"}
	claims = claimsFromPC(pc, cfg)
	if len(claims.AdminOfPersonas) != 1 || claims.AdminOfPersonas[0] != "finance" {
		t.Errorf("AdminOfPersonas = %v, want [finance]", claims.AdminOfPersonas)
	}
}

// --- handleManagedList tests ---

func TestMCPManagedResourceMiddleware_ListAppendsManaged(t *testing.T) {
	store := newMockResourceStore()
	store.resources["r1"] = &resource.Resource{
		ID:          "r1",
		Scope:       resource.ScopeGlobal,
		URI:         "mcp://global/samples/test.csv",
		DisplayName: "Test CSV",
		Description: "A test CSV file.",
		MIMEType:    "text/csv",
	}

	cfg := ManagedResourceConfig{
		Store:     store,
		URIScheme: "mcp",
	}

	// Simulate the static handler returning one static resource.
	staticResource := &mcp.Resource{
		URI:  "file:///static.txt",
		Name: "Static",
	}
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.ListResourcesResult{
			Resources: []*mcp.Resource{staticResource},
		}, nil
	}

	mw := MCPManagedResourceMiddleware(cfg)
	handler := mw(next)

	// Use a context with PlatformContext (required for managed resource injection).
	pc := &PlatformContext{UserID: "test-user", Roles: []string{"analyst"}}
	ctx := WithPlatformContext(context.Background(), pc)
	result, err := handler(ctx, methodListResources, &mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	listResult, ok := result.(*mcp.ListResourcesResult)
	if !ok {
		t.Fatalf("result type = %T, want *mcp.ListResourcesResult", result)
	}

	// Should have 1 static + 1 managed = 2 resources.
	if len(listResult.Resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(listResult.Resources))
		for i, r := range listResult.Resources {
			t.Logf("  [%d] URI=%s Name=%s", i, r.URI, r.Name)
		}
	}

	// Second resource should be the managed one.
	if len(listResult.Resources) >= 2 {
		managed := listResult.Resources[1]
		if managed.URI != "mcp://global/samples/test.csv" {
			t.Errorf("managed URI = %q", managed.URI)
		}
		if managed.Name != "Test CSV" {
			t.Errorf("managed Name = %q", managed.Name)
		}
	}
}

func TestMCPManagedResourceMiddleware_ListNoManaged(t *testing.T) {
	store := newMockResourceStore() // empty store

	cfg := ManagedResourceConfig{
		Store:     store,
		URIScheme: "mcp",
	}

	staticResource := &mcp.Resource{URI: "file:///static.txt", Name: "Static"}
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.ListResourcesResult{
			Resources: []*mcp.Resource{staticResource},
		}, nil
	}

	mw := MCPManagedResourceMiddleware(cfg)
	handler := mw(next)

	result, err := handler(context.Background(), methodListResources, &mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	listResult, _ := result.(*mcp.ListResourcesResult)
	if len(listResult.Resources) != 1 {
		t.Errorf("expected 1 resource (static only), got %d", len(listResult.Resources))
	}
}

// --- handleManagedRead tests ---

func makeReadRequest(t *testing.T, uri string) *mcp.ReadResourceRequest {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{
		"params": map[string]any{"uri": uri},
	})
	var req mcp.ReadResourceRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal read request: %v", err)
	}
	return &req
}

func TestMCPManagedResourceMiddleware_ReadManaged(t *testing.T) {
	store := newMockResourceStore()
	blob := newMockBlobReader()

	store.resources["r1"] = &resource.Resource{
		ID:          "r1",
		Scope:       resource.ScopeGlobal,
		URI:         "mcp://global/samples/test.csv",
		DisplayName: "Test CSV",
		MIMEType:    "text/csv",
		S3Key:       "resources/global/r1/test.csv",
	}
	blob.objects["resources/global/r1/test.csv"] = []byte("col1,col2\na,b")

	cfg := ManagedResourceConfig{
		Store:     store,
		S3Client:  blob,
		S3Bucket:  "test-bucket",
		URIScheme: "mcp",
	}

	nextCalled := false
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		nextCalled = true
		return nil, fmt.Errorf("should not reach next")
	}

	mw := MCPManagedResourceMiddleware(cfg)
	handler := mw(next)

	// Create context with PlatformContext (needed for permission check).
	pc := &PlatformContext{UserID: "user-1", Roles: []string{"admin"}}
	ctx := WithPlatformContext(context.Background(), pc)

	req := makeReadRequest(t, "mcp://global/samples/test.csv")
	result, err := handler(ctx, methodReadResource, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if nextCalled {
		t.Error("next handler should not have been called for managed resource")
	}

	readResult, ok := result.(*mcp.ReadResourceResult)
	if !ok {
		t.Fatalf("result type = %T, want *mcp.ReadResourceResult", result)
	}

	if len(readResult.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(readResult.Contents))
	}
	content := readResult.Contents[0]
	if content.URI != "mcp://global/samples/test.csv" {
		t.Errorf("URI = %q", content.URI)
	}
	if content.Text != "col1,col2\na,b" {
		t.Errorf("Text = %q", content.Text)
	}
}

func TestMCPManagedResourceMiddleware_ReadFallsThrough(t *testing.T) {
	store := newMockResourceStore()

	cfg := ManagedResourceConfig{
		Store:     store,
		URIScheme: "mcp",
	}

	nextCalled := false
	expectedResult := &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:  "file:///some/file.txt",
			Text: "static content",
		}},
	}
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		nextCalled = true
		return expectedResult, nil
	}

	mw := MCPManagedResourceMiddleware(cfg)
	handler := mw(next)

	// URI with a different scheme should fall through.
	req := makeReadRequest(t, "file:///some/file.txt")
	result, err := handler(context.Background(), methodReadResource, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !nextCalled {
		t.Error("next handler should have been called for non-managed URI")
	}

	readResult, ok := result.(*mcp.ReadResourceResult)
	if !ok {
		t.Fatalf("result type = %T", result)
	}
	if len(readResult.Contents) != 1 || readResult.Contents[0].Text != "static content" {
		t.Errorf("unexpected result: %v", readResult)
	}
}

func TestMCPManagedResourceMiddleware_ReadURINotInStore(t *testing.T) {
	store := newMockResourceStore() // empty

	cfg := ManagedResourceConfig{
		Store:     store,
		URIScheme: "mcp",
	}

	nextCalled := false
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		nextCalled = true
		return &mcp.ReadResourceResult{}, nil
	}

	mw := MCPManagedResourceMiddleware(cfg)
	handler := mw(next)

	// URI matches scheme but not in store -- falls through to next.
	req := makeReadRequest(t, "mcp://global/nonexistent/file.txt")
	_, err := handler(context.Background(), methodReadResource, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !nextCalled {
		t.Error("next handler should have been called when URI not in store")
	}
}

// --- fetchResourceContent tests ---

func TestFetchResourceContent_TextInline(t *testing.T) {
	blob := newMockBlobReader()
	blob.objects["resources/global/r1/test.csv"] = []byte("col1,col2\na,b")

	cfg := ManagedResourceConfig{
		S3Client: blob,
		S3Bucket: "test-bucket",
	}
	res := &resource.Resource{
		URI:      "mcp://global/samples/test.csv",
		MIMEType: "text/csv",
		S3Key:    "resources/global/r1/test.csv",
	}

	result, err := fetchResourceContent(context.Background(), cfg, res)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(result.Contents))
	}
	c := result.Contents[0]
	if c.Text != "col1,col2\na,b" {
		t.Errorf("Text = %q", c.Text)
	}
	if c.Blob != nil {
		t.Error("expected nil Blob for text content")
	}
	if c.MIMEType != "text/csv" {
		t.Errorf("MIMEType = %q", c.MIMEType)
	}
}

func TestFetchResourceContent_BinaryBlob(t *testing.T) {
	blob := newMockBlobReader()
	binaryData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A} // PNG header
	blob.objects["resources/global/r1/image.png"] = binaryData

	cfg := ManagedResourceConfig{
		S3Client: blob,
		S3Bucket: "test-bucket",
	}
	res := &resource.Resource{
		URI:      "mcp://global/samples/image.png",
		MIMEType: "image/png",
		S3Key:    "resources/global/r1/image.png",
	}

	result, err := fetchResourceContent(context.Background(), cfg, res)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(result.Contents))
	}
	c := result.Contents[0]
	if c.Text != "" {
		t.Errorf("expected empty Text for binary, got %q", c.Text)
	}
	if len(c.Blob) != len(binaryData) {
		t.Errorf("Blob length = %d, want %d", len(c.Blob), len(binaryData))
	}
}

func TestFetchResourceContent_NoS3(t *testing.T) {
	cfg := ManagedResourceConfig{
		S3Client: nil, // no S3 configured
	}
	res := &resource.Resource{
		URI:      "mcp://global/samples/test.csv",
		MIMEType: "text/csv",
		S3Key:    "resources/global/r1/test.csv",
	}

	result, err := fetchResourceContent(context.Background(), cfg, res)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(result.Contents))
	}
	c := result.Contents[0]
	if c.Text != "(blob storage not configured)" {
		t.Errorf("Text = %q, want placeholder", c.Text)
	}
}

func TestFetchResourceContent_S3Error(t *testing.T) {
	blob := newMockBlobReader() // empty, so all gets fail

	cfg := ManagedResourceConfig{
		S3Client: blob,
		S3Bucket: "test-bucket",
	}
	res := &resource.Resource{
		URI:      "mcp://global/samples/test.csv",
		MIMEType: "text/csv",
		S3Key:    "resources/global/r1/missing.csv",
	}

	_, err := fetchResourceContent(context.Background(), cfg, res)
	if err == nil {
		t.Fatal("expected error for missing S3 object")
	}
}

// --- handleManagedList error path ---

func TestMCPManagedResourceMiddleware_ListNextError(t *testing.T) {
	store := newMockResourceStore()
	cfg := ManagedResourceConfig{Store: store, URIScheme: "mcp"}

	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, fmt.Errorf("next handler failed")
	}

	mw := MCPManagedResourceMiddleware(cfg)
	handler := mw(next)

	_, err := handler(context.Background(), methodListResources, &mcp.ListResourcesRequest{})
	if err == nil {
		t.Fatal("expected error from next handler")
	}
}

func TestMCPManagedResourceMiddleware_ListResultNotListType(t *testing.T) {
	store := newMockResourceStore()
	store.resources["r1"] = &resource.Resource{
		ID:    "r1",
		Scope: resource.ScopeGlobal,
		URI:   "mcp://global/samples/test.csv",
	}
	cfg := ManagedResourceConfig{Store: store, URIScheme: "mcp"}

	// Return a non-ListResourcesResult type.
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.ReadResourceResult{}, nil
	}

	mw := MCPManagedResourceMiddleware(cfg)
	handler := mw(next)

	result, err := handler(context.Background(), methodListResources, &mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return as-is since type assertion fails.
	if _, ok := result.(*mcp.ReadResourceResult); !ok {
		t.Errorf("expected ReadResourceResult passthrough, got %T", result)
	}
}

func TestExtractResourceURI_EmptyParams(t *testing.T) {
	// A request with no params/uri field should return empty string.
	raw, _ := json.Marshal(map[string]any{"params": map[string]any{}})
	var req mcp.ReadResourceRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	uri, err := extractResourceURI(&req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uri != "" {
		t.Errorf("expected empty URI, got %q", uri)
	}
}

// --- middleware passthrough test ---

func TestMCPManagedResourceMiddleware_NonResourceMethod(t *testing.T) {
	store := newMockResourceStore()
	cfg := ManagedResourceConfig{Store: store}

	nextCalled := false
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		nextCalled = true
		return &mcp.ListResourcesResult{}, nil
	}

	mw := MCPManagedResourceMiddleware(cfg)
	handler := mw(next)

	// A non-resource method should pass through directly.
	_, err := handler(context.Background(), "tools/call", &mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !nextCalled {
		t.Error("next should have been called for non-resource method")
	}
}
