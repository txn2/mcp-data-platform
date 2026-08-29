package toolratelimit

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

const (
	rlToolTrinoQuery   = "trino_query"
	rlToolPlatformInfo = "platform_info"
	rlToolSearch       = "search"
)

var rlSuccessResult = &mcp.CallToolResult{
	Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
}

// rlCall returns a middleware-wrapped tools/call invoker plus a pointer to a
// flag set when the inner handler runs.
func rlCall(h *Handle) (call func(ctx context.Context) (mcp.Result, error), ran *bool) {
	called := false
	next := h.Middleware()(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		called = true
		return rlSuccessResult, nil
	})
	return func(ctx context.Context) (mcp.Result, error) {
		return next(ctx, "tools/call", nil)
	}, &called
}

// toolResult asserts a middleware result is a CallToolResult and returns it, so
// callers can inspect GetError() (nil means the call was allowed).
func toolResult(t *testing.T, r mcp.Result) *mcp.CallToolResult {
	t.Helper()
	cr, ok := r.(*mcp.CallToolResult)
	require.True(t, ok, "result is not a *mcp.CallToolResult")
	return cr
}

// userCtx builds a tools/call context for a distinct authenticated user.
func userCtx(userID, tool string) context.Context {
	pc := middleware.NewPlatformContext("req")
	pc.UserID = userID
	pc.AuthType = middleware.AuthTypeOIDC
	pc.ToolName = tool
	return middleware.WithPlatformContext(context.Background(), pc)
}

func TestNew_Exempt(t *testing.T) {
	h := New(60, 2, []string{rlToolSearch}, nil)
	defer h.Close()

	assert.True(t, h.IsExempt(rlToolPlatformInfo), "platform_info is always exempt")
	assert.True(t, h.IsExempt(rlToolSearch), "configured exempt tool")
	assert.False(t, h.IsExempt(rlToolTrinoQuery))
}

func TestNew_RetryAfter(t *testing.T) {
	// rate = 240/60 = 4 tokens/sec -> 1/4 = 0.25s -> ceil = 1s.
	fast := New(240, 60, nil, nil)
	defer fast.Close()
	assert.Equal(t, 1, fast.retryAfterSeconds)

	// rate = 30/60 = 0.5 tokens/sec -> 1/0.5 = 2s.
	slow := New(30, 1, nil, nil)
	defer slow.Close()
	assert.Equal(t, 2, slow.retryAfterSeconds)
}

func TestMiddleware_NonToolsCallPassesThrough(t *testing.T) {
	h := New(60, 1, nil, nil)
	defer h.Close()

	ran := false
	next := h.Middleware()(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		ran = true
		return &mcp.ListToolsResult{}, nil
	})
	for range 5 {
		_, err := next(context.Background(), "tools/list", nil)
		require.NoError(t, err)
	}
	assert.True(t, ran)
}

func TestMiddleware_NoPlatformContextPassesThrough(t *testing.T) {
	h := New(60, 1, nil, nil)
	defer h.Close()

	call, ran := rlCall(h)
	for range 5 {
		result, err := call(context.Background())
		require.NoError(t, err)
		assert.NotNil(t, result)
	}
	assert.True(t, *ran)
}

func TestMiddleware_UnderLimitPasses(t *testing.T) {
	h := New(60, 3, nil, nil)
	defer h.Close()

	call, ran := rlCall(h)
	ctx := userCtx("alice", rlToolTrinoQuery)
	for i := range 3 {
		result, err := call(ctx)
		require.NoError(t, err, "call %d", i)
		assert.Equal(t, rlSuccessResult, result)
	}
	assert.True(t, *ran)
}

func TestMiddleware_BurstOverLimitRefuses(t *testing.T) {
	h := New(60, 2, nil, nil)
	defer h.Close()

	call, _ := rlCall(h)
	ctx := userCtx("alice", rlToolTrinoQuery)
	for range 2 {
		_, err := call(ctx)
		require.NoError(t, err)
	}

	result, err := call(ctx)
	require.NoError(t, err)
	callResult, ok := result.(*mcp.CallToolResult)
	require.True(t, ok)
	getErr := callResult.GetError()
	require.NotNil(t, getErr)
	assert.Contains(t, getErr.Error(), "RATE_LIMITED")
	assert.Equal(t, categoryRateLimited, middleware.ErrorCategory(getErr))

	// The interval the message names travels as data beside it, decoded the
	// way a client sees it: through the JSON the envelope is sent as.
	raw, err := json.Marshal(callResult.StructuredContent)
	require.NoError(t, err)
	var sc map[string]map[string]any
	require.NoError(t, json.Unmarshal(raw, &sc))
	env := sc["error"]
	assert.Equal(t, CodeRateLimited, env["code"])
	assert.Equal(t, float64(h.retryAfterSeconds), env["retry_after_seconds"])
	assert.Contains(t, env["message"], fmt.Sprintf("Wait about %d second(s)", h.retryAfterSeconds))
}

