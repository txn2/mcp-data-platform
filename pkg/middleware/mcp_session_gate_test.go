package middleware

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants for session gate tests.
const (
	gateTestToolDatahubSearch  = "datahub_search"
	gateTestToolListConns      = "list_connections"
	gateTestToolPlatformInfo   = "platform_info"
	gateTestToolReadResource   = "read_resource"
	gateTestToolTrinoQuery     = "trino_query"
	gateTestToolSomeFutureTool = "some_future_tool"
)

func TestNewSessionGate(t *testing.T) {
	t.Run("init tool always exempt", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{
			InitTool: gateTestToolPlatformInfo,
		})
		assert.True(t, gate.IsExempt(gateTestToolPlatformInfo))
	})

	t.Run("custom exempt tools", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{
			InitTool:    gateTestToolPlatformInfo,
			ExemptTools: []string{gateTestToolListConns, gateTestToolReadResource},
		})
		assert.True(t, gate.IsExempt(gateTestToolPlatformInfo))
		assert.True(t, gate.IsExempt(gateTestToolListConns))
		assert.True(t, gate.IsExempt(gateTestToolReadResource))
		assert.False(t, gate.IsExempt(gateTestToolDatahubSearch))
	})

	t.Run("default TTL", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{InitTool: gateTestToolPlatformInfo})
		assert.Equal(t, defaultGateSessionTTL*time.Minute, gate.sessionTTL)
	})

	t.Run("custom TTL", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{
			InitTool:   gateTestToolPlatformInfo,
			SessionTTL: 1 * time.Hour,
		})
		assert.Equal(t, 1*time.Hour, gate.sessionTTL)
	})
}

func TestSessionGate_RecordAndCheck(t *testing.T) {
	gate := NewSessionGate(SessionGateConfig{
		InitTool:   gateTestToolPlatformInfo,
		SessionTTL: defaultGateSessionTTL * time.Minute,
	})

	assert.False(t, gate.IsInitialized("s1"))

	gate.RecordInit("s1")
	assert.True(t, gate.IsInitialized("s1"))
	assert.False(t, gate.IsInitialized("s2"), "sessions should be isolated")
}

func TestSessionGate_TTLExpiration(t *testing.T) {
	gate := NewSessionGate(SessionGateConfig{
		InitTool:   gateTestToolPlatformInfo,
		SessionTTL: 50 * time.Millisecond,
	})

	gate.RecordInit("s1")
	require.True(t, gate.IsInitialized("s1"))

	time.Sleep(60 * time.Millisecond)
	assert.False(t, gate.IsInitialized("s1"), "expired session should not be initialized")
}

func TestSessionGate_Cleanup(t *testing.T) {
	gate := NewSessionGate(SessionGateConfig{
		InitTool:   gateTestToolPlatformInfo,
		SessionTTL: 50 * time.Millisecond,
	})

	gate.RecordInit("s1")
	gate.RecordInit("s2")
	require.True(t, gate.IsInitialized("s1"))

	time.Sleep(60 * time.Millisecond)
	gate.cleanup()

	_, _, active := gate.Stats()
	assert.Equal(t, int64(0), active, "all expired sessions should be cleaned up")
}

func TestSessionGate_StartCleanupAndStop(t *testing.T) {
	gate := NewSessionGate(SessionGateConfig{
		InitTool:   gateTestToolPlatformInfo,
		SessionTTL: 10 * time.Millisecond,
	})
	gate.StartCleanup(10 * time.Millisecond)

	gate.RecordInit("s-bg")
	time.Sleep(50 * time.Millisecond)

	// Stop should not panic
	gate.Stop()

	assert.False(t, gate.IsInitialized("s-bg"))
}

func TestSessionGate_Stats(t *testing.T) {
	gate := NewSessionGate(SessionGateConfig{
		InitTool:   gateTestToolPlatformInfo,
		SessionTTL: defaultGateSessionTTL * time.Minute,
	})

	gate.RecordInit("s1")
	gate.IncrementGateCount()
	gate.IncrementGateCount()

	violations, retries, active := gate.Stats()
	assert.Equal(t, int64(2), violations)
	assert.Equal(t, int64(0), retries)
	assert.Equal(t, int64(1), active)

	// Recording init for existing session counts as retry
	gate.RecordInit("s1")
	_, retries, _ = gate.Stats()
	assert.Equal(t, int64(1), retries)
}

