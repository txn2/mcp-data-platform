package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRedirectPolicy_HostPinned holds the redirect policy to its rule: a
// pattern matches the whole URI, and a host an outsider could register is
// never matched in place of the host the pattern names.
func TestRedirectPolicy_HostPinned(t *testing.T) {
	patterns := []string{
		`https://.*\.anthropic\.com/callback`,
		`http://localhost:.*/callback`,
		`https://claude\.ai/api/mcp/auth_callback`,
		`https://[a-z0-9-]+\.example\.com/cb`,
		`https://claude\.ai.*`,
		`https://[a-z]*corp\.test/cb`,
		`https://example\.co[a-z]*/cb`,
		`com\.example\.app:/callback`,
	}
	service, err := NewDCRService(&mockStorage{}, DCRConfig{Enabled: true, AllowedRedirectPatterns: patterns})
	if err != nil {
		t.Fatalf("NewDCRService: %v", err)
	}
	tests := []struct {
		uri     string
		allowed bool
	}{
		// Exact and subdomain forms an operator means.
		{"https://claude.ai/api/mcp/auth_callback", true},
		{"https://app.anthropic.com/callback", true},
		{"https://app.example.com/cb", true},
		{"http://localhost:53682/callback", true},
		{"com.example.app:/callback", true},

		// A wildcard reaching across the authority.
		{"https://evil.example/?x=.anthropic.com/callback", false},
		{"https://evil.example/x.anthropic.com/callback", false},
		// Matched as a substring before anchoring.
		{"https://evil.example/https://claude.ai/api/mcp/auth_callback", false},
		{"https://claude.ai/api/mcp/auth_callback/../../evil", false},
		// A wildcard after the host.
		{"https://claude.ai.evil.example/x", false},
		// A wildcard before the registrable domain.
		{"https://evilcorp.test/cb", false},
		// A wildcard in the top-level domain.
		{"https://example.com/cb", false},

		// Shapes whose host a browser reads differently.
		{"https://app.example.com@evil.example/cb", false},
		{"https://user:pw@app.example.com/cb", false},
		{`https://app.example.com\@evil.example/cb`, false},
		{"https://app%2eexample.com/cb", false},
		{"https://app.example.com/cb#frag", false},
		{"https://app.example.com/cb ", false},
		{"https:///cb", false},
		{"/relative/cb", false},
	}
	for _, tt := range tests {
		if got := service.isAllowedRedirectURI(tt.uri); got != tt.allowed {
			t.Errorf("isAllowedRedirectURI(%q) = %v, want %v", tt.uri, got, tt.allowed)
		}
		if got := service.AllowsRedirectURI(tt.uri); got != tt.allowed {
			t.Errorf("AllowsRedirectURI(%q) = %v, want %v", tt.uri, got, tt.allowed)
		}
	}
}

// TestRedirectPolicy_NilServiceAllowsNothing: with registration off, a client
// that registered itself earlier is held to no policy and admitted nowhere.
func TestRedirectPolicy_NilServiceAllowsNothing(t *testing.T) {
	var service *DCRService
	if service.AllowsRedirectURI("https://claude.ai/api/mcp/auth_callback") {
		t.Fatal("a nil DCR service admitted a redirect URI")
	}
}

func TestHostPinned_IPLiteral(t *testing.T) {
	service, err := NewDCRService(&mockStorage{}, DCRConfig{
		Enabled:                 true,
		AllowedRedirectPatterns: []string{`https://\[2001:db8::1\]/cb`, `https://.*/any`},
	})
	if err != nil {
		t.Fatalf("NewDCRService: %v", err)
	}
	if !service.isAllowedRedirectURI("https://[2001:db8::1]/cb") {
		t.Error("an IP literal the pattern names was refused")
	}
	if service.isAllowedRedirectURI("https://[2001:db8::1]/any") {
		t.Error("a pattern matching any host admitted an IP literal")
	}
}

// TestRegister_RefusesAnOversizedBody: the unauthenticated registration
// endpoint reads at most maxTokenRequestBytes of a body.
func TestRegister_RefusesAnOversizedBody(t *testing.T) {
	server, err := NewServer(ServerConfig{
		Issuer: "https://mcp.example.com",
		DCR:    DCRConfig{Enabled: true, AllowedRedirectPatterns: []string{`https://claude\.ai/cb`}},
	}, NewMemoryStorage())
	if err != nil {
		t.Fatal(err)
	}
	body := `{"client_name":"` + strings.Repeat("x", maxTokenRequestBytes+1) + `","redirect_uris":["https://claude.ai/cb"]}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("oversized registration = %d", w.Code)
	}
	ok := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/oauth/register",
		strings.NewReader(`{"client_name":"c","redirect_uris":["https://claude.ai/cb"]}`))
	w = httptest.NewRecorder()
	server.ServeHTTP(w, ok)
	if w.Code != http.StatusCreated {
		t.Fatalf("a small registration = %d: %s", w.Code, w.Body.String())
	}
}

// TestHostPinned_KeepsThePort: the probes keep the URI's port, so a pattern
// that names a port is held to the host as one that does not is.
func TestHostPinned_KeepsThePort(t *testing.T) {
	service, err := NewDCRService(&mockStorage{}, DCRConfig{
		Enabled:                 true,
		AllowedRedirectPatterns: []string{`https://app\.example\.com:8443/cb`, `https://.*:8443/any`},
	})
	if err != nil {
		t.Fatalf("NewDCRService: %v", err)
	}
	if !service.isAllowedRedirectURI("https://app.example.com:8443/cb") {
		t.Error("a host and port the pattern names was refused")
	}
	if service.isAllowedRedirectURI("https://attacker.test:8443/any") {
		t.Error("a pattern matching any host on a port admitted one")
	}
}
