//go:build integration

package platform

// Real-Postgres proof for the session-start notice digest (#1278). It drives the
// real assembled MCP server — platform_info registered on an mcp.Server, called
// over an in-memory transport by a real client — wired to the real Postgres
// asset, share and thread stores and the real watermark table, then verifies the
// digest that comes back and the fact that a second call reports nothing.
//
// This exercises what mocks cannot: the ActivityAfter EXISTS against the real
// event timeline, the polymorphic since-query over portal_shares, and the
// watermark upsert that makes delivery single-shot. Run under `make test-realdb`.

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/portalstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	promptpostgres "github.com/txn2/mcp-data-platform/pkg/prompt/postgres"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

const (
	noticeOwnerID    = "550e8400-e29b-41d4-a716-446655440000"
	noticeOwnerEmail = "owner@example.com"
	noticeSMEEmail   = "sme@example.com"
)

func TestRealDB_PlatformInfoNoticesBriefOnceThenGoQuiet(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	handle := portalstore.New(db, nil, nil, portalstore.Config{Name: "test"})
	require.NotNil(t, handle)
	seedNoticeFixtures(ctx, t, handle)

	p := newNoticePlatform(t, handle)
	client := connectNoticeClient(ctx, t, p)

	// FIRST SESSION: the digest names the SME's thread and the shared prompt.
	info := callPlatformInfo(ctx, t, client)
	require.NotNil(t, info.Notices, "the owner has unaddressed feedback and a new share")

	require.Len(t, info.Notices.Feedback, 1)
	got := info.Notices.Feedback[0]
	assert.Equal(t, "thr_notice", got.ThreadID)
	assert.Equal(t, "asset_notice", got.AssetID)
	assert.Equal(t, "Q3 revenue", got.AssetName)
	assert.Equal(t, "mcp:asset:asset_notice", got.AssetReference)
	assert.Equal(t, noticeSMEEmail, got.AuthorEmail)
	assert.Equal(t, 1, info.Notices.FeedbackTotal)

	require.Len(t, info.Notices.NewShares, 1)
	assert.Equal(t, "asset", info.Notices.NewShares[0].Kind)
	assert.Equal(t, "asset_shared", info.Notices.NewShares[0].ID)
	assert.Equal(t, "mcp:asset:asset_shared", info.Notices.NewShares[0].Reference)
	assert.Equal(t, "Board pack", info.Notices.NewShares[0].Name)

	// The agent is told to relay it, in the same response.
	assert.Contains(t, info.AgentInstructions, "notices.feedback")
	assert.Contains(t, info.AgentInstructions, "notices.new_shares")

	// SECOND SESSION: delivery advanced the watermark, so nothing is repeated.
	assert.Nil(t, callPlatformInfo(ctx, t, client).Notices,
		"the digest was delivered; a later session must not be briefed on it again")

	// A NEW EVENT from someone else re-raises the same thread.
	_, err := handle.ThreadStore().AppendEvent(ctx, portal.ThreadEvent{
		ID: "evt_notice_2", ThreadID: "thr_notice", EventType: portal.EventTypeComment,
		AuthorID: "sme", AuthorEmail: noticeSMEEmail, Body: "still wrong",
	})
	require.NoError(t, err)

	again := callPlatformInfo(ctx, t, client)
	require.NotNil(t, again.Notices)
	require.Len(t, again.Notices.Feedback, 1)
	assert.Equal(t, "thr_notice", again.Notices.Feedback[0].ThreadID)
	assert.Empty(t, again.Notices.NewShares, "no new share arrived since the last briefing")
}

// The owner's own reply is not feedback awaiting the owner: it must not raise a
// notice, even though it is activity on the thread after the watermark.
func TestRealDB_PlatformInfoNoticesIgnoreTheCallersOwnReply(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	handle := portalstore.New(db, nil, nil, portalstore.Config{Name: "test"})
	require.NotNil(t, handle)
	seedNoticeFixtures(ctx, t, handle)

	p := newNoticePlatform(t, handle)
	client := connectNoticeClient(ctx, t, p)

	require.NotNil(t, callPlatformInfo(ctx, t, client).Notices)

	// The owner answers their own thread. That is the owner acting, not
	// feedback arriving.
	_, err := handle.ThreadStore().AppendEvent(ctx, portal.ThreadEvent{
		ID: "evt_notice_own", ThreadID: "thr_notice", EventType: portal.EventTypeComment,
		AuthorID: noticeOwnerID, AuthorEmail: noticeOwnerEmail, Body: "fixing it now",
	})
	require.NoError(t, err)

	assert.Nil(t, callPlatformInfo(ctx, t, client).Notices,
		"a caller's own reply must never come back to them as a notice")
}

