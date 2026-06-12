//go:build integration

package platform

// Real-Postgres proof for the Phase 2 feedback bridge (#602). It drives the
// real assembled MCP server (capture_insight over an in-memory transport) wired
// to the real Postgres thread store as the ThreadLinker, then verifies by
// READ-BACK that the tool call wrote insight_id onto the thread, appended an
// insight_linked event, and resolved the thread. It then proves the reverse
// direction: a changeset whose source_insight_ids contains that insight id is
// retrievable through the same JSONB-containment query getThreadChain uses, so
// the thread -> insight -> changeset -> target_urn chain holds end to end.
//
// This exercises behavior mocks cannot: the real LinkInsight transaction, the
// real event read-back, and the real `source_insight_ids @> ?::jsonb` filter.
// Run under `make test-realdb`.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
	portalkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

func TestRealDB_FeedbackBridge_CaptureLinksThreadAndChain(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()

	const owner = "550e8400-e29b-41d4-a716-446655440000"

	// Real stores.
	assetStore := portal.NewPostgresAssetStore(db)
	threadStore := portal.NewPostgresThreadStore(db)
	// Production always backs insights with the memory store (migration 000031
	// drops knowledge_insights); mirror that exactly so we exercise the real path.
	insightStore := knowledgekit.NewMemoryInsightAdapter(memory.NewPostgresStore(db))
	csStore := knowledgekit.NewPostgresChangesetStore(db)

	// Seed an asset and a feedback thread on it.
	require.NoError(t, assetStore.Insert(ctx, portal.Asset{
		ID: "asset_bridge", OwnerID: owner, OwnerEmail: "owner@example.com", Name: "asset_bridge",
		ContentType: "text/markdown", S3Bucket: "b", S3Key: "k", Tags: []string{}, CurrentVersion: 1,
	}))
	_, err := threadStore.CreateThread(ctx,
		portal.Thread{
			ID: "thr_bridge", Kind: portal.ThreadKindCorrection, TargetType: "asset",
			AssetID: "asset_bridge", RequiresResolution: true,
			AuthorID: "sme", AuthorEmail: "sme@example.com",
		},
		portal.ThreadEvent{
			ID: "evt_bridge_1", ThreadID: "thr_bridge", EventType: portal.EventTypeComment,
			AuthorID: "sme", AuthorEmail: "sme@example.com", Body: "this column is mislabeled",
		},
	)
	require.NoError(t, err)

	// A second asset+thread owned by a DIFFERENT user. The caller below is the
	// owner of asset_bridge, not this one, so the bridge must refuse to link it.
	const otherOwner = "11111111-2222-3333-4444-555555555555"
	require.NoError(t, assetStore.Insert(ctx, portal.Asset{
		ID: "asset_other", OwnerID: otherOwner, OwnerEmail: "other@example.com", Name: "asset_other",
		ContentType: "text/markdown", S3Bucket: "b", S3Key: "k", Tags: []string{}, CurrentVersion: 1,
	}))
	_, err = threadStore.CreateThread(ctx,
		portal.Thread{
			ID: "thr_other", Kind: portal.ThreadKindCorrection, TargetType: "asset",
			AssetID: "asset_other", AuthorID: "sme", AuthorEmail: "sme@example.com",
		},
		portal.ThreadEvent{
			ID: "evt_other_1", ThreadID: "thr_other", EventType: portal.EventTypeComment,
			AuthorID: "sme", AuthorEmail: "sme@example.com", Body: "unrelated",
		},
	)
	require.NoError(t, err)

	// Assemble the real MCP server with capture_insight, wired to the real
	// portal toolkit as the bridge so thread linking is gated by the same
	// owns-or-edit access check as resolve_thread.
	portalTk := portalkit.New(portalkit.Config{
		Name: "test", S3Bucket: "b",
		AssetStore:      assetStore,
		ThreadStore:     threadStore,
		ShareStore:      portal.NewNoopShareStore(),
		CollectionStore: portal.NewNoopCollectionStore(),
	})
	tk, err := knowledgekit.New("test", insightStore)
	require.NoError(t, err)
	tk.SetThreadLinker(portalTk)

	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)
	// Inject the asset owner's identity the way the real MCP auth middleware
	// would, so the bridge's per-thread authorization can run.
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			ctx = middleware.WithPlatformContext(ctx, &middleware.PlatformContext{
				UserID: owner, UserEmail: "owner@example.com",
			})
			return next(ctx, method, req)
		}
	})
	tk.RegisterTools(server)

	ct, st := mcp.NewInMemoryTransports()
	serverSess, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	defer func() { _ = serverSess.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	clientSess, err := client.Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer func() { _ = clientSess.Close() }()

	// Call capture_insight with thread_ids through the real protocol path.
	callRes, err := clientSess.CallTool(ctx, &mcp.CallToolParams{
		Name: "capture_insight",
		Arguments: map[string]any{
			"category":     "data_quality",
			"insight_text": "The churn column actually measures monthly active retention.",
			// Includes a thread on an asset the caller does NOT own: the bridge
			// must link only the owned one and report the other as unlinked.
			"thread_ids": []string{"thr_bridge", "thr_other"},
		},
	})
	require.NoError(t, err)
	require.False(t, callRes.IsError, "capture_insight failed: %+v", callRes.Content)

	tc, ok := callRes.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var out struct {
		InsightID         string   `json:"insight_id"`
		LinkedThreadCount int      `json:"linked_thread_count"`
		UnlinkedThreadIDs []string `json:"unlinked_thread_ids"`
	}
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	require.NotEmpty(t, out.InsightID)
	assert.Equal(t, 1, out.LinkedThreadCount)
	// thr_other belongs to another owner: the authorization gate refused it.
	assert.Equal(t, []string{"thr_other"}, out.UnlinkedThreadIDs)

	// And the unauthorized thread was NOT mutated.
	otherThread, err := threadStore.GetThread(ctx, "thr_other")
	require.NoError(t, err)
	assert.Empty(t, otherThread.InsightID)
	assert.NotEqual(t, portal.ThreadStatusResolved, otherThread.Status)

	// READ-BACK 1: the tool call set insight_id and resolved the thread.
	gotThread, err := threadStore.GetThread(ctx, "thr_bridge")
	require.NoError(t, err)
	assert.Equal(t, out.InsightID, gotThread.InsightID)
	assert.Equal(t, portal.ThreadStatusResolved, gotThread.Status)

	// READ-BACK 2: an insight_linked event was appended, readable through the store.
	events, err := threadStore.ListEvents(ctx, "thr_bridge")
	require.NoError(t, err)
	var sawLinked bool
	for _, e := range events {
		if e.EventType == portal.EventTypeInsightLinked {
			sawLinked = true
		}
	}
	assert.True(t, sawLinked, "expected an insight_linked event on the thread timeline")

	// Reverse direction: a changeset sourced from that insight (as apply_knowledge
	// records) is retrievable by the same JSONB-containment query getThreadChain
	// uses, closing thread -> insight -> changeset -> target_urn.
	require.NoError(t, csStore.InsertChangeset(ctx, knowledgekit.Changeset{
		ID:               "cs_bridge",
		TargetURN:        "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.churn,PROD)",
		ChangeType:       "update_description",
		SourceInsightIDs: []string{out.InsightID},
		AppliedBy:        "admin",
	}))

	chain, _, err := csStore.ListChangesets(ctx, knowledgekit.ChangesetFilter{SourceInsightID: out.InsightID})
	require.NoError(t, err)
	require.Len(t, chain, 1)
	assert.Equal(t, "cs_bridge", chain[0].ID)
	assert.Equal(t, "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.churn,PROD)", chain[0].TargetURN)
}
