package portal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// scriptOutputAsset is what a managed script's portal output looks like on
// disk: the script principal as its owner id, and the address of the person who
// owns the script as owner_email.
func scriptOutputAsset() portal.Asset {
	return portal.Asset{
		ID: "a-script", OwnerID: "script:weekly-revenue", OwnerEmail: "Alice@Example.com",
		Name: "Weekly revenue", ContentType: "text/csv", S3Bucket: "b", S3Key: "k",
		Tags: []string{"script", "weekly-revenue"}, CurrentVersion: 1,
	}
}

// scriptOwnerCtx is the script owner calling as themselves.
func scriptOwnerCtx() context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "u-alice", UserEmail: "alice@example.com",
	})
}

// scriptStrangerCtx is a second authenticated person who is neither the owner nor an
// administrator.
func scriptStrangerCtx() context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "u-bob", UserEmail: "bob@example.com",
	})
}

func scriptOutputToolkit(t *testing.T) (*Toolkit, *inMemoryAssetStore) {
	t.Helper()
	store := newInMemoryAssetStore()
	require.NoError(t, store.Insert(context.Background(), scriptOutputAsset()))
	return New(Config{Name: "test", AssetStore: store, S3Bucket: "b"}), store
}

// A run's output is in its owner's listing, beside the assets they saved
// themselves (#1551).
func TestManageAsset_ListIncludesTheOwnersScriptOutput(t *testing.T) {
	tk, store := scriptOutputToolkit(t)
	require.NoError(t, store.Insert(context.Background(), portal.Asset{
		ID: "a-own", OwnerID: "u-alice", OwnerEmail: "alice@example.com", Name: "Saved by hand",
	}))

	r, _, err := tk.handleManageAsset(scriptOwnerCtx(), nil, manageAssetInput{Action: actionList})
	require.NoError(t, err)
	require.False(t, r.IsError)

	var out struct {
		Assets []portal.Asset `json:"assets"`
		Total  int            `json:"total"`
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	assert.Equal(t, 2, out.Total)

	ids := make([]string, 0, len(out.Assets))
	for _, a := range out.Assets {
		ids = append(ids, a.ID)
	}
	assert.ElementsMatch(t, []string{"a-script", "a-own"}, ids)
}

// The listing is still one person's: a second authenticated non-admin sees
// nothing of it.
func TestManageAsset_ListExcludesAnotherPersonsScriptOutput(t *testing.T) {
	tk, _ := scriptOutputToolkit(t)

	r, _, err := tk.handleManageAsset(scriptStrangerCtx(), nil, manageAssetInput{Action: actionList})
	require.NoError(t, err)
	require.False(t, r.IsError)

	var out struct {
		Total int `json:"total"`
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	assert.Zero(t, out.Total)
}

// Reading and changing it needs no administrator, and a stranger is refused.
func TestManageAsset_OwnerReadsAndChangesTheScriptOutput(t *testing.T) {
	tk, _ := scriptOutputToolkit(t)

	got, _, err := tk.handleManageAsset(scriptOwnerCtx(), nil,
		manageAssetInput{Action: actionGet, AssetID: "a-script"})
	require.NoError(t, err)
	assert.False(t, got.IsError)

	renamed := "Weekly revenue (Q3)"
	up, _, err := tk.handleManageAsset(scriptOwnerCtx(), nil, manageAssetInput{
		Action: actionUpdate, AssetID: "a-script", Name: renamed,
	})
	require.NoError(t, err)
	assert.False(t, up.IsError)

	denied, _, err := tk.handleManageAsset(scriptStrangerCtx(), nil, manageAssetInput{
		Action: actionUpdate, AssetID: "a-script", Name: renamed,
	})
	require.NoError(t, err)
	assert.True(t, denied.IsError)
}

// A run still reaches the assets of the person it acts for, and is scoped to
// that person rather than to the script's owner: after a transfer the two are
// different people, and the run presents the author's authority.
func TestCallerAssetOwner_UnattendedCallerActsForItsAuthor(t *testing.T) {
	run := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "script:weekly-revenue", UserEmail: "alice@example.com",
		OnBehalfOfEmail: "author@example.com",
	})
	owner := callerAssetOwner(run)
	assert.Equal(t, "author@example.com", owner.Email)
	// The principal is not an arm of its own: script:<name> is unique only
	// within an owner, so matching it would hand this run the outputs of
	// another person's same-named script (#1579).
	assert.Empty(t, owner.Arms().UserID)
	assert.False(t, owner.Owns("script:weekly-revenue", "bob@example.com"))
	assert.True(t, owner.Owns("u-author", "author@example.com"))

	person := callerAssetOwner(scriptOwnerCtx())
	assert.Equal(t, "u-alice", person.UserID)
	assert.Equal(t, "alice@example.com", person.Email)

	assert.False(t, callerAssetOwner(context.Background()).Identified())
}