func TestMiddleware_ExemptToolNeverRefused(t *testing.T) {
	h := New(60, 1, nil, nil)
	defer h.Close()

	call, ran := rlCall(h)
	ctx := userCtx("alice", rlToolPlatformInfo)
	for range 10 {
		result, err := call(ctx)
		require.NoError(t, err)
		assert.Equal(t, rlSuccessResult, result)
	}
	assert.True(t, *ran)
}

func TestMiddleware_PerUserIsolation(t *testing.T) {
	h := New(60, 1, nil, nil)
	defer h.Close()

	call, _ := rlCall(h)

	_, err := call(userCtx("alice", rlToolTrinoQuery))
	require.NoError(t, err)
	refused, err := call(userCtx("alice", rlToolTrinoQuery))
	require.NoError(t, err)
	require.NotNil(t, toolResult(t, refused).GetError())

	result, err := call(userCtx("bob", rlToolTrinoQuery))
	require.NoError(t, err)
	assert.Nil(t, toolResult(t, result).GetError(), "bob must not share alice's bucket")
}

func TestMiddleware_NonDistinctIdentityFallsBackToSession(t *testing.T) {
	h := New(60, 1, nil, nil)
	defer h.Close()

	call, _ := rlCall(h)
	mk := func(session string) context.Context {
		pc := middleware.NewPlatformContext("req")
		pc.UserID = "anonymous"
		pc.AuthType = middleware.AuthTypeAnonymous
		pc.SessionID = session
		pc.ToolName = rlToolTrinoQuery
		return middleware.WithPlatformContext(context.Background(), pc)
	}

	_, err := call(mk("s1"))
	require.NoError(t, err)
	refused, err := call(mk("s1"))
	require.NoError(t, err)
	require.NotNil(t, toolResult(t, refused).GetError())

	result, err := call(mk("s2"))
	require.NoError(t, err)
	assert.Nil(t, toolResult(t, result).GetError())
}

func TestMiddleware_UnkeyableFailsOpen(t *testing.T) {
	h := New(60, 1, nil, nil)
	defer h.Close()

	call, _ := rlCall(h)
	mk := func() context.Context {
		pc := middleware.NewPlatformContext("req")
		pc.AuthType = middleware.AuthTypeNoop
		pc.ToolName = rlToolTrinoQuery
		return middleware.WithPlatformContext(context.Background(), pc)
	}
	for range 5 {
		result, err := call(mk())
		require.NoError(t, err)
		assert.Nil(t, toolResult(t, result).GetError())
	}
}

func TestMiddleware_RefusalIncrementsCounter(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	defer func() { _ = m.Shutdown(context.Background()) }()

	h := New(60, 1, nil, m)
	defer h.Close()

	call, _ := rlCall(h)
	ctx := userCtx("alice", rlToolTrinoQuery)
	_, err = call(ctx) // consumes the token
	require.NoError(t, err)
	_, err = call(ctx) // refused -> counter++
	require.NoError(t, err)

	body := scrapeMetrics(t, m.Handler())
	assert.Contains(t, body, "mcp_rate_limited_total 1")
}

func TestClose_NilSafeAndIdempotent(_ *testing.T) {
	var h *Handle
	h.Close() // must not panic

	h2 := New(60, 1, nil, nil)
	h2.Close()
	h2.Close() // idempotent
}

func TestConcurrentAccess(_ *testing.T) {
	h := New(6000, 100, nil, nil)
	defer h.Close()

	next := h.Middleware()(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return rlSuccessResult, nil
	})
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = next(userCtx("user", rlToolTrinoQuery), "tools/call", nil)
			_ = h.IsExempt(rlToolPlatformInfo)
			_ = n
		}(i)
	}
	wg.Wait() // race detector proves the limiter is safe for concurrent use.
}

// scrapeMetrics fetches the Prometheus exposition text from a metrics handler.
func scrapeMetrics(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	defer srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}
