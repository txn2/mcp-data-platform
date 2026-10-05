package searchfed

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/internal/producedview"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// outputProducers lists two assets one script produced.
type outputProducers struct{ producedby.Store }

func (outputProducers) ListByProducer(context.Context, string, string, int) ([]producedby.Row, error) {
	at := time.Now()
	return []producedby.Row{
		{TargetKind: producedby.TargetAsset, TargetID: "mine", FirstWriteAt: at, LastWriteAt: at},
		{TargetKind: producedby.TargetAsset, TargetID: "theirs", FirstWriteAt: at, LastWriteAt: at},
		{TargetKind: producedby.TargetResource, TargetID: "res-1", FirstWriteAt: at, LastWriteAt: at},
	}, nil
}

// outputAssets holds one asset of the caller's and one of somebody else's.
type outputAssets struct{ portal.AssetStore }

var outputAssetRows = map[string]*portal.Asset{
	"mine":   {ID: "mine", Name: "Bob's Report", OwnerID: "u-bob", OwnerEmail: "bob@example.com"},
	"theirs": {ID: "theirs", Name: "Jane's Report", OwnerID: "u-jane", OwnerEmail: "jane@example.com"},
}

func (outputAssets) Get(_ context.Context, id string) (*portal.Asset, error) {
	return outputAssetRows[id], nil
}

func (outputAssets) GetByIDs(context.Context, []string) (map[string]*portal.Asset, error) {
	return outputAssetRows, nil
}

// outputResources holds one global resource.
type outputResources struct{ resource.Store }

func (outputResources) Get(context.Context, string) (*resource.Resource, error) {
	return &resource.Resource{ID: "res-1", DisplayName: "Extract", Scope: resource.ScopeGlobal}, nil
}

// TestScriptOutputsNamesWhatTheCallerOpens proves a fetched script lists the
// files the caller can open by fetch's own rules, with a reference fetch
// dereferences, and counts the rest unnamed (#2027).
func TestScriptOutputsNamesWhatTheCallerOpens(t *testing.T) {
	assert.Nil(t, newScriptOutputs(Config{}), "no producer record lists nothing")

	o := newScriptOutputs(Config{
		Producers: outputProducers{}, AssetStore: outputAssets{}, ResourceStore: outputResources{},
	})
	require.NotNil(t, o)

	set, err := o.Outputs(context.Background(), "script-1", knowledge.Caller{UserID: "u-bob", Email: "bob@example.com"})
	require.NoError(t, err)
	assert.False(t, set.More)
	assert.Equal(t, 1, set.Hidden, "Jane's report is counted, not named")
	assert.Equal(t, []knowledge.ScriptOutput{
		{Kind: "asset", ID: "mine", Name: "Bob's Report", Reference: "mcp:asset:mine"},
		{Kind: "resource", ID: "res-1", Name: "Extract", Reference: "mcp:resource:res-1"},
	}, set.Open)

	admin, err := o.Outputs(context.Background(), "script-1", knowledge.Caller{Email: "root@example.com", IsAdmin: true})
	require.NoError(t, err)
	assert.Len(t, admin.Open, 3)
	assert.Zero(t, admin.Hidden)
}

func TestOutputReferenceNamesEachKind(t *testing.T) {
	assert.Equal(t, "mcp:collection:c1", outputReference(producedviewItem(producedby.TargetCollection, "c1")))
	assert.Equal(t, "mcp:asset:a1", outputReference(producedviewItem(producedby.TargetAsset, "a1")))
	assert.Equal(t, "mcp:resource:r1", outputReference(producedviewItem(producedby.TargetResource, "r1")))
}

func producedviewItem(kind, id string) producedview.Item {
	return producedview.Item{TargetKind: kind, TargetID: id}
}

// TestNew_ScriptProviderListsOutputsWhenTheRecordIsWired proves the
// composition hands the scripts provider its output lister.
func TestNew_ScriptProviderListsOutputsWhenTheRecordIsWired(t *testing.T) {
	h := New(Config{
		ToolkitName: "default",
		ScriptStore: stubScriptStore{},
		Producers:   outputProducers{},
		Registry:    registry.NewRegistry(),
	})
	assert.Contains(t, providerNames(t, h), knowledge.SourceScripts)
}

// TestScriptOutputsJudgeARunByThePersonItActsFor proves a run sees its
// author's own outputs and nothing an administrator's reach would add.
func TestScriptOutputsJudgeARunByThePersonItActsFor(t *testing.T) {
	o := newScriptOutputs(Config{Producers: outputProducers{}, AssetStore: outputAssets{}, ResourceStore: outputResources{}})
	set, err := o.Outputs(context.Background(), "script-1", knowledge.Caller{
		UserID: "script:daily", Email: "script:daily", OnBehalfOf: "bob@example.com", IsAdmin: true,
	})
	require.NoError(t, err)
	names := make([]string, 0, len(set.Open))
	for _, out := range set.Open {
		names = append(names, out.ID)
	}
	assert.Contains(t, names, "mine")
	assert.NotContains(t, names, "theirs")
	assert.Equal(t, 1, set.Hidden)
}

// TestSharedAssetReadsTheShareGraph proves fetch opens an asset shared with
// a person, and a deployment with no shares opens none that way.
func TestSharedAssetReadsTheShareGraph(t *testing.T) {
	assert.Nil(t, sharedAsset(nil))
	lookup := sharedAsset(outputShares{})
	theirs := outputAssetRows["theirs"]
	assert.True(t, lookup(context.Background(), theirs, "u-bob", "bob@example.com"))
	assert.False(t, lookup(context.Background(), theirs, "u-carol", "carol@example.com"))
}

// outputShares shares "theirs" with bob.
type outputShares struct{ portal.ShareStore }

func (outputShares) ListByAsset(_ context.Context, id string) ([]portal.Share, error) {
	if id == "theirs" {
		return []portal.Share{{SharedWithEmail: "bob@example.com", Permission: portal.PermissionViewer}}, nil
	}
	return nil, nil
}

func (outputShares) GetUserAssetPermissionViaCollection(context.Context, string, string, string) (portal.SharePermission, error) {
	return "", nil
}