// An asset shared with the caller BEFORE their watermark is not new to them.
func TestRealDB_PlatformInfoNoticesOnlyReportSharesAfterTheWatermark(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	handle := portalstore.New(db, nil, nil, portalstore.Config{Name: "test"})
	require.NotNil(t, handle)
	seedNoticeFixtures(ctx, t, handle)

	p := newNoticePlatform(t, handle)
	client := connectNoticeClient(ctx, t, p)

	first := callPlatformInfo(ctx, t, client)
	require.NotNil(t, first.Notices)
	require.Len(t, first.Notices.NewShares, 1)

	// A second share, made after the briefing, is the only one reported next.
	require.NoError(t, handle.AssetStore().Insert(ctx, portal.Asset{
		ID: "asset_later", OwnerID: "other-owner", OwnerEmail: "lead@example.com", Name: "Later pack",
		ContentType: "text/markdown", S3Bucket: "b", S3Key: "k", Tags: []string{}, CurrentVersion: 1,
	}))
	require.NoError(t, handle.ShareStore().Insert(ctx, portal.Share{
		ID: "shr_later", AssetID: "asset_later", Token: "tok_later", CreatedBy: "lead@example.com",
		SharedWithEmail: noticeOwnerEmail, Permission: portal.PermissionViewer,
	}))

	second := callPlatformInfo(ctx, t, client)
	require.NotNil(t, second.Notices)
	require.Len(t, second.Notices.NewShares, 1,
		"the share delivered in the first briefing must not be delivered twice")
	assert.Equal(t, "asset_later", second.Notices.NewShares[0].ID)
}

// --- harness ---

// seedNoticeFixtures writes the owner's asset with a thread someone else opened
// on it, plus an asset another person shared with the owner.
func seedNoticeFixtures(ctx context.Context, t *testing.T, h *portalstore.Handle) {
	t.Helper()
	require.NoError(t, h.AssetStore().Insert(ctx, portal.Asset{
		ID: "asset_notice", OwnerID: noticeOwnerID, OwnerEmail: noticeOwnerEmail, Name: "Q3 revenue",
		ContentType: "text/markdown", S3Bucket: "b", S3Key: "k", Tags: []string{}, CurrentVersion: 1,
	}))
	_, err := h.ThreadStore().CreateThread(ctx,
		portal.Thread{
			ID: "thr_notice", Kind: portal.ThreadKindCorrection, TargetType: "asset",
			AssetID: "asset_notice", RequiresResolution: true,
			AuthorID: "sme", AuthorEmail: noticeSMEEmail, Title: "wrong currency",
		},
		portal.ThreadEvent{
			ID: "evt_notice_1", ThreadID: "thr_notice", EventType: portal.EventTypeComment,
			AuthorID: "sme", AuthorEmail: noticeSMEEmail, Body: "these are dollars, not euros",
		},
	)
	require.NoError(t, err)

	// A thread the OWNER opened on their own asset is not feedback awaiting
	// them and must never appear in their digest.
	_, err = h.ThreadStore().CreateThread(ctx,
		portal.Thread{
			ID: "thr_own", Kind: portal.ThreadKindComment, TargetType: "asset",
			AssetID: "asset_notice", AuthorID: noticeOwnerID, AuthorEmail: noticeOwnerEmail,
		},
		portal.ThreadEvent{
			ID: "evt_own_1", ThreadID: "thr_own", EventType: portal.EventTypeComment,
			AuthorID: noticeOwnerID, AuthorEmail: noticeOwnerEmail, Body: "note to self",
		},
	)
	require.NoError(t, err)

	require.NoError(t, h.AssetStore().Insert(ctx, portal.Asset{
		ID: "asset_shared", OwnerID: "other-owner", OwnerEmail: "lead@example.com", Name: "Board pack",
		ContentType: "text/markdown", S3Bucket: "b", S3Key: "k", Tags: []string{}, CurrentVersion: 1,
	}))
	require.NoError(t, h.ShareStore().Insert(ctx, portal.Share{
		ID: "shr_notice", AssetID: "asset_shared", Token: "tok_notice", CreatedBy: "lead@example.com",
		SharedWithEmail: noticeOwnerEmail, Permission: portal.PermissionViewer,
	}))
}

// seedEditorShare has someone OTHER than the owner grant access, which is the
// case where naming the owner as the sharer would name a person who did nothing.
func seedEditorShare(ctx context.Context, t *testing.T, h *portalstore.Handle) {
	t.Helper()
	require.NoError(t, h.AssetStore().Insert(ctx, portal.Asset{
		ID: "asset_relayed", OwnerID: "third-owner", OwnerEmail: "author@example.com", Name: "Relayed pack",
		ContentType: "text/markdown", S3Bucket: "b", S3Key: "k", Tags: []string{}, CurrentVersion: 1,
	}))
	require.NoError(t, h.ShareStore().Insert(ctx, portal.Share{
		ID: "shr_relayed", AssetID: "asset_relayed", Token: "tok_relayed", CreatedBy: "editor@example.com",
		SharedWithEmail: noticeOwnerEmail, Permission: portal.PermissionViewer,
	}))
}

