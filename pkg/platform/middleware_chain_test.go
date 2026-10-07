package platform

import (
	"context"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/txn2/mcp-data-platform/internal/platform/mwchain"
	"github.com/txn2/mcp-data-platform/internal/platform/obs"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// TestReceivingMiddlewareChain_CanonicalOrder pins the exact execution order of
// the receiving-middleware chain (outermost first). Any deliberate reorder must
// update this list, so the change is an explicit, reviewable diff rather than a
// silent one-line move at the registration site (issue #758).
func TestReceivingMiddlewareChain_CanonicalOrder(t *testing.T) {
	// Method values are captured without invoking the registrations, so a
	// zero-value Platform is sufficient to inspect names and requires.
	p := &Platform{}

	want := []mwName{
		mwResultType,
		mwIcons,
		mwDescriptionOverride,
		mwPromptVisibility,
		mwToolVisibility,
		mwSessionHandleSchema,
		mwPurposeSchema,
		mwOutputSchema,
		mwMCPApps,
		mwTracing,
		mwMetrics,
		mwAudit,
		mwToolCall,
		mwSessionGate,
		mwWorkflowGate,
		mwRateLimit,
		mwReflexiveCapture,
		mwErrorContract,
		mwResultBudget,
		mwClientLogging,
		mwManagedResource,
		mwCallReference,
		mwEnrichment,
		mwResultCapture,
		mwUnwrapJSON,
	}

	specs := p.receivingMiddlewareChain()
	if len(specs) != len(want) {
		t.Fatalf("chain length = %d, want %d", len(specs), len(want))
	}
	for i, s := range specs {
		if s.Name != want[i] {
			t.Errorf("position %d: name = %q, want %q", i, s.Name, want[i])
		}
		if s.Register == nil {
			t.Errorf("position %d (%q): register func is nil", i, s.Name)
		}
	}
}

// TestReceivingMiddlewareChain_Validates proves the shipped chain satisfies
// every declared ordering dependency. This is the same check finalizeSetup runs
// at startup; asserting it in a unit test surfaces a violation as a test
// failure rather than only as a runtime panic.
func TestReceivingMiddlewareChain_Validates(t *testing.T) {
	if err := mwchain.Validate((&Platform{}).receivingMiddlewareChain()); err != nil {
		t.Fatalf("canonical middleware chain failed validation: %v", err)
	}
}

// TestReceivingMiddlewareChain_PlatformContextReadersRequireAuth guards the
// central invariant: every middleware that reads the PlatformContext BEFORE
// calling the next handler declares a dependency on the auth/authz middleware
// that fills it in. If a new reader is added without the requires entry, this
// test fails. The three observers (tracing, metrics, audit) are not in this
// set: they attach the context themselves and read it only after the call
// returns, which is what lets them sit outer to auth and record a refusal
// (#1892); the next test pins that side.
func TestReceivingMiddlewareChain_PlatformContextReadersRequireAuth(t *testing.T) {
	specs := (&Platform{}).receivingMiddlewareChain()

	readers := map[mwName]bool{
		mwSessionGate:      true,
		mwWorkflowGate:     true,
		mwRateLimit:        true,
		mwReflexiveCapture: true,
		mwCallReference:    true,
		mwEnrichment:       true,
	}

	for _, s := range specs {
		if !readers[s.Name] {
			continue
		}
		if !requires(s, mwToolCall) {
			t.Errorf("middleware %q reads PlatformContext but does not require %q", s.Name, mwToolCall)
		}
	}
}

// TestReceivingMiddlewareChain_RefusalsAreObserved pins the #1892 invariant:
// the auth/authz middleware declares tracing, metrics and audit as outer to it,
// so a call it or a gate inner to it refuses still produces a span, a counter
// increment and an audit row. Before this, all three sat inner to auth and the
// gates, and a refused call left one log line and nothing else.
func TestReceivingMiddlewareChain_RefusalsAreObserved(t *testing.T) {
	var toolCall mwSpec
	for _, s := range (&Platform{}).receivingMiddlewareChain() {
		if s.Name == mwToolCall {
			toolCall = s
		}
	}
	require.Equal(t, mwToolCall, toolCall.Name)
	for _, observer := range []mwName{mwTracing, mwMetrics, mwAudit} {
		if !requires(toolCall, observer) {
			t.Errorf("%q must declare %q as outer to it, or that observer misses every refused call", mwToolCall, observer)
		}
	}
}

// TestReceivingMiddlewareChain_DeclaredDependencies pins the ordering edges
// that the old linear registration guaranteed only by position. Each edge here
// corresponds to a real cross-middleware data dependency (verified against the
// middleware sources); if a future edit drops one of these Requires entries,
// the validator would silently stop protecting that ordering, so this test
// fails loudly instead.
func TestReceivingMiddlewareChain_DeclaredDependencies(t *testing.T) {
	byName := make(map[mwName]mwSpec)
	for _, s := range (&Platform{}).receivingMiddlewareChain() {
		byName[s.Name] = s
	}

	// want[X] lists middlewares that MUST be outer to X.
	want := map[mwName][]mwName{
		// PlatformContext readers depend on the auth/authz writer.
		mwCallReference: {mwToolCall},
		// The rate limiter reads PlatformContext identity to key its per-user
		// bucket, so it depends on the auth/authz writer.
		mwRateLimit: {mwToolCall},
		// Observers of the enrichment fields (set on the way out) must be
		// outer to enrichment.
		mwEnrichment: {mwToolCall, mwTracing, mwMetrics, mwAudit, mwClientLogging},
		// audit/metrics/reflexive-capture observe the normalized error, so the
		// error contract is inner to all three.
		mwErrorContract: {mwAudit, mwMetrics, mwReflexiveCapture},
	}

	for name, reqs := range want {
		s, ok := byName[name]
		if !ok {
			t.Fatalf("middleware %q not present in chain", name)
		}
		for _, r := range reqs {
			if !requires(s, r) {
				t.Errorf("middleware %q must declare requires %q", name, r)
			}
		}
	}
}

func requires(s mwSpec, name mwName) bool {
	return slices.Contains(s.Requires, name)
}

// TestAddSessionGateMiddleware covers both branches of the session-gate
// registration helper: nil gate is a no-op, a configured gate registers
// without panic.
func TestAddSessionGateMiddleware(_ *testing.T) {
	p := &Platform{config: &Config{}}
	p.mcpServer = mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)

	p.addSessionGateMiddleware() // disabled: nil gate → no-op

	p.sessionGate = middleware.NewSessionGate(middleware.SessionGateConfig{InitTool: "platform_info"})
	p.addSessionGateMiddleware() // enabled: registers without panic
}