// An enumeration is scoped to the caller's own library, which for a run is the
// assets its own script produced. The address a run carries is a person's, and
// listing on it would hand every run of a script that person's whole library --
// a different rule from the one that lets a run act on a named asset its author
// owns. The principal is not the inventory either: it is script:<name>, unique
// only within an owner, so it enumerates every same-named script's outputs on
// the platform. Neither identifier on the row names one script, so a run is
// scoped by the producer its own writes recorded (#1579).
func TestCallerAssetScope_AnUnattendedListingStaysTheScriptsOwn(t *testing.T) {
	run := scriptRunProducerCtx("script:weekly-revenue", "alice@example.com", "script-uuid")
	owner, producer := callerAssetScope(run)
	assert.False(t, owner.Identified(),
		"neither identifier on the row names one script, so neither is a scope")
	assert.Equal(t, producedby.KindScript, producer.Kind)
	assert.Equal(t, "script-uuid", producer.ID)

	person, personProducer := callerAssetScope(scriptOwnerCtx())
	assert.Equal(t, "u-alice", person.UserID)
	assert.Equal(t, "alice@example.com", person.Email)
	assert.False(t, personProducer.Named(), "a person is scoped by what they own")

	// A run carrying no producer -- a deployment that records none -- is scoped
	// by nothing rather than by the principal every same-named script shares.
	noProducer, noProducerScope := callerAssetScope(
		scriptOutputRunCtx("script:weekly-revenue", "alice@example.com", "alice@example.com"))
	assert.False(t, noProducer.Identified())
	assert.False(t, noProducerScope.Named())

	nobody, nobodyProducer := callerAssetScope(context.Background())
	assert.False(t, nobody.Identified())
	assert.False(t, nobodyProducer.Named())
}

// The listing a run gets is its own outputs, not the library of the person it
// acts for, exercised through the tool rather than through the scope helper.
func TestManageAsset_ARunsListingIsItsOwnOutputs(t *testing.T) {
	store := newInMemoryAssetStore()
	require.NoError(t, store.Insert(context.Background(), scriptOutputAsset()))
	require.NoError(t, store.Insert(context.Background(), portal.Asset{
		ID: "a-own", OwnerID: "u-alice", OwnerEmail: "alice@example.com", Name: "Saved by hand",
	}))
	store.producedBy = map[string]portaldomain.ContentProducer{
		"a-script": portaldomain.NewContentProducer(producedby.KindScript, "alices-script"),
	}
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "b"})

	run := scriptRunProducerCtx("script:weekly-revenue", "Alice@Example.com", "alices-script")
	r, _, err := tk.handleManageAsset(run, nil, manageAssetInput{Action: actionList})
	require.NoError(t, err)
	require.False(t, r.IsError)

	var out struct {
		Assets []portal.Asset `json:"assets"`
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	require.Len(t, out.Assets, 1)
	assert.Equal(t, "a-script", out.Assets[0].ID)

	// Naming the person's own asset is still the widened path.
	assert.True(t, ownsResource(run, "u-alice", "alice@example.com"))
}

// scriptOutputRunCtx is a managed-script run: the principal it authenticated as, its
// script owner's address, and the address of the person it acts for.
func scriptOutputRunCtx(principal, ownerEmail, actingFor string) context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: principal, UserEmail: ownerEmail, OnBehalfOfEmail: actingFor,
		Source: middleware.SourceScript,
	})
}

