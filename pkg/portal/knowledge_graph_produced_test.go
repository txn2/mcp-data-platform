package portal

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// graphScriptID is the cited script in these tests, in the form scripts.id takes.
const graphScriptID = "6f1c0a52-8d8e-4f7b-9a3e-2b8c1d0e4f55"

// producedAssets resolves assets per id: the viewer's own, one shared with
// them, one private to somebody else, and one deleted.
type producedAssets struct{ AssetStore }

var producedAssetRows = func() map[string]*Asset {
	gone := time.Now()
	return map[string]*Asset{
		"own":     {ID: "own", Name: "Viewer's Report", OwnerID: kpViewer.UserID, OwnerEmail: kpViewer.Email},
		"shared":  {ID: "shared", Name: "Shared Report", OwnerID: "jane", OwnerEmail: "jane@example.com"},
		"private": {ID: "private", Name: "Confidential Forecast", OwnerID: "jane", OwnerEmail: "jane@example.com"},
		"deleted": {ID: "deleted", Name: "Old Report", OwnerID: "jane", OwnerEmail: "jane@example.com", DeletedAt: &gone},
	}
}()

func (producedAssets) Get(_ context.Context, id string) (*Asset, error) {
	if a, ok := producedAssetRows[id]; ok {
		return a, nil
	}
	return nil, sql.ErrNoRows
}

func (producedAssets) GetByIDs(_ context.Context, ids []string) (map[string]*Asset, error) {
	out := map[string]*Asset{}
	for _, id := range ids {
		if a, ok := producedAssetRows[id]; ok {
			out[id] = a
		}
	}
	return out, nil
}

// producedShares shares "shared" with the viewer.
type producedShares struct{ ShareStore }

func (producedShares) ListByAsset(_ context.Context, id string) ([]Share, error) {
	if id == "shared" {
		return []Share{{SharedWithEmail: kpViewer.Email, Permission: PermissionViewer}}, nil
	}
	return nil, nil
}

func (producedShares) GetUserAssetPermissionViaCollection(context.Context, string, string, string) (SharePermission, error) {
	return "", nil
}

// producedRows is what the cited script wrote.
type producedRows struct {
	producedby.Store
	err error
}

func (p producedRows) ListByProducer(_ context.Context, _, id string, _ int) ([]producedby.Row, error) {
	if p.err != nil {
		return nil, p.err
	}
	if id != graphScriptID {
		return nil, nil
	}
	at := time.Now()
	row := func(kind, target string) producedby.Row {
		return producedby.Row{TargetKind: kind, TargetID: target, FirstWriteAt: at, LastWriteAt: at}
	}
	return []producedby.Row{
		row(producedby.TargetAsset, "own"), row(producedby.TargetAsset, "shared"),
		row(producedby.TargetAsset, "private"), row(producedby.TargetAsset, "deleted"),
		row(producedby.TargetResource, "res-global"),
	}, nil
}

// producedResources holds one global resource.
type producedResources struct{ fakeResourceReader }

type fakeResourceReader interface {
	GetByURI(context.Context, string) (*resource.Resource, error)
	GetByIDs(context.Context, []string) (map[string]*resource.Resource, error)
}

func (producedResources) Get(_ context.Context, id string) (*resource.Resource, error) {
	if id == "res-global" {
		return &resource.Resource{ID: id, DisplayName: "Regional Extract", Scope: resource.ScopeGlobal}, nil
	}
	return nil, sql.ErrNoRows
}

