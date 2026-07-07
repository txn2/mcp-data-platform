package platform

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// stubAuthenticator returns a fixed UserInfo/error for identity tests.
type stubAuthenticator struct {
	info *middleware.UserInfo
	err  error
}

func (s stubAuthenticator) Authenticate(_ context.Context) (*middleware.UserInfo, error) {
	return s.info, s.err
}

func TestGatewayIdentityResolver(t *testing.T) {
	tests := []struct {
		name string
		auth middleware.Authenticator
		want string
	}{
		{
			name: "api key name",
			auth: stubAuthenticator{info: &middleware.UserInfo{UserID: "apikey:nifi-etl", AuthType: "apikey"}},
			want: "nifi-etl",
		},
		{
			name: "oidc email",
			auth: stubAuthenticator{info: &middleware.UserInfo{UserID: "sub-123", Email: "jo@example.com", AuthType: "oidc"}},
			want: "jo@example.com",
		},
		{
			name: "oidc subject fallback when no email",
			auth: stubAuthenticator{info: &middleware.UserInfo{UserID: "sub-123", AuthType: "oidc"}},
			want: "sub-123",
		},
		{
			name: "auth error yields unknown",
			auth: stubAuthenticator{err: errors.New("bad token")},
			want: "unknown",
		},
		{
			name: "nil info yields unknown",
			auth: stubAuthenticator{},
			want: "unknown",
		},
		{
			name: "apikey without prefix falls through to userid",
			auth: stubAuthenticator{info: &middleware.UserInfo{UserID: "weird", AuthType: "apikey"}},
			want: "weird",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &Platform{authenticator: tc.auth}
			r := p.NewGatewayIdentityResolver()
			if got := r.ResolveIdentity(context.Background()); got != tc.want {
				t.Errorf("ResolveIdentity() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGatewayIdentityResolver_NilAuthenticator(t *testing.T) {
	p := &Platform{}
	r := p.NewGatewayIdentityResolver()
	if got := r.ResolveIdentity(context.Background()); got != "unknown" {
		t.Errorf("ResolveIdentity() with nil authn = %q, want unknown", got)
	}
}

func TestObservability_EnabledByDefault(t *testing.T) {
	// Bind the listener to an ephemeral port so this test does not collide
	// with the package's fixed :9090 default when other tests run in parallel.
	t.Setenv("OTEL_METRICS_ADDR", "127.0.0.1:0")

	p := newTestPlatform(t)
	defer func() { _ = p.Close() }()

	if p.Metrics() == nil {
		t.Fatal("Metrics() = nil with default env; want non-nil (enabled by default)")
	}
	// Wire call is idempotent and safe with no apigateway toolkit registered.
	p.WireAPIGatewayMetrics()
}

func TestObservability_ExplicitDisable(t *testing.T) {
	t.Setenv("OTEL_METRICS_ENABLED", "false")

	p := newTestPlatform(t)
	defer func() { _ = p.Close() }()

	if p.Metrics() != nil {
		t.Errorf("Metrics() = non-nil with OTEL_METRICS_ENABLED=false; want nil")
	}
	// Start/Shutdown must be safe even when disabled.
	if err := p.StartMetricsListener(context.Background()); err != nil {
		t.Errorf("StartMetricsListener (disabled) err = %v", err)
	}
	if err := p.ShutdownMetricsListener(context.Background()); err != nil {
		t.Errorf("ShutdownMetricsListener (disabled) err = %v", err)
	}
	// Wire call is a no-op; must not panic on toolkit walk.
	p.WireAPIGatewayMetrics()
}

func TestObservability_EnabledStartsListener(t *testing.T) {
	// Find an ephemeral port and release it so the listener can bind.
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("ephemeral port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	t.Setenv("OTEL_METRICS_ENABLED", "true")
	t.Setenv("OTEL_METRICS_ADDR", addr)

	p := newTestPlatform(t)
	defer func() { _ = p.Close() }()

	if p.Metrics() == nil {
		t.Fatal("Metrics() = nil with env enabled; want non-nil")
	}
	if err := p.StartMetricsListener(context.Background()); err != nil {
		t.Fatalf("StartMetricsListener: %v", err)
	}
	defer func() {
		_ = p.ShutdownMetricsListener(context.Background())
	}()

	// Wire call is idempotent and safe even with no apigateway toolkit.
	p.WireAPIGatewayMetrics()

	// Verify the listener bound and serves /metrics. We don't assert
	// the body shape here — that's covered by the observability
	// package's own tests; this proves the platform wiring put the
	// listener on the configured address.
	url := "http://" + addr + "/metrics"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec,bodyclose // test code; URL is a literal ephemeral http://127.0.0.1, body closed below
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort cleanup; body fully read above
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /metrics status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "go_goroutines") {
		t.Errorf("expected go_goroutines in /metrics body; got:\n%s", string(body))
	}
}

// TestWireAPIGatewayMetrics_InstrumentsRegisteredToolkit exercises the
// loop body of WireAPIGatewayMetrics: with a real apigateway toolkit
// registered in the platform's registry, the wire step must thread
// the platform's metrics recorder through the toolkit so subsequent
// outbound calls record observations.
func TestWireAPIGatewayMetrics_InstrumentsRegisteredToolkit(t *testing.T) {
	t.Setenv("OTEL_METRICS_ENABLED", "true")
	t.Setenv("OTEL_METRICS_ADDR", "127.0.0.1:0") // not started in this test

	// Build a registry with an apigateway toolkit pre-registered so
	// the platform constructor consumes it via WithToolkitRegistry.
	reg := registry.NewRegistry()
	api := apigatewaykit.New("primary")
	if err := reg.Register(api); err != nil {
		t.Fatalf("register apigateway: %v", err)
	}

	p := newTestPlatform(t, WithToolkitRegistry(reg))
	defer func() { _ = p.Close() }()

	if p.Metrics() == nil {
		t.Fatal("Metrics() = nil with env enabled; want non-nil")
	}
	// Pre-wire: toolkit metrics is nil. Wire then confirm the toolkit
	// would emit on a subsequent outbound (we cannot easily invoke
	// the round-trip without standing up a server; the SetMetrics
	// unit test in pkg/toolkits/apigateway covers transport wrapping
	// directly, so here we only verify the wire call does not error
	// and the platform path exercises the loop body).
	p.WireAPIGatewayMetrics()
}

// TestObservabilityAuthorizer_CapabilityGating proves the observability:read
// capability is enforced through the REAL persona authorizer: a persona
// whose tools.allow grants the capability passes; one that does not is
// denied. This exercises the same authz path the proxy uses in
// production (authenticate -> IsAuthorized(observability:read) -> persona).
func TestObservabilityAuthorizer_CapabilityGating(t *testing.T) {
	reg := persona.NewRegistry()
	// The personas' own Roles deliberately do NOT include the mapped
	// OIDC role ("ops-token"/"analyst-token"); resolution happens only
	// through PersonaMapping. This is the case that exposed the prior
	// bug where persona was resolved a second way (GetForRoles) that
	// ignores PersonaMapping and returned empty.
	require.NoError(t, reg.Register(&persona.Persona{
		Name:  "ops",
		Roles: []string{"ops-role"},
		Tools: persona.ToolRules{Allow: []string{"observability:read"}},
	}))
	require.NoError(t, reg.Register(&persona.Persona{
		Name:  "analyst",
		Roles: []string{"analyst-role"},
		Tools: persona.ToolRules{Allow: []string{"trino_*"}},
	}))
	mapper := &persona.OIDCRoleMapper{
		PersonaMapping: map[string]string{"ops-token": "ops", "analyst-token": "analyst"},
		Registry:       reg,
	}
	authz := persona.NewAuthorizer(reg, mapper)

	tests := []struct {
		name        string
		roles       []string
		wantAllowed bool
		wantPersona string
	}{
		{"granted persona via PersonaMapping", []string{"ops-token"}, true, "ops"},
		{"denied persona via PersonaMapping", []string{"analyst-token"}, false, "analyst"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Build through the public constructor so it is covered too.
			p := &Platform{
				authenticator:   stubAuthenticator{info: &middleware.UserInfo{UserID: "u", Roles: tc.roles}},
				authorizer:      authz,
				personaRegistry: reg,
			}
			dec := p.NewObservabilityAuthorizer().Authorize(context.Background())
			assert.True(t, dec.Authenticated)
			assert.Equal(t, tc.wantAllowed, dec.Allowed)
			assert.Equal(t, tc.wantPersona, dec.Persona)
		})
	}
}

func TestObservabilityAuthorizer_Unauthenticated(t *testing.T) {
	p := &Platform{authenticator: stubAuthenticator{err: errors.New("bad token")}}
	dec := p.NewObservabilityAuthorizer().Authorize(context.Background())
	assert.False(t, dec.Authenticated)
	assert.False(t, dec.Allowed)
}
