package scriptrun

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/toolratelimit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// refusingCaller answers every call with {"n": <attempt>} after refusing the
// attempts listed in refuse (1-based, counted across every call the run makes)
// with the refusal it is given. It records how many attempts it saw, which is
// how a test tells a paced retry from a call the script never made.
type refusingCaller struct {
	refuse   map[int]error
	attempts int
}

func (c *refusingCaller) CallTool(_ context.Context, _ string, _ map[string]any) (map[string]any, error) {
	c.attempts++
	if err, ok := c.refuse[c.attempts]; ok {
		return nil, err
	}
	return map[string]any{"n": float64(c.attempts)}, nil
}

func rateLimited(after time.Duration) error {
	return &RefusalError{Code: toolratelimit.CodeRateLimited, RetryAfter: after, text: "RATE_LIMITED: wait and retry"}
}

const loopSource = "for i in range(3):\n    print(platform.call(\"echo\", {\"i\": i})[\"n\"])\n"

// TestRun_PacesARateLimitedCall is the engine half of the #1533 acceptance: a
// call refused for timing is waited out and issued again, the script sees the
// admitted call's result, the run succeeds, the log names each wait, and the
// step count is the one an unlimited caller would have produced.
func TestRun_PacesARateLimitedCall(t *testing.T) {
	unlimited, err := execute(t, loopSource, &refusingCaller{}, nil)
	require.NoError(t, err)

	caller := &refusingCaller{refuse: map[int]error{1: rateLimited(5 * time.Millisecond), 4: rateLimited(5 * time.Millisecond)}}
	paced, err := execute(t, loopSource, caller, nil)
	require.NoError(t, err)

	assert.Equal(t, 5, caller.attempts, "three calls, two of them issued twice")
	assert.Equal(t, "rate limit: echo was refused; waited 5ms and retried\n2\n3\n"+
		"rate limit: echo was refused; waited 5ms and retried\n5\n", paced.Log,
		"the script prints the admitted result, and the host records each wait beside it")
	assert.Equal(t, unlimited.Steps, paced.Steps, "the interpreter does not advance while the host waits")
}

// TestRun_PacingIsBoundedByTheRunDeadline: a wait the deadline cuts short fails
// the run as a timeout, which the worker never re-queues, and never as a
// rate-limit backtrace.
func TestRun_PacingIsBoundedByTheRunDeadline(t *testing.T) {
	caller := &refusingCaller{refuse: map[int]error{1: rateLimited(5 * time.Second)}}
	result, err := Run(context.Background(), Options{
		Source: loopSource, Name: "test", FireTime: fireTime, Caller: caller,
		Timeout: 50 * time.Millisecond,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrTimeout)
	assert.Contains(t, err.Error(), "waiting 5s to retry echo after a rate-limit refusal")
	assert.NotContains(t, err.Error(), "RATE_LIMITED")
	assert.Empty(t, result.Log, "a wait that did not complete is not recorded as one that did")
	assert.Equal(t, 1, caller.attempts)
}

// TestRun_OnlyARateLimitRefusalIsRetried: every other refusal, structured or
// not, fails the run exactly as it always has, on the first attempt.
func TestRun_OnlyARateLimitRefusalIsRetried(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"another structured refusal", &RefusalError{Code: middleware.CodeUnauthorized, RetryAfter: time.Millisecond, text: "not permitted"}},
		{"a plain error", errors.New("not permitted")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caller := &refusingCaller{refuse: map[int]error{1: tc.err}}
			_, err := execute(t, loopSource, caller, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not permitted")
			assert.NotErrorIs(t, err, ErrTimeout)
			assert.Equal(t, 1, caller.attempts)
		})
	}
}

// TestRun_ARefusalNamingNoIntervalIsPacedAtTheFloor: the platform's limiter
// always names at least one second, so a refusal without one is another
// producer's, and it is waited a second rather than re-issued at once.
func TestRun_ARefusalNamingNoIntervalIsPacedAtTheFloor(t *testing.T) {
	caller := &refusingCaller{refuse: map[int]error{1: rateLimited(0)}}
	started := time.Now()
	result, err := execute(t, "print(platform.call(\"echo\")[\"n\"])\n", caller, nil)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(started), minPace)
	assert.Equal(t, "rate limit: echo was refused; waited 1s and retried\n2\n", result.Log)
}

// TestRefusalError pins how a failed result becomes the error a Caller returns:
// the envelope's code and interval are read as data, the text is the result's
// own, and a result without the envelope is the plain error it always was.
func TestRefusalError(t *testing.T) {
	text := &mcp.TextContent{Text: "refused"}
	cases := []struct {
		name string
		res  *mcp.CallToolResult
		code string
		wait time.Duration
	}{
		{"no structured content", &mcp.CallToolResult{Content: []mcp.Content{text}}, "", 0},
		{"structured content without an envelope", &mcp.CallToolResult{
			Content: []mcp.Content{text}, StructuredContent: map[string]any{"rows": []any{}},
		}, "", 0},
		{"an envelope without a code", &mcp.CallToolResult{
			Content: []mcp.Content{text}, StructuredContent: map[string]any{"error": map[string]any{"message": "m"}},
		}, "", 0},
		{"an envelope naming no interval", &mcp.CallToolResult{
			Content: []mcp.Content{text}, StructuredContent: map[string]any{"error": map[string]any{"code": "unauthorized"}},
		}, "unauthorized", 0},
		{"a rate-limit envelope", &mcp.CallToolResult{
			Content: []mcp.Content{text},
			StructuredContent: map[string]any{"error": map[string]any{
				"code": "rate_limited", "retry_after_seconds": float64(3),
			}},
		}, "rate_limited", 3 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := refusalError(tc.res)
			require.Error(t, err)
			assert.Equal(t, "refused", err.Error(), "the text the author reads is the tool's own")
			var refusal *RefusalError
			if tc.code == "" {
				assert.False(t, errors.As(err, &refusal), "no envelope, no typed refusal")
				return
			}
			require.True(t, errors.As(err, &refusal))
			assert.Equal(t, tc.code, refusal.Code)
			assert.Equal(t, tc.wait, refusal.RetryAfter)
		})
	}
}

// TestSessionCaller_ReadsTheEnvelopeOverTheWire drives the production Caller
// against a tool that refuses with BuildErrorResult, so the field the limiter
// sets is proven to survive JSON and arrive as the refusal the engine paces on.
func TestSessionCaller_ReadsTheEnvelopeOverTheWire(t *testing.T) {
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "refuse"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			pe := middleware.NewToolError(toolratelimit.CodeRateLimited, "rate_limited", "too many calls", "pause")
			pe.RetryAfterSeconds = 2
			return middleware.BuildErrorResult(pe), nil, nil
		})
	caller, cleanup, err := Connect(ctx, server, "test")
	require.NoError(t, err)
	defer cleanup()

	_, err = caller.CallTool(ctx, "refuse", nil)
	require.Error(t, err)
	var refusal *RefusalError
	require.True(t, errors.As(err, &refusal))
	assert.Equal(t, toolratelimit.CodeRateLimited, refusal.Code)
	assert.Equal(t, 2*time.Second, refusal.RetryAfter)
	assert.Contains(t, err.Error(), "too many calls")
	assert.Contains(t, err.Error(), "code: rate_limited")
}