func TestSessionGate_ConcurrentAccess(t *testing.T) {
	gate := NewSessionGate(SessionGateConfig{
		InitTool:   gateTestToolPlatformInfo,
		SessionTTL: defaultGateSessionTTL * time.Minute,
	})

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sid := "session-concurrent"
			if n%3 == 0 {
				gate.RecordInit(sid)
			}
			_ = gate.IsInitialized(sid)
			_ = gate.IsExempt(gateTestToolPlatformInfo)
			gate.IncrementGateCount()
		}(i)
	}
	wg.Wait()

	// No panics = success; verify state is readable
	assert.True(t, gate.IsInitialized("session-concurrent"))
}

func TestMCPSessionGateMiddleware(t *testing.T) {
	successResult := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
	}

	t.Run("non-tools/call passes through", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{InitTool: gateTestToolPlatformInfo})
		mw := MCPSessionGateMiddleware(gate)

		nextCalled := false
		handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			nextCalled = true
			return &mcp.ListResourcesResult{}, nil
		})

		result, err := handler(context.Background(), "resources/list", nil)
		require.NoError(t, err)
		assert.True(t, nextCalled)
		assert.NotNil(t, result)
	})

	t.Run("no platform context passes through", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{InitTool: gateTestToolPlatformInfo})
		mw := MCPSessionGateMiddleware(gate)

		nextCalled := false
		handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			nextCalled = true
			return successResult, nil
		})

		result, err := handler(context.Background(), methodToolsCall, nil)
		require.NoError(t, err)
		assert.True(t, nextCalled)
		assert.NotNil(t, result)
	})

	t.Run("init tool call records and proceeds", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{InitTool: gateTestToolPlatformInfo})
		mw := MCPSessionGateMiddleware(gate)

		nextCalled := false
		handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			nextCalled = true
			return successResult, nil
		})

		pc := NewPlatformContext("test-req")
		pc.ToolName = gateTestToolPlatformInfo
		pc.SessionID = "s1"
		ctx := WithPlatformContext(context.Background(), pc)

		result, err := handler(ctx, methodToolsCall, nil)
		require.NoError(t, err)
		assert.True(t, nextCalled)
		assert.True(t, gate.IsInitialized("s1"))
		assert.NotNil(t, result)
	})

	t.Run("exempt tool bypasses gate", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{
			InitTool:    gateTestToolPlatformInfo,
			ExemptTools: []string{gateTestToolListConns},
		})
		mw := MCPSessionGateMiddleware(gate)

		nextCalled := false
		handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			nextCalled = true
			return successResult, nil
		})

		pc := NewPlatformContext("test-req")
		pc.ToolName = gateTestToolListConns
		pc.SessionID = "s1"
		ctx := WithPlatformContext(context.Background(), pc)

		result, err := handler(ctx, methodToolsCall, nil)
		require.NoError(t, err)
		assert.True(t, nextCalled)
		assert.NotNil(t, result)
	})

	t.Run("non-exempt tool before init is gated", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{InitTool: gateTestToolPlatformInfo})
		mw := MCPSessionGateMiddleware(gate)

		nextCalled := false
		handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			nextCalled = true
			return successResult, nil
		})

		pc := NewPlatformContext("test-req")
		pc.ToolName = gateTestToolDatahubSearch
		pc.SessionID = "s1"
		ctx := WithPlatformContext(context.Background(), pc)

		result, err := handler(ctx, methodToolsCall, nil)
		require.NoError(t, err)
		assert.False(t, nextCalled, "handler should not be called when gated")

		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok)
		require.NotNil(t, callResult.GetError())
		assert.Contains(t, callResult.GetError().Error(), "SETUP_REQUIRED")
		assert.Contains(t, callResult.GetError().Error(), gateTestToolPlatformInfo)
		assert.Contains(t, callResult.GetError().Error(), gateTestToolDatahubSearch)
	})

	t.Run("non-exempt tool after init proceeds", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{InitTool: gateTestToolPlatformInfo})
		gate.RecordInit("s1")

		mw := MCPSessionGateMiddleware(gate)

		nextCalled := false
		handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			nextCalled = true
			return successResult, nil
		})

		pc := NewPlatformContext("test-req")
		pc.ToolName = gateTestToolDatahubSearch
		pc.SessionID = "s1"
		ctx := WithPlatformContext(context.Background(), pc)

		result, err := handler(ctx, methodToolsCall, nil)
		require.NoError(t, err)
		assert.True(t, nextCalled)
		assert.Equal(t, successResult, result)
	})

	t.Run("gating increments violation count", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{InitTool: gateTestToolPlatformInfo})
		mw := MCPSessionGateMiddleware(gate)

		handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return successResult, nil
		})

		for i := range 3 {
			pc := NewPlatformContext("test-req")
			pc.ToolName = gateTestToolTrinoQuery
			pc.SessionID = "s1"
			ctx := WithPlatformContext(context.Background(), pc)

			_, err := handler(ctx, methodToolsCall, nil)
			require.NoError(t, err)

			violations, _, _ := gate.Stats()
			assert.Equal(t, int64(i+1), violations)
		}
	})

	t.Run("different sessions gated independently", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{InitTool: gateTestToolPlatformInfo})
		gate.RecordInit("s1")

		mw := MCPSessionGateMiddleware(gate)

		handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return successResult, nil
		})

		// s1 initialized - should proceed
		pc1 := NewPlatformContext("test-req-1")
		pc1.ToolName = gateTestToolDatahubSearch
		pc1.SessionID = "s1"
		ctx1 := WithPlatformContext(context.Background(), pc1)

		result, err := handler(ctx1, methodToolsCall, nil)
		require.NoError(t, err)
		assert.Equal(t, successResult, result)

		// s2 not initialized - should be gated
		pc2 := NewPlatformContext("test-req-2")
		pc2.ToolName = gateTestToolDatahubSearch
		pc2.SessionID = "s2"
		ctx2 := WithPlatformContext(context.Background(), pc2)

		result, err = handler(ctx2, methodToolsCall, nil)
		require.NoError(t, err)
		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok)
		require.NotNil(t, callResult.GetError())
		assert.Contains(t, callResult.GetError().Error(), "SETUP_REQUIRED")
	})

	t.Run("new tools automatically gated", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{InitTool: gateTestToolPlatformInfo})
		mw := MCPSessionGateMiddleware(gate)

		handler := mw(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			return successResult, nil
		})

		// A hypothetical new tool should be gated without changes
		pc := NewPlatformContext("test-req")
		pc.ToolName = gateTestToolSomeFutureTool
		pc.SessionID = "s1"
		ctx := WithPlatformContext(context.Background(), pc)

		result, err := handler(ctx, methodToolsCall, nil)
		require.NoError(t, err)
		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok)
		require.NotNil(t, callResult.GetError())
		assert.Contains(t, callResult.GetError().Error(), "SETUP_REQUIRED")
	})
}