// A script name is unique only within its owner, so two people who each keep a
// weekly-revenue present the same principal. A run of Bob's must not reach the
// outputs of Alice's through the subject they share (#1579).
func TestManageAsset_ARunDoesNotReachASameNamedScriptsOutputs(t *testing.T) {
	tk, _ := scriptOutputToolkit(t)
	bobsRun := scriptOutputRunCtx("script:weekly-revenue", "bob@example.com", "bob@example.com")

	// The actions that ask ownsResource. manage_asset get is not one of them:
	// it reads any asset by id for any authenticated caller, which is how it
	// behaved before this and is not what the principal collision changed.
	for _, action := range []struct {
		name  string
		input manageAssetInput
	}{
		{"update", manageAssetInput{Action: actionUpdate, AssetID: "a-script", Name: "Taken"}},
		{"delete", manageAssetInput{Action: actionDelete, AssetID: "a-script"}},
		{"share", manageAssetInput{Action: actionShare, AssetID: "a-script", Recipient: "bob@example.com"}},
	} {
		t.Run(action.name, func(t *testing.T) {
			r, _, err := tk.handleManageAsset(bobsRun, nil, action.input)
			require.NoError(t, err)
			assert.True(t, r.IsError, "a run must reach no asset the person it acts for cannot")

			// The property the run has to match: Bob himself is refused the
			// same action, so his script is too.
			person, _, err := tk.handleManageAsset(scriptStrangerCtx(), nil, action.input)
			require.NoError(t, err)
			assert.True(t, person.IsError)
		})
	}
}

// The author's own run still owns the asset and still writes new versions of
// it, which is the reach the subject arm used to provide and the address arm
// now does.
func TestManageAsset_TheAuthorsOwnRunStillOwnsItsOutput(t *testing.T) {
	tk, _ := scriptOutputToolkit(t)
	// The address on the row is Alice@Example.com; the run acts for
	// alice@example.com. Addresses reach the platform from several identity
	// providers, so the arm is case-folded.
	alicesRun := scriptOutputRunCtx("script:weekly-revenue", "alice@example.com", "alice@example.com")

	// The ownership judgment itself, stated directly: manage_asset get reads
	// any asset by id for any authenticated caller, so asserting it succeeds
	// would prove nothing about ownership.
	assert.True(t, ownsResource(alicesRun, "script:weekly-revenue", "Alice@Example.com"))

	up, _, err := tk.handleManageAsset(alicesRun, nil, manageAssetInput{
		Action: actionUpdate, AssetID: "a-script", Name: "Weekly revenue (Q3)",
	})
	require.NoError(t, err)
	assert.False(t, up.IsError)
}

// The inventory a run lists is its own script's outputs. Alice's run sees hers;
// Bob's same-named script sees none of them, and neither sees the rest of the
// other person's library.
func TestManageAsset_ARunsListingIsItsOwnScriptsOutputs(t *testing.T) {
	tk, store := scriptOutputToolkit(t)
	ctx := context.Background()
	// Bob's same-named script wrote its own output, under the same principal.
	require.NoError(t, store.Insert(ctx, portal.Asset{
		ID: "a-bob-script", OwnerID: "script:weekly-revenue", OwnerEmail: "bob@example.com",
		Name: "Bob's weekly revenue",
	}))
	require.NoError(t, store.Insert(ctx, portal.Asset{
		ID: "a-alice-own", OwnerID: "u-alice", OwnerEmail: "alice@example.com",
		Name: "Saved by hand",
	}))

	listIDs := func(t *testing.T, ctx context.Context) []string {
		t.Helper()
		r, _, err := tk.handleManageAsset(ctx, nil, manageAssetInput{Action: actionList})
		require.NoError(t, err)
		require.False(t, r.IsError)
		var out struct {
			Assets []portal.Asset `json:"assets"`
		}
		tc, ok := r.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
		ids := make([]string, 0, len(out.Assets))
		for _, a := range out.Assets {
			ids = append(ids, a.ID)
		}
		return ids
	}

	store.producedBy = map[string]portaldomain.ContentProducer{
		"a-script":     portaldomain.NewContentProducer(producedby.KindScript, "alices-script"),
		"a-bob-script": portaldomain.NewContentProducer(producedby.KindScript, "bobs-script"),
	}

	assert.ElementsMatch(t, []string{"a-script"},
		listIDs(t, scriptRunProducerCtx("script:weekly-revenue", "alice@example.com", "alices-script")))
	assert.ElementsMatch(t, []string{"a-bob-script"},
		listIDs(t, scriptRunProducerCtx("script:weekly-revenue", "bob@example.com", "bobs-script")))
}

