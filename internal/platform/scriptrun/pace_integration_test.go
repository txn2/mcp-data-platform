package scriptrun

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/toolratelimit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// limitedAuthn authenticates every call as one identity of the given auth
// type: the author's own (a draft run) or the script principal (a platform
// run). The limiter treats the two differently, which is what these tests pin.
type limitedAuthn struct{ authType string }

func (a limitedAuthn) Authenticate(context.Context) (*middleware.UserInfo, error) {
	return &middleware.UserInfo{
		UserID: "paced", Email: "paced@example.com",
		Roles: []string{"analyst"}, AuthType: a.authType,
	}, nil
}

type limitedAuthz struct{}

func (limitedAuthz) IsAuthorized(context.Context, string, []string, string, string) (authorized bool, persona, reason string) {
	return true, "analyst", ""
}

type limitedLookup struct{}

func (limitedLookup) GetToolkitForTool(string) registry.ToolkitMatch { return registry.ToolkitMatch{} }

type echoInput struct {
	N int `json:"n"`
}

// limitedServer assembles the real chain a script's call crosses — the
// tool-call middleware that establishes its identity, outermost, then the
// platform's own limiter — over an echo tool, and returns the Caller a run is
// handed plus the count of calls the handler actually served.
func limitedServer(t *testing.T, authType string, rpm, burst int) (caller Caller, served *int) {
	t.Helper()
	served = new(int)
	server := mcp.NewServer(&mcp.Implementation{Name: "limited", Version: "v0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo"},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
			*served++
			return nil, map[string]any{"n": in.N}, nil
		})
	limiter := toolratelimit.New(rpm, burst, nil, nil)
	t.Cleanup(limiter.Close)
	server.AddReceivingMiddleware(limiter.Middleware())
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(limitedAuthn{authType}, limitedAuthz{}, limitedLookup{},
		middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"}))

	caller, cleanup, err := Connect(context.Background(), server, "script-run")
	require.NoError(t, err)
	t.Cleanup(cleanup)
	return caller, served
}

const pacedLoop = "for i in range(4):\n    print(platform.call(\"echo\", {\"n\": i})[\"n\"])\n"

// TestIntegration_ADraftRunOutpacingTheLimiterCompletes is the #1533
// acceptance through the real chain: a draft run carries its author's own
// identity, so a loop issuing more calls than the limiter's burst admits is
// refused, and the host absorbs each refusal. The run completes with every
// call's result, and the captured log carries one line per absorbed refusal.
// rpm=60 refills one token a second, so the calls past the burst are refused
// deterministically and each wait is the limiter's own one-second interval.
func TestIntegration_ADraftRunOutpacingTheLimiterCompletes(t *testing.T) {
	caller, served := limitedServer(t, middleware.AuthTypeOIDC, 60, 2)
	result, err := Run(context.Background(), RunLimits().withCaller(caller))
	require.NoError(t, err)

	assert.Equal(t, 4, *served, "every call the script made was served exactly once")
	assert.Equal(t, "0\n1\n"+
		"rate limit: echo was refused; waited 1s and retried\n2\n"+
		"rate limit: echo was refused; waited 1s and retried\n3\n", result.Log)
	assert.Equal(t, 2, strings.Count(result.Log, "rate limit:"))

	unlimited, _ := limitedServer(t, middleware.AuthTypeOIDC, 60, 100)
	free, err := Run(context.Background(), RunLimits().withCaller(unlimited))
	require.NoError(t, err)
	assert.Equal(t, free.Steps, result.Steps, "pacing changes wall-clock time and nothing else")
}

// TestIntegration_APlatformRunOutpacingTheLimiterIsQueued is the #1534
// acceptance through the real chain and the session a platform run uses: a
// script principal's calls past the burst are held by the middleware until
// the sustained rate admits them, never refused. The host's pacing never
// fires, so the log carries no rate-limit line; every call is served; and the
// wall clock reflects the sustained rate. rpm=600 refills one token every
// 100ms, so the two calls past the burst wait about 200ms between them.
func TestIntegration_APlatformRunOutpacingTheLimiterIsQueued(t *testing.T) {
	caller, served := limitedServer(t, middleware.AuthTypeScript, 600, 2)
	start := time.Now()
	result, err := Run(context.Background(), RunLimits().withCaller(caller))
	require.NoError(t, err)

	assert.Equal(t, 4, *served, "every call the script made was served exactly once")
	assert.Equal(t, "0\n1\n2\n3\n", result.Log, "a queued call is not a refusal the host absorbs")
	assert.GreaterOrEqual(t, time.Since(start), 150*time.Millisecond, "the sustained rate governs the run")

	unlimited, _ := limitedServer(t, middleware.AuthTypeScript, 600, 100)
	free, err := Run(context.Background(), RunLimits().withCaller(unlimited))
	require.NoError(t, err)
	assert.Equal(t, free.Steps, result.Steps, "queueing changes wall-clock time and nothing else")
}

// TestIntegration_ALimiterTheDeadlineCannotWaitOutFailsAsATimeout: when the
// sustained rate cannot admit the loop inside the run's timeout, the run fails
// as ErrTimeout, the failure every caller already treats as final, and not with
// a RATE_LIMITED backtrace. For a draft the deadline arrives in the host's own
// wait; for a platform run it arrives while the middleware holds the call, and
// the client's cancellation reaches the wait through the session.
func TestIntegration_ALimiterTheDeadlineCannotWaitOutFailsAsATimeout(t *testing.T) {
	for _, authType := range []string{middleware.AuthTypeOIDC, middleware.AuthTypeScript} {
		t.Run(authType, func(t *testing.T) {
			caller, served := limitedServer(t, authType, 6, 1)
			opts := RunLimits().withCaller(caller)
			opts.Timeout = 200 * time.Millisecond
			start := time.Now()
			_, err := Run(context.Background(), opts)
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrTimeout)
			assert.NotContains(t, err.Error(), "RATE_LIMITED")
			assert.Less(t, time.Since(start), 3*time.Second, "the run must end at its deadline, not at the next refill")
			assert.Equal(t, 1, *served)
		})
	}
}

// withCaller returns o with the paced loop as its source and the caller a test
// run needs.
func (o Options) withCaller(caller Caller) Options {
	o.Source, o.Name, o.RunID, o.FireTime, o.Caller = pacedLoop, "paced", "run_1", fireTime, caller
	return o
}