func TestCreateSessionGateError(t *testing.T) {
	result := createSessionGateError(gateTestToolPlatformInfo, gateTestToolTrinoQuery)
	callResult, ok := result.(*mcp.CallToolResult)
	require.True(t, ok)

	getErr := callResult.GetError()
	require.NotNil(t, getErr)
	assert.Contains(t, getErr.Error(), "SETUP_REQUIRED")
	assert.Contains(t, getErr.Error(), gateTestToolPlatformInfo)
	assert.Contains(t, getErr.Error(), gateTestToolTrinoQuery)
	assert.Contains(t, getErr.Error(), "query routing")

	// Verify error category
	cat := ErrorCategory(getErr)
	assert.Equal(t, ErrCategorySetupRequired, cat)
}

func TestCheckAccess(t *testing.T) {
	t.Run("init tool records and returns nil", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{InitTool: gateTestToolPlatformInfo})
		pc := &PlatformContext{ToolName: gateTestToolPlatformInfo, SessionID: "s1"}
		result := gate.checkAccess(pc)
		assert.Nil(t, result)
		assert.True(t, gate.IsInitialized("s1"))
	})

	t.Run("exempt tool returns nil without init", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{
			InitTool:    gateTestToolPlatformInfo,
			ExemptTools: []string{gateTestToolListConns},
		})
		pc := &PlatformContext{ToolName: gateTestToolListConns, SessionID: "s1"}
		result := gate.checkAccess(pc)
		assert.Nil(t, result)
	})

	t.Run("initialized session returns nil", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{InitTool: gateTestToolPlatformInfo})
		gate.RecordInit("s1")
		pc := &PlatformContext{ToolName: gateTestToolDatahubSearch, SessionID: "s1"}
		result := gate.checkAccess(pc)
		assert.Nil(t, result)
	})

	t.Run("uninitialized session returns error", func(t *testing.T) {
		gate := NewSessionGate(SessionGateConfig{InitTool: gateTestToolPlatformInfo})
		pc := &PlatformContext{ToolName: gateTestToolDatahubSearch, SessionID: "s1"}
		result := gate.checkAccess(pc)
		require.NotNil(t, result)
		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok)
		assert.Contains(t, callResult.GetError().Error(), "SETUP_REQUIRED")
	})
}
