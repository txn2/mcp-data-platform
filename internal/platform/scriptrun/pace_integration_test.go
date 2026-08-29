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

type limitedAuthn struct{}

func (limitedAuthn) Authenticate(context.Context) (*middleware.UserInfo, error) {
	return &middleware.UserInfo{
		UserID: "script:paced", Email: "paced@example.com",
		Roles: []string{"analyst"}, AuthType: middleware.AuthTypeScript,
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
func limitedServer(t *testing.T, rpm, burst int) (caller Caller, served *int) {
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
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(limitedAuthn{}, limitedAuthz{}, limitedLookup{},
		middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"}))

	caller, cleanup, err := Connect(context.Background(), server, "script-run")
	require.NoError(t, err)
	t.Cleanup(cleanup)
	return caller, served
}

const pacedLoop = "for i in range(4):\n    print(platform.call(\"echo\", {\"n\": i})[\"n\"])\n"

// TestIntegration_ARunOutpacingTheLimiterCompletes is the #1533 acceptance
// through the real chain: a loop issuing more calls than the limiter's burst
// admits completes with every call's result, and the captured log carries one
// line per absorbed refusal. rpm=60 refills one token a second, so the calls
// past the burst are refused deterministically and each wait is the limiter's
// own one-second interval.
func TestIntegration_ARunOutpacingTheLimiterCompletes(t *testing.T) {
	caller, served := limitedServer(t, 60, 2)
	result, err := Run(context.Background(), RunLimits().withSource(pacedLoop, caller))
	require.NoError(t, err)

	assert.Equal(t, 4, *served, "every call the script made was served exactly once")
	assert.Equal(t, "0\n1\n"+
		"rate limit: echo was refused; waited 1s and retried\n2\n"+
		"rate limit: echo was refused; waited 1s and retried\n3\n", result.Log)
	assert.Equal(t, 2, strings.Count(result.Log, "rate limit:"))

	unlimited, _ := limitedServer(t, 60, 100)
	free, err := Run(context.Background(), RunLimits().withSource(pacedLoop, unlimited))
	require.NoError(t, err)
	assert.Equal(t, free.Steps, result.Steps, "pacing changes wall-clock time and nothing else")
}

// TestIntegration_ALimiterTheDeadlineCannotWaitOutFailsAsATimeout: when the
// sustained rate cannot admit the loop inside the run's timeout, the run fails
// as ErrTimeout, the failure every caller already treats as final, and not with
// a RATE_LIMITED backtrace.
func TestIntegration_ALimiterTheDeadlineCannotWaitOutFailsAsATimeout(t *testing.T) {
	caller, served := limitedServer(t, 60, 1)
	opts := RunLimits().withSource(pacedLoop, caller)
	opts.Timeout = 200 * time.Millisecond
	_, err := Run(context.Background(), opts)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTimeout)
	assert.NotContains(t, err.Error(), "RATE_LIMITED")
	assert.Equal(t, 1, *served)
}

// withSource returns o with the source and caller a test run needs.
func (o Options) withSource(source string, caller Caller) Options {
	o.Source, o.Name, o.RunID, o.FireTime, o.Caller = source, "paced", "run_1", fireTime, caller
	return o
}