// TestRateLimitMiddlewareRegister covers both branches of the inline rate-limit
// Register closure in receivingMiddlewareChain: disabled is a no-op, enabled
// builds the toolratelimit seam, registers its middleware, and hooks Close on
// the lifecycle (exercised by Start+Stop, which must not error).
func TestRateLimitMiddlewareRegister(t *testing.T) {
	rateLimitRegister := func(p *Platform) func() {
		for _, s := range p.receivingMiddlewareChain() {
			if s.Name == mwRateLimit {
				return s.Register
			}
		}
		t.Fatal("rate_limit spec not found in chain")
		return nil
	}

	newP := func(cfg RateLimitConfig) *Platform {
		p := &Platform{config: &Config{RateLimit: cfg}}
		p.obs = obs.New(nil, nil)
		p.mcpServer = mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
		p.lifecycle = NewLifecycle()
		return p
	}

	t.Run("disabled is a no-op", func(t *testing.T) {
		off := false
		p := newP(RateLimitConfig{Enabled: &off})
		rateLimitRegister(p)() // must not panic and register nothing
		require.NoError(t, p.lifecycle.Start(context.Background()))
		require.NoError(t, p.lifecycle.Stop(context.Background()))
	})

	t.Run("enabled registers and hooks close", func(t *testing.T) {
		p := newP(RateLimitConfig{ExemptTools: []string{"search"}})
		rateLimitRegister(p)() // default (nil) is enabled
		// Start then Stop drives the OnStop hook that closes the limiter's
		// eviction goroutine; both halves must succeed.
		require.NoError(t, p.lifecycle.Start(context.Background()))
		require.NoError(t, p.lifecycle.Stop(context.Background()))
	})
}

// TestAddTracingMiddleware covers both branches of the tracing registration
// helper: a nil (disabled) tracer is a no-op, an enabled tracer registers
// without panic.
func TestAddTracingMiddleware(t *testing.T) {
	p := &Platform{config: &Config{}}
	p.mcpServer = mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)

	p.addTracingMiddleware() // disabled: nil tracer → Enabled()==false → no-op

	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	p.obs = obs.New(nil, observability.NewTracerFromProvider(tp, observability.TracingConfig{Enabled: true}))
	p.addTracingMiddleware() // enabled: registers without panic
}
