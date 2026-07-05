package platform

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/session"
)

// mintTestPlatform builds a minimal Platform wired with a memory session store
// and the given handles config, sufficient to exercise handleInfo minting.
func mintTestPlatform(t *testing.T, handles SessionHandlesConfig) (*Platform, *session.MemoryStore) {
	t.Helper()
	store := session.NewMemoryStore(time.Hour)
	p := &Platform{
		config: &Config{
			Server:   ServerConfig{Name: "mint-test", Version: "1.0.0"},
			Sessions: SessionsConfig{Handles: handles},
		},
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: registry.NewRegistry(),
		sessionStore:    store,
	}
	return p, store
}

// ctxWithUser returns a context carrying a PlatformContext with the given user
// identity, as MCPToolCallMiddleware would set before the handler runs.
func ctxWithUser(userID string) context.Context {
	pc := middleware.NewPlatformContext("req-test")
	pc.UserID = userID
	return middleware.WithPlatformContext(context.Background(), pc)
}

// TestHandleInfo_MintsSessionHandle covers acceptance criterion 1: platform_info
// returns a non-empty session_id and a matching row exists in the session store
// with the caller's identity and a future expiry.
func TestHandleInfo_MintsSessionHandle(t *testing.T) {
	p, store := mintTestPlatform(t, SessionHandlesConfig{})

	result, _, err := p.handleInfo(ctxWithUser("user-hash-1"), nil)
	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	require.NotEmpty(t, info.SessionID, "session_id must be minted")
	assert.True(t, session.IsHandle(info.SessionID), "session_id must carry the dps_ prefix")
	require.NotEmpty(t, info.SessionExpiresAt)

	expiry, err := time.Parse(time.RFC3339, info.SessionExpiresAt)
	require.NoError(t, err)
	assert.True(t, expiry.After(time.Now()), "session_expires_at must be in the future")

	// The agent instructions must tell the model to thread the handle.
	assert.Contains(t, info.AgentInstructions, info.SessionID)
	assert.Contains(t, info.AgentInstructions, "session_id")

	// The store row exists, carries the caller's identity, and is marked as
	// minted by platform_info.
	sess, err := store.Get(context.Background(), info.SessionID)
	require.NoError(t, err)
	require.NotNil(t, sess, "a session row must exist for the minted handle")
	assert.Equal(t, "user-hash-1", sess.UserID)
	assert.Equal(t, session.MintedByPlatformInfo, sess.State[session.StateKeyMintedBy])
	assert.True(t, sess.ExpiresAt.After(time.Now()))
}

// TestHandleInfo_DisabledMintsNothing covers acceptance criterion 8: with
// handles disabled the response carries no session_id and no row is created.
func TestHandleInfo_DisabledMintsNothing(t *testing.T) {
	off := false
	p, store := mintTestPlatform(t, SessionHandlesConfig{Enabled: &off})

	result, _, err := p.handleInfo(ctxWithUser("user-hash-1"), nil)
	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	assert.Empty(t, info.SessionID, "no handle when disabled")
	assert.Empty(t, info.SessionExpiresAt)
	assert.NotContains(t, info.AgentInstructions, "SESSION HANDLE")

	sessions, err := store.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, sessions, "no session row created when disabled")
}

// failCreateStore embeds a MemoryStore but fails every Create, exercising the
// mint error path.
type failCreateStore struct {
	*session.MemoryStore
}

func (failCreateStore) Create(context.Context, *session.Session) error {
	return errors.New("insert failed")
}

// TestHandleInfo_MintCreateErrorOmitsHandle proves a store failure degrades to a
// handle-less response rather than failing the whole platform_info call.
func TestHandleInfo_MintCreateErrorOmitsHandle(t *testing.T) {
	p, _ := mintTestPlatform(t, SessionHandlesConfig{})
	p.sessionStore = failCreateStore{session.NewMemoryStore(time.Hour)}

	result, _, err := p.handleInfo(ctxWithUser("u"), nil)
	require.NoError(t, err, "a mint failure must not fail platform_info")
	info := requireInfoFromResult(t, result)
	assert.Empty(t, info.SessionID, "no handle surfaced when the store fails")
}

// TestBuildSessionResolver covers the enabled/disabled construction glue and the
// carry-forward of the legacy gate's exempt_tools.
func TestBuildSessionResolver(t *testing.T) {
	p, _ := mintTestPlatform(t, SessionHandlesConfig{})
	if p.buildSessionResolver() == nil {
		t.Error("enabled handles must produce a resolver")
	}

	// With the legacy session gate enabled, its exempt_tools carry forward.
	p.config.SessionGate = SessionGateConfig{Enabled: true, ExemptTools: []string{"list_connections"}}
	if p.buildSessionResolver() == nil {
		t.Error("resolver must build with carried-over exempt_tools")
	}

	off := false
	p.config.Sessions.Handles.Enabled = &off
	if p.buildSessionResolver() != nil {
		t.Error("disabled handles must produce a nil resolver")
	}
}

// TestAddSessionHandleSchemaMiddleware covers both branches of the list-decorator
// registration.
func TestAddSessionHandleSchemaMiddleware(t *testing.T) {
	p, _ := mintTestPlatform(t, SessionHandlesConfig{})
	p.mcpServer = mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	p.addSessionHandleSchemaMiddleware() // enabled: registers without panic

	off := false
	p.config.Sessions.Handles.Enabled = &off
	p.addSessionHandleSchemaMiddleware() // disabled: no-op
}

// TestInitSessionGate_SupersededByHandles proves the legacy transport-keyed
// session gate is not registered when explicit handles are enabled (they are
// the stronger, non-conflicting mechanism).
func TestInitSessionGate_SupersededByHandles(t *testing.T) {
	p := &Platform{config: &Config{
		SessionGate: SessionGateConfig{Enabled: true, InitTool: "platform_info"},
		Sessions:    SessionsConfig{Handles: SessionHandlesConfig{}}, // enabled by default
	}}
	p.initSessionGate()
	if p.sessionGate != nil {
		t.Error("legacy session gate must be skipped when handles are enabled")
	}
}

// TestInitSessionGate_RunsWhenHandlesDisabled proves the legacy gate still works
// for operators who explicitly disable handles.
func TestInitSessionGate_RunsWhenHandlesDisabled(t *testing.T) {
	off := false
	p := &Platform{config: &Config{
		SessionGate: SessionGateConfig{Enabled: true, InitTool: "platform_info"},
		Sessions:    SessionsConfig{Handles: SessionHandlesConfig{Enabled: &off}},
	}}
	p.initSessionGate()
	if p.sessionGate == nil {
		t.Fatal("legacy session gate must run when handles are disabled")
	}
	p.sessionGate.Stop()
}

// TestHandleInfo_CustomTTL verifies the configured handle TTL is honored.
func TestHandleInfo_CustomTTL(t *testing.T) {
	p, _ := mintTestPlatform(t, SessionHandlesConfig{TTL: 30 * time.Minute})

	result, _, err := p.handleInfo(ctxWithUser("u"), nil)
	require.NoError(t, err)
	info := requireInfoFromResult(t, result)

	expiry, err := time.Parse(time.RFC3339, info.SessionExpiresAt)
	require.NoError(t, err)
	// Expiry should be ~30m out, comfortably under the 8h default.
	assert.True(t, expiry.Before(time.Now().Add(time.Hour)), "expiry honors the 30m TTL")
	assert.True(t, expiry.After(time.Now().Add(20*time.Minute)))
	assert.False(t, strings.HasPrefix(info.SessionID, "stdio"))
}