func producedGraphHandler(user *User, producers producedby.Store) *Handler {
	pages := []knowledgepage.Page{{ID: "kp1", Title: "Revenue", Tags: []string{}}}
	refs := []knowledgepage.EntityRef{
		{PageID: "kp1", TargetType: knowledgepage.RefTargetScript, ScriptID: graphScriptID, Source: knowledgepage.RefSourceInline},
	}
	deps := Deps{
		KnowledgePageStore: newGraphStore(pages, refs),
		AssetStore:         producedAssets{},
		ShareStore:         producedShares{},
		ResourceReader:     producedResources{},
		Producers:          producers,
		ScriptRefs:         func(context.Context, string) (string, bool, error) { return "Daily Sales Report", true, nil },
		AdminRoles:         []string{"admin"},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	return NewHandler(deps, testAuthMiddleware(user))
}

// TestKnowledgeGraph_ACitedScriptIsDrawnWithWhatTheViewerCanOpen proves #1985:
// a script a page cites is drawn for every reader, with an edge to each file
// its runs wrote that the reader can open, and the files they cannot open are
// counted on the script's node without being named (#2027). A file since
// deleted is neither.
func TestKnowledgeGraph_ACitedScriptIsDrawnWithWhatTheViewerCanOpen(t *testing.T) {
	resp := fetchGraph(t, producedGraphHandler(kpViewer, producedRows{}), "")
	scriptURN := "mcp:script:" + graphScriptID

	sc, ok := resp.nodeByID(scriptURN)
	require.True(t, ok, "a cited script is drawn for every reader")
	assert.Equal(t, "Daily Sales Report", sc.Label)
	assert.Equal(t, 1, sc.HiddenOutputs, "the private forecast is counted, the deleted report is not")

	for target, kind := range map[string]string{
		"mcp:asset:own": "asset", "mcp:asset:shared": "asset", "mcp:resource:res-global": "resource",
	} {
		assert.Contains(t, resp.Edges, knowledgeGraphEdge{
			Source: scriptURN, Target: target, Type: kind, RefSource: refSourceProduced,
		}, target)
	}
	shared, ok := resp.nodeByID("mcp:asset:shared")
	require.True(t, ok)
	assert.Equal(t, "Shared Report", shared.Label)

	body := mustJSON(t, resp)
	assert.NotContains(t, body, "Confidential Forecast", "an output not shared with the reader is never named")
	assert.NotContains(t, body, "mcp:asset:private")
	assert.NotContains(t, body, "mcp:asset:deleted")

	admin := fetchGraph(t, producedGraphHandler(kpAdmin, producedRows{}), "")
	sc, _ = admin.nodeByID(scriptURN)
	assert.Zero(t, sc.HiddenOutputs, "an administrator opens every output")
	_, ok = admin.nodeByID("mcp:asset:private")
	assert.True(t, ok)
}

// TestKnowledgeGraph_ProducedEdgesDegrade covers the deployments that cannot
// read what a script produced: no producer record, or a read that fails. The
// script is still drawn; it simply has no produced edges.
func TestKnowledgeGraph_ProducedEdgesDegrade(t *testing.T) {
	for name, producers := range map[string]producedby.Store{
		"no producer record": nil,
		"a failed read":      producedRows{err: errors.New("down")},
	} {
		t.Run(name, func(t *testing.T) {
			resp := fetchGraph(t, producedGraphHandler(kpViewer, producers), "")
			_, ok := resp.nodeByID("mcp:script:" + graphScriptID)
			assert.True(t, ok)
			for _, e := range resp.Edges {
				assert.NotEqual(t, refSourceProduced, e.RefSource)
			}
		})
	}
}

func refResolveHandler(user *User) *Handler {
	deps := Deps{
		KnowledgePageStore: &mockKnowledgePageStore{page: livePage()},
		AssetStore:         producedAssets{},
		ShareStore:         producedShares{},
		ResourceReader:     producedResources{},
		AdminRoles:         []string{"admin"},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	return NewHandler(deps, testAuthMiddleware(user))
}

// TestResolveRef_ADeletedAssetIsNotLive pins the citation of a deleted asset:
// it no longer resolves as an asset the reader can open, administrator or
// not, though the asset store still returns its row.
func TestResolveRef_ADeletedAssetIsNotLive(t *testing.T) {
	for _, u := range []*User{kpViewer, kpAdmin} {
		got := resolveOne(t, refResolveHandler(u), "mcp:asset:deleted")
		assert.False(t, got.Accessible, u.Email)
		assert.NotContains(t, got.Label, "Old Report")
	}
	assert.True(t, resolveOne(t, refResolveHandler(kpAdmin), "mcp:asset:private").Accessible,
		"an administrator opens any live asset")
	assert.False(t, resolveOne(t, refResolveHandler(kpViewer), "mcp:asset:private").Accessible)
	assert.True(t, resolveOne(t, refResolveHandler(kpViewer), "mcp:asset:shared").Accessible)
}

// TestResolveRef_AResourceResolvesByItsScope proves a resource reference is
// judged by the resource scopes rather than resolved as open to everyone.
func TestResolveRef_AResourceResolvesByItsScope(t *testing.T) {
	got := resolveOne(t, refResolveHandler(kpViewer), "mcp:resource:res-global")
	assert.True(t, got.Accessible)
	assert.Equal(t, "Regional Extract", got.Label)

	missing := resolveOne(t, refResolveHandler(kpViewer), "mcp:resource:res-unknown")
	assert.False(t, missing.Accessible)

	unwired := NewHandler(Deps{
		KnowledgePageStore: &mockKnowledgePageStore{page: livePage()},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, testAuthMiddleware(kpViewer))
	assert.False(t, resolveOne(t, unwired, "mcp:resource:res-global").Accessible)
}