// A collection ENUMERATION is scoped the same way, on the producer a run's own
// writes recorded rather than on the principal two people share.
func TestCollectionScope_ARunIsScopedToItsOwnCollections(t *testing.T) {
	id, producer := collectionScope(
		scriptRunProducerCtx("script:weekly-revenue", "alice@example.com", "script-uuid"))
	assert.Empty(t, id, "the owner id a run's collections record is shared, so it is not a scope")
	assert.Equal(t, producedby.KindScript, producer.Kind)
	assert.Equal(t, "script-uuid", producer.ID)

	id, producer = collectionScope(scriptOwnerCtx())
	assert.Equal(t, "u-alice", id)
	assert.False(t, producer.Named(), "a person is scoped by what they own")

	id, producer = collectionScope(context.Background())
	assert.Equal(t, anonymousUserName, id)
	assert.False(t, producer.Named())

	// A run carrying no producer is scoped by nothing, which the handler
	// refuses rather than falling back to the shared principal.
	id, producer = collectionScope(
		scriptOutputRunCtx("script:weekly-revenue", "alice@example.com", "alice@example.com"))
	assert.Empty(t, id)
	assert.False(t, producer.Named())
}

// scriptRunProducerCtx is a managed-script run carrying the producer its own
// writes are recorded under, which is what its enumerations are scoped by.
func scriptRunProducerCtx(principal, ownerEmail, scriptID string) context.Context {
	ctx := scriptOutputRunCtx(principal, ownerEmail, ownerEmail)
	return producedby.With(ctx, producedby.Producer{
		Kind: producedby.KindScript, ID: scriptID, Label: "daily-sales",
	})
}

// A DRAFT run is a person. It is tagged SourceScript for audit and for its own
// session identity, but it authenticates as the caller, carries no address it
// acts for, and stamps no script producer. Keying the enumeration on the tag
// would refuse somebody the listing of their own library while they iterate on
// a script; the signal is the address a caller acts FOR, which only a platform
// run has.
func TestCallerAssetScope_ADraftRunIsThePersonAtTheKeyboard(t *testing.T) {
	draft := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "u-alice", UserEmail: "alice@example.com", Source: middleware.SourceScript,
	})
	owner, producer := callerAssetScope(draft)
	assert.Equal(t, "u-alice", owner.UserID)
	assert.Equal(t, "alice@example.com", owner.Email)
	assert.False(t, producer.Named())

	id, collProducer := collectionScope(draft)
	assert.Equal(t, "u-alice", id)
	assert.False(t, collProducer.Named())
}

// A draft run lists the person's own library through the tool, which is what
// the scope above buys and what keying on the source tag took away.
func TestManageAsset_ADraftRunListsTheCallersOwnAssets(t *testing.T) {
	store := newInMemoryAssetStore()
	require.NoError(t, store.Insert(context.Background(), portal.Asset{
		ID: "a-own", OwnerID: "u-alice", OwnerEmail: "alice@example.com", Name: "Saved by hand",
	}))
	tk := New(Config{Name: "test", AssetStore: store, S3Bucket: "b"})

	draft := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "u-alice", UserEmail: "alice@example.com", Source: middleware.SourceScript,
	})
	r, _, err := tk.handleManageAsset(draft, nil, manageAssetInput{Action: actionList})
	require.NoError(t, err)
	require.False(t, r.IsError, "a draft run is a person and lists their own library")

	var out struct {
		Assets []portal.Asset `json:"assets"`
	}
	tc, ok := r.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	require.Len(t, out.Assets, 1)
	assert.Equal(t, "a-own", out.Assets[0].ID)
}

// A producer of another kind is not a script's inventory. The middleware stamps
// a session producer on every ordinary MCP call, and a run's scope must not
// match one.
func TestCallerProducer_OnlyAScriptProducerScopesARun(t *testing.T) {
	run := producedby.With(
		scriptOutputRunCtx("script:weekly-revenue", "alice@example.com", "alice@example.com"),
		producedby.Producer{Kind: producedby.KindSession, ID: "sess-1"})
	assert.False(t, callerProducer(run).Named())

	person := producedby.With(
		scriptOutputRunCtx("script:weekly-revenue", "alice@example.com", "alice@example.com"),
		producedby.Producer{Kind: producedby.KindPerson, ID: "u-alice", Label: "alice@example.com"})
	assert.False(t, callerProducer(person).Named())
}