// seedOtherKindShares adds a collection share and a prompt share to the same
// recipient. portal_shares is one polymorphic table and one query serves all
// three kinds, so the kind discrimination and the per-kind name resolution are
// only proven by having all three present at once.
func seedOtherKindShares(ctx context.Context, t *testing.T, db *sql.DB, h *portalstore.Handle) {
	t.Helper()
	require.NoError(t, h.CollectionStore().Insert(ctx, portal.Collection{
		ID: "col_notice", OwnerID: "other-owner", OwnerEmail: "lead@example.com", Name: "Weekly board pack",
	}))
	require.NoError(t, h.ShareStore().Insert(ctx, portal.Share{
		ID: "shr_coll", CollectionID: "col_notice", Token: "tok_coll", CreatedBy: "lead@example.com",
		SharedWithEmail: noticeOwnerEmail, Permission: portal.PermissionEditor,
	}))

	prm := &prompt.Prompt{
		Name: "daily-sales-report", DisplayName: "Daily Sales Report", Content: "Analyze sales.",
		Scope: prompt.ScopePersonal, OwnerEmail: "lead@example.com",
		Source: prompt.SourceOperator, Enabled: true,
	}
	require.NoError(t, promptpostgres.New(db).Create(ctx, prm))
	require.NoError(t, h.ShareStore().Insert(ctx, portal.Share{
		ID: "shr_prompt", PromptID: prm.ID, Token: "tok_prompt", CreatedBy: "lead@example.com",
		SharedWithEmail: noticeOwnerEmail, Permission: portal.PermissionViewer,
	}))
}

// TestRealDB_PlatformInfoNoticesNameEveryShareKind proves the one polymorphic
// since-query answers for assets, collections and prompts alike, resolving each
// kind's display name and its fetchable reference.
func TestRealDB_PlatformInfoNoticesNameEveryShareKind(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	handle := portalstore.New(db, nil, nil, portalstore.Config{Name: "test"})
	require.NotNil(t, handle)
	seedNoticeFixtures(ctx, t, handle)
	seedOtherKindShares(ctx, t, db, handle)
	seedEditorShare(ctx, t, handle)

	info := callPlatformInfo(ctx, t, connectNoticeClient(ctx, t, newNoticePlatform(t, handle)))
	require.NotNil(t, info.Notices)
	require.Len(t, info.Notices.NewShares, 4)

	byKind := map[string]string{}
	refs := map[string]string{}
	for _, s := range info.Notices.NewShares {
		byKind[s.Kind] = s.Name
		refs[s.Kind] = s.Reference
	}
	assert.Equal(t, "Board pack", byKind["asset"])
	assert.Equal(t, "Weekly board pack", byKind["collection"])
	assert.Equal(t, "Daily Sales Report", byKind["prompt"],
		"a prompt's display name is what a person calls it; the slug is the fallback")
	assert.Equal(t, "mcp:asset:asset_shared", refs["asset"])
	sharers := map[string]string{}
	for _, sh := range info.Notices.NewShares {
		sharers[sh.ID] = sh.SharedBy
	}
	assert.Equal(t, "lead@example.com", sharers["asset_shared"])
	assert.Equal(t, "editor@example.com", sharers["asset_relayed"],
		"who shared it is the person who made the grant, not whoever owns the artifact")
	assert.Equal(t, "mcp:collection:col_notice", refs["collection"])
	assert.Contains(t, refs["prompt"], "mcp:prompt:")
	assert.False(t, info.Notices.NewSharesTruncated)
}

// newNoticePlatform builds the platform facade around the real portal store
// handle, with everything platform_info needs and nothing it does not.
func newNoticePlatform(t *testing.T, h *portalstore.Handle) *Platform {
	t.Helper()
	cfg := &Config{Server: ServerConfig{Name: "test-platform", Version: "1.0.0"}}
	cfg.Purpose.Enabled = new(false)
	return &Platform{
		config:          cfg,
		personaRegistry: persona.NewRegistry(),
		toolkitRegistry: registry.NewRegistry(),
		portalStore:     h,
	}
}

// connectNoticeClient registers platform_info on a real MCP server, injects the
// owner's identity the way the auth middleware would, and connects a real
// client over an in-memory transport.
func connectNoticeClient(ctx context.Context, t *testing.T, p *Platform) *mcp.ClientSession {
	t.Helper()
	p.mcpServer = mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	p.mcpServer.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			return next(middleware.WithPlatformContext(ctx, &middleware.PlatformContext{
				UserID: noticeOwnerID, UserEmail: noticeOwnerEmail, AuthType: "oidc",
			}), method, req)
		}
	})
	p.registerInfoTool()

	ct, st := mcp.NewInMemoryTransports()
	serverSess, err := p.mcpServer.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSess.Close() })

	clientSess, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil).
		Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSess.Close() })
	return clientSess
}

// callPlatformInfo calls the tool through the real protocol path and decodes
// the response the agent would receive.
func callPlatformInfo(ctx context.Context, t *testing.T, client *mcp.ClientSession) Info {
	t.Helper()
	// The watermark is stamped from the wall clock, so a second call in the
	// same instant could read its own delivery as "not after". A moment of
	// space keeps the ordering assertions about ordering.
	time.Sleep(2 * time.Millisecond)

	res, err := client.CallTool(ctx, &mcp.CallToolParams{Name: defaultInitTool})
	require.NoError(t, err)
	require.False(t, res.IsError, "platform_info failed: %+v", res.Content)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)

	var info Info
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &info))
	return info
}
