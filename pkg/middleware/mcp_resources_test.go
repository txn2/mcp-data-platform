package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// --- mock store for middleware tests ---

type mockResourceStore struct {
	resources map[string]*resource.Resource
	deleted   []string // ids passed to Delete (records orphan prunes)
}

func newMockResourceStore() *mockResourceStore {
	return &mockResourceStore{resources: make(map[string]*resource.Resource)}
}

// Delete records the id and removes the row, so mockResourceStore satisfies the
// optional resourcePruner capability the read path uses to prune orphans.
func (m *mockResourceStore) Delete(_ context.Context, id string) error {
	m.deleted = append(m.deleted, id)
	delete(m.resources, id)
	return nil
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
	getErr  error // when set, GetObject returns this regardless of objects
}

func newMockBlobReader() *mockBlobReader {
	return &mockBlobReader{objects: make(map[string][]byte)}
}

func (m *mockBlobReader) GetObject(_ context.Context, _, key string) (body []byte, ct string, err error) {
	if m.getErr != nil {
		return nil, "", m.getErr
	}
	data, ok := m.objects[key]
	if !ok {
		return nil, "", fmt.Errorf("s3 get: NoSuchKey: the specified key does not exist; status code: 404")
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

func TestExtractResourceURI_NilRequest(t *testing.T) {
	_, err := extractResourceURI(nil)
	if err == nil {
		t.Error("expected error for nil request")
	}
}

func TestExtractResourceURI_WrongParamsType(t *testing.T) {
	req := &mcp.ServerRequest[*mcp.ListResourcesParams]{Params: &mcp.ListResourcesParams{}}
	_, err := extractResourceURI(req)
	if err == nil {
		t.Error("expected error for wrong params type")
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

func TestGetOrAuthenticatePC(t *testing.T) {
	t.Run("returns existing PlatformContext", func(t *testing.T) {
		existing := &PlatformContext{UserID: "existing"}
		ctx := context.WithValue(context.Background(), platformContextKey, existing)
		got := getOrAuthenticatePC(ctx, nil, nil, nil, "")
		if got != existing {
			t.Error("should return existing PlatformContext")
		}
	})

	t.Run("authenticates when no PlatformContext", func(t *testing.T) {
		auth := &mockManagedAuth{
			user: &UserInfo{UserID: "u1", Email: "u1@example.com", Roles: []string{"dp_admin"}},
		}
		cfg := ManagedResourceConfig{
			Authenticator:    auth,
			AdminPersona:     "admin",
			PersonasForRoles: func(_ []string) []string { return []string{"admin"} },
		}
		// Pass a request so bridgeAuthToken is exercised.
		req := &mcp.ServerRequest[*mcp.ReadResourceParams]{
			Params: &mcp.ReadResourceParams{URI: "mcp://global/test/file.txt"},
		}
		got := getOrAuthenticatePC(context.Background(), req, cfg.Authenticator, cfg.PersonasForRoles, cfg.AdminPersona)
		if got == nil {
			t.Fatal("expected non-nil PlatformContext")
		}
		if got.UserID != "u1" {
			t.Errorf("UserID = %q, want u1", got.UserID)
		}
		if !got.IsAdmin {
			t.Error("expected IsAdmin=true for admin persona")
		}
	})

	t.Run("returns nil when auth fails", func(t *testing.T) {
		auth := &mockManagedAuth{err: fmt.Errorf("unauthorized")}
		cfg := ManagedResourceConfig{Authenticator: auth}
		got := getOrAuthenticatePC(context.Background(), nil, cfg.Authenticator, cfg.PersonasForRoles, cfg.AdminPersona)
		if got != nil {
			t.Error("expected nil when auth fails")
		}
	})

	t.Run("returns nil when no authenticator", func(t *testing.T) {
		got := getOrAuthenticatePC(context.Background(), nil, nil, nil, "")
		if got != nil {
			t.Error("expected nil when no authenticator configured")
		}
	})

	t.Run("non-admin persona does not set IsAdmin", func(t *testing.T) {
		auth := &mockManagedAuth{
			user: &UserInfo{UserID: "u2", Roles: []string{"dp_analyst"}},
		}
		cfg := ManagedResourceConfig{
			Authenticator:    auth,
			AdminPersona:     "admin",
			PersonasForRoles: func(_ []string) []string { return []string{"analyst"} },
		}
		got := getOrAuthenticatePC(context.Background(), nil, cfg.Authenticator, cfg.PersonasForRoles, cfg.AdminPersona)
		if got == nil {
			t.Fatal("expected non-nil PlatformContext")
		}
		if got.IsAdmin {
			t.Error("expected IsAdmin=false for non-admin persona")
		}
		if got.PersonaName != "analyst" {
			t.Errorf("PersonaName = %q, want analyst", got.PersonaName)
		}
	})
}

// mockManagedAuth is a minimal Authenticator for resource middleware tests.
type mockManagedAuth struct {
	user *UserInfo
	err  error
}

func (m *mockManagedAuth) Authenticate(_ context.Context) (*UserInfo, error) {
	return m.user, m.err
}

// --- handleManagedList tests ---

func TestMCPManagedResourceMiddleware_ListFiltersByScope(t *testing.T) {
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

	// SDK list includes both static and managed resources (managed registered via AddResource).
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.ListResourcesResult{
			Resources: []*mcp.Resource{
				{URI: "file:///static.txt", Name: "Static"},
				{URI: "mcp://global/samples/test.csv", Name: "Test CSV"},
			},
		}, nil
	}

	mw := MCPManagedResourceMiddleware(cfg)
	handler := mw(next)

	// Authenticated user sees global resources.
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

	// Should have 1 static + 1 managed = 2 resources (global is visible to all).
	if len(listResult.Resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(listResult.Resources))
		for i, r := range listResult.Resources {
			t.Logf("  [%d] URI=%s Name=%s", i, r.URI, r.Name)
		}
	}
}

// TestMCPManagedResourceMiddleware_ListAuthenticatesFallback verifies the
// production path where resources/list is called WITHOUT a pre-set
// PlatformContext. The middleware must authenticate the user directly
// via the configured Authenticator and return managed resources.
func TestMCPManagedResourceMiddleware_ListAuthenticatesFallback(t *testing.T) {
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
		Authenticator: &mockManagedAuth{
			user: &UserInfo{UserID: "u1", Email: "u1@example.com", Roles: []string{"dp_admin"}},
		},
		AdminPersona:     "admin",
		PersonasForRoles: func(_ []string) []string { return []string{"admin"} },
	}

	// SDK list includes both static and managed (registered via AddResource).
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.ListResourcesResult{Resources: []*mcp.Resource{
			{URI: "file:///static.txt", Name: "Static"},
			{URI: "mcp://global/samples/test.csv", Name: "Test CSV"},
		}}, nil
	}

	mw := MCPManagedResourceMiddleware(cfg)
	handler := mw(next)

	// NO PlatformContext in context — forces the authentication fallback.
	req := &mcp.ServerRequest[*mcp.ListResourcesParams]{Params: &mcp.ListResourcesParams{}}
	result, err := handler(context.Background(), methodListResources, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	listResult, ok := result.(*mcp.ListResourcesResult)
	if !ok {
		t.Fatalf("result type = %T, want *mcp.ListResourcesResult", result)
	}

	// Admin sees both static and managed (global visible to all).
	if len(listResult.Resources) != 2 {
		t.Errorf("expected 2 resources (1 static + 1 managed), got %d", len(listResult.Resources))
		for i, r := range listResult.Resources {
			t.Logf("  [%d] URI=%s Name=%s", i, r.URI, r.Name)
		}
	}
}

// TestMCPManagedResourceMiddleware_ListNoAuthReturnsStaticOnly verifies that
// when no Authenticator is configured and no PlatformContext exists, the
// middleware returns only static resources without error.
func TestMCPManagedResourceMiddleware_ListNoAuthReturnsStaticOnly(t *testing.T) {
	store := newMockResourceStore()
	store.resources["r1"] = &resource.Resource{
		ID:    "r1",
		Scope: resource.ScopeGlobal,
		URI:   "mcp://global/samples/test.csv",
	}

	cfg := ManagedResourceConfig{
		Store:     store,
		URIScheme: "mcp",
		// No Authenticator — simulates stdio transport without auth.
	}

	// SDK list includes both static and managed (registered via AddResource).
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.ListResourcesResult{Resources: []*mcp.Resource{
			{URI: "file:///static.txt", Name: "Static"},
			{URI: "mcp://global/samples/test.csv", Name: "Test CSV"},
		}}, nil
	}

	mw := MCPManagedResourceMiddleware(cfg)
	handler := mw(next)

	// No PlatformContext, no Authenticator — managed resources filtered out.
	result, err := handler(context.Background(), methodListResources, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	listResult, ok := result.(*mcp.ListResourcesResult)
	if !ok {
		t.Fatalf("result type = %T, want *mcp.ListResourcesResult", result)
	}

	// Should have only the static resource — managed resources filtered out without auth.
	if len(listResult.Resources) != 1 {
		t.Errorf("expected 1 static resource, got %d", len(listResult.Resources))
	}
	if len(listResult.Resources) > 0 && listResult.Resources[0].URI != "file:///static.txt" {
		t.Errorf("expected static resource, got %s", listResult.Resources[0].URI)
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

func TestMCPManagedResourceMiddleware_ListStoreErrorRemovesManaged(t *testing.T) {
	errStore := &errorResourceStore{}
	cfg := ManagedResourceConfig{
		Store:     errStore,
		URIScheme: "mcp",
	}

	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return &mcp.ListResourcesResult{Resources: []*mcp.Resource{
			{URI: "file:///static.txt", Name: "Static"},
			{URI: "mcp://global/test/file.txt", Name: "Managed"},
		}}, nil
	}

	mw := MCPManagedResourceMiddleware(cfg)
	handler := mw(next)

	// Authenticated but store fails — managed resources removed for safety.
	pc := &PlatformContext{UserID: "u1", Roles: []string{"admin"}}
	ctx := WithPlatformContext(context.Background(), pc)
	result, err := handler(ctx, methodListResources, &mcp.ListResourcesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	listResult, ok := result.(*mcp.ListResourcesResult)
	if !ok {
		t.Fatal("expected *ListResourcesResult")
	}
	if len(listResult.Resources) != 1 {
		t.Errorf("expected 1 static resource after store error, got %d", len(listResult.Resources))
	}
}

// errorResourceStore always returns an error for List.
type errorResourceStore struct{}

func (*errorResourceStore) List(_ context.Context, _ resource.Filter) ([]resource.Resource, int, error) {
	return nil, 0, fmt.Errorf("database error")
}

func (*errorResourceStore) GetByURI(_ context.Context, _ string) (*resource.Resource, error) {
	return nil, fmt.Errorf("not found")
}

func TestManagedURIPrefix_Default(t *testing.T) {
	cfg := ManagedResourceConfig{} // no URIScheme set
	if got := managedURIPrefix(cfg); got != "mcp://" {
		t.Errorf("managedURIPrefix() = %q, want mcp://", got)
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

// TestMCPManagedResourceMiddleware_ReadOrphaned_Prunes is the #576 self-heal:
// reading a managed resource whose backing object is gone returns an actionable
// "missing/orphaned" error AND prunes the dead row, so it stops appearing in
// resources/list.
func TestMCPManagedResourceMiddleware_ReadOrphaned_Prunes(t *testing.T) {
	store := newMockResourceStore()
	blob := newMockBlobReader() // empty -> GetObject returns NoSuchKey/404

	store.resources["r1"] = &resource.Resource{
		ID:       "r1",
		Scope:    resource.ScopeGlobal,
		URI:      "mcp://global/samples/orphan.csv",
		MIMEType: "text/csv",
		S3Key:    "resources/global/r1/orphan.csv", // no matching blob object
	}

	cfg := ManagedResourceConfig{Store: store, S3Client: blob, S3Bucket: "test-bucket", URIScheme: "mcp"}
	next := func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, fmt.Errorf("should not reach next")
	}
	handler := MCPManagedResourceMiddleware(cfg)(next)

	ctx := WithPlatformContext(context.Background(), &PlatformContext{UserID: "user-1", Roles: []string{"admin"}})
	_, err := handler(ctx, methodReadResource, makeReadRequest(t, "mcp://global/samples/orphan.csv"))

	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected an actionable missing/orphaned error, got: %v", err)
	}
	// The orphaned row was pruned so it will not reappear in list.
	if len(store.deleted) != 1 || store.deleted[0] != "r1" {
		t.Errorf("expected orphaned resource r1 to be pruned, deleted=%v", store.deleted)
	}
}

func TestFetchResourceContent_MissingBlobIsActionable(t *testing.T) {
	blob := newMockBlobReader() // empty -> GetObject returns a NoSuchKey/404 error

	cfg := ManagedResourceConfig{S3Client: blob, S3Bucket: "test-bucket"}
	res := &resource.Resource{
		URI:      "mcp://global/samples/test.csv",
		MIMEType: "text/csv",
		S3Key:    "resources/global/r1/missing.csv",
	}

	_, err := fetchResourceContent(context.Background(), cfg, res)
	if err == nil {
		t.Fatal("expected error for missing S3 object")
	}
	// The error names the resource and identifies it as orphaned/missing, not an
	// opaque "error reading resource content".
	if !strings.Contains(err.Error(), "missing") || !strings.Contains(err.Error(), res.URI) {
		t.Errorf("missing-blob error must be actionable and name the URI, got: %q", err.Error())
	}
}

func TestFetchResourceContent_TransientErrorIsGeneric(t *testing.T) {
	blob := &mockBlobReader{getErr: fmt.Errorf("connection reset by peer")}

	cfg := ManagedResourceConfig{S3Client: blob, S3Bucket: "test-bucket"}
	res := &resource.Resource{URI: "mcp://global/samples/test.csv", S3Key: "k"}

	_, err := fetchResourceContent(context.Background(), cfg, res)
	if err == nil {
		t.Fatal("expected error for transient S3 failure")
	}
	// A transient failure must NOT be reported as orphaned/missing.
	if strings.Contains(err.Error(), "missing") || strings.Contains(err.Error(), "orphaned") {
		t.Errorf("transient error must not be reported as missing/orphaned, got: %q", err.Error())
	}
}

func TestIsObjectNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"nosuchkey", fmt.Errorf("s3 get: NoSuchKey: the specified key does not exist"), true},
		{"status 404", fmt.Errorf("s3 get: api error, status code: 404"), true},
		{"plain not found", fmt.Errorf("not found"), true},
		{"connection reset", fmt.Errorf("connection reset by peer"), false},
		{"permission denied", fmt.Errorf("s3 get: AccessDenied: access denied"), false},
		{"timeout", fmt.Errorf("context deadline exceeded"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isObjectNotFound(tt.err); got != tt.want {
				t.Errorf("isObjectNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
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
