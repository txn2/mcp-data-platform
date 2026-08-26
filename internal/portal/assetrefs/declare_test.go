package assetrefs_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

const (
	chartURI  = "mcp://persona/finance/chart.png"
	author    = "analyst@example.com"
	anyPerson = "u1"

	// The two asset fixtures and the reference form an author writes to name
	// one (#1488).
	readableAssetID  = "ast-readable"
	closedAssetID    = "ast-closed"
	readableAssetRef = "mcp:asset:" + readableAssetID
	closedAssetRef   = "mcp:asset:" + closedAssetID
)

// analystClaims is a caller in no persona: they read global resources and
// nothing else.
func analystClaims() resource.Claims {
	return resource.BuildClaims(anyPerson, author, "", nil, false)
}

// financeClaims is the same caller inside the persona that owns the chart.
func financeClaims() resource.Claims {
	return resource.BuildClaims(anyPerson, author, "finance", nil, false)
}

func declarer(refs *fakeRefs) *assetrefs.Declarer {
	d := assetrefs.NewDeclarer(refs, fixtureAssets())
	d.BindResources(fixtureResources(), "")
	return d
}

// fixtureAssets is the standard asset set: one the author can read and one they
// cannot, so the asset arm of a declaration is checked both ways.
func fixtureAssets() *fakeAssets {
	return &fakeAssets{byID: map[string]*portaldomain.Asset{
		readableAssetID: {ID: readableAssetID, Name: "Weekly numbers", OwnerID: anyPerson},
		closedAssetID:   {ID: closedAssetID, Name: "Someone else's", OwnerID: "u2"},
	}}
}

// analystAuthor is the identity a declaration is checked against: global
// resources only, and the one asset this person owns.
func analystAuthor() assetrefs.Author {
	return assetrefs.Author{Claims: analystClaims(), ReadsAsset: ownedByAnyPerson}
}

// financeAuthor is the same person inside the persona that owns the chart.
func financeAuthor() assetrefs.Author {
	return assetrefs.Author{Claims: financeClaims(), ReadsAsset: ownedByAnyPerson}
}

// ownedByAnyPerson is the asset read gate the fixtures are built around: this
// caller reads what they own and nothing else, which is the narrowest true
// shape of the portal's own rule.
func ownedByAnyPerson(_ context.Context, asset *portaldomain.Asset) bool {
	return asset != nil && asset.OwnerID == anyPerson
}

// TestDeclareRecordsWhatTheAuthorCanRead is the ordinary path: a readable
// resource is resolved, recorded in declared order, and stamped with the author
// whose permission admitted it.
func TestDeclareRecordsWhatTheAuthorCanRead(t *testing.T) {
	store := newFakeRefs()
	d := declarer(store)

	declared, err := d.Resolve(t.Context(), []string{logoURI}, analystAuthor(), "")
	require.NoError(t, err)
	require.Len(t, declared, 1)
	assert.Equal(t, "res-logo", declared[0].TargetID)

	refs, err := d.Apply(t.Context(), testAssetID, declared, author)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, logoURI, refs[0].URI, "the URI is stored as declared, not rebuilt")
	assert.Equal(t, author, refs[0].DeclaredBy)
	assert.NotEmpty(t, refs[0].RefToken)
	assert.Equal(t, refs, store.replaced[testAssetID])
}

// TestResolveRefusesAResourceTheAuthorCannotRead is the acceptance criterion
// for the permission check, including that the refusal names the URI the author
// wrote -- the only string they can find in their own content.
func TestResolveRefusesAResourceTheAuthorCannotRead(t *testing.T) {
	_, err := declarer(newFakeRefs()).Resolve(t.Context(), []string{chartURI}, analystAuthor(), "")

	require.Error(t, err)
	require.ErrorIs(t, err, assetrefs.ErrRefused)
	assert.Contains(t, err.Error(), chartURI)
	assert.NotContains(t, err.Error(), "Forecast",
		"a refusal names the URI the author wrote and nothing about the resource behind it")
}

// TestResolveAdmitsThePersonaThatOwnsIt is the other half of the check above:
// the rule withholds from outsiders without withholding from the audience.
func TestResolveAdmitsThePersonaThatOwnsIt(t *testing.T) {
	declared, err := declarer(newFakeRefs()).Resolve(t.Context(), []string{chartURI}, financeAuthor(), "")

	require.NoError(t, err)
	require.Len(t, declared, 1)
	assert.Equal(t, "res-chart", declared[0].TargetID)
}

// TestResolveIsAllOrNothing proves a declaration naming one unreadable resource
// records none of the others. A partly applied declaration would leave the
// markup referring to files that resolve for some readers and not others.
func TestResolveIsAllOrNothing(t *testing.T) {
	store := newFakeRefs()
	_, err := declarer(store).Resolve(t.Context(), []string{logoURI, chartURI}, analystAuthor(), "")

	require.ErrorIs(t, err, assetrefs.ErrRefused)
	assert.Empty(t, store.replaced, "nothing may be written when any URI is refused")
}

// TestResolveRefusals covers every shape of a declaration that cannot stand.
func TestResolveRefusals(t *testing.T) {
	tests := []struct {
		name    string
		uris    []string
		wants   string
		refused bool
	}{
		{"neither form", []string{"https://example.com/logo.png"}, "mcp:asset:<id>", true},
		{"unknown scope", []string{"mcp://nowhere/logo.png"}, "is not a managed resource URI", true},
		{"no such resource", []string{"mcp://global/brand/missing.png"}, "no managed resource at", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := declarer(newFakeRefs()).Resolve(t.Context(), tt.uris, analystAuthor(), "")
			require.Error(t, err)
			assert.ErrorIs(t, err, assetrefs.ErrRefused)
			assert.Contains(t, err.Error(), tt.wants)
			assert.Contains(t, err.Error(), tt.uris[0], "the refusal must name the URI the author wrote")
		})
	}
}

// TestResolveRefusesAboveTheCap is the acceptance criterion for the bound: the
// refusal states the cap rather than leaving the author to discover it.
func TestResolveRefusesAboveTheCap(t *testing.T) {
	uris := make([]string, assetrefs.MaxRefs+1)
	for i := range uris {
		uris[i] = logoURI
	}

	_, err := declarer(newFakeRefs()).Resolve(t.Context(), uris, analystAuthor(), "")

	require.ErrorIs(t, err, assetrefs.ErrRefused)
	assert.Contains(t, err.Error(), "20", "the cap must be named in the refusal")
	assert.Contains(t, err.Error(), "21", "and so must the number the author declared")
}

// TestResolveCollapsesDuplicates proves one file cannot consume two slots of the
// cap, and that the surviving entry keeps the position it was first declared at.
func TestResolveCollapsesDuplicates(t *testing.T) {
	declared, err := declarer(newFakeRefs()).Resolve(t.Context(),
		[]string{logoURI, logoURI}, analystAuthor(), "")

	require.NoError(t, err)
	assert.Len(t, declared, 1)
}

// TestApplyKeepsAnExistingToken is what stops a reader's open page breaking
// every time the author saves: a reference that survives a save keeps the URL
// already rendered into that page.
func TestApplyKeepsAnExistingToken(t *testing.T) {
	store := newFakeRefs()
	d := declarer(store)

	declared, err := d.Resolve(t.Context(), []string{logoURI}, analystAuthor(), "")
	require.NoError(t, err)
	first, err := d.Apply(t.Context(), testAssetID, declared, author)
	require.NoError(t, err)

	second, err := d.Apply(t.Context(), testAssetID, declared, author)
	require.NoError(t, err)
	assert.Equal(t, first[0].RefToken, second[0].RefToken)
}

// TestApplyEmptyClearsEveryReference proves an empty declaration is a decision
// rather than a no-op, which is what lets an author remove a reference.
func TestApplyEmptyClearsEveryReference(t *testing.T) {
	store := newFakeRefs()
	d := declarer(store)

	declared, err := d.Resolve(t.Context(), []string{logoURI}, analystAuthor(), "")
	require.NoError(t, err)
	_, err = d.Apply(t.Context(), testAssetID, declared, author)
	require.NoError(t, err)

	refs, err := d.Apply(t.Context(), testAssetID, nil, author)
	require.NoError(t, err)
	assert.Empty(t, refs)
	assert.Empty(t, store.byAsset[testAssetID])
}

// TestDeclarerWithoutAResourceLayerRefuses proves a declaration made where
// there is nothing to declare against is refused, never accepted and dropped.
// An author told a reference was recorded when nothing was is the failure this
// closes.
func TestDeclarerWithoutAResourceLayerRefuses(t *testing.T) {
	for name, d := range map[string]*assetrefs.Declarer{
		"nil declarer":  nil,
		"no ref store":  assetrefs.NewDeclarer(nil, fixtureAssets()),
		"nothing wired": assetrefs.NewDeclarer(nil, nil),
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, d.Available())

			_, err := d.Resolve(t.Context(), []string{logoURI}, analystAuthor(), "")
			assert.ErrorIs(t, err, assetrefs.ErrRefused)

			_, err = d.Apply(t.Context(), testAssetID, nil, author)
			assert.ErrorIs(t, err, assetrefs.ErrRefused)
		})
	}
}

// TestResolveRefusesAResourceURIWithNoResourceLayer proves the two kinds are
// independent: a declarer with a reference store and an asset store but no
// managed-resource layer records an asset reference and refuses an mcp:// URI,
// rather than either accepting it and dropping it or refusing both kinds.
func TestResolveRefusesAResourceURIWithNoResourceLayer(t *testing.T) {
	d := assetrefs.NewDeclarer(newFakeRefs(), fixtureAssets())

	_, err := d.Resolve(t.Context(), []string{logoURI}, analystAuthor(), "")
	require.ErrorIs(t, err, assetrefs.ErrRefused)
	assert.Contains(t, err.Error(), "managed-resource layer")

	declared, err := d.Resolve(t.Context(), []string{readableAssetRef}, analystAuthor(), "")
	require.NoError(t, err)
	require.Len(t, declared, 1)
	assert.Equal(t, assetrefs.TargetAsset, declared[0].Kind)
}

// TestResolveStoreFailureIsNotARefusal separates a database fault from a
// permission decision: telling an author they may not reference a file when the
// lookup merely failed is a different and worse answer.
func TestResolveStoreFailureIsNotARefusal(t *testing.T) {
	res := fixtureResources()
	res.uriErr = errStore
	d := assetrefs.NewDeclarer(newFakeRefs(), fixtureAssets())
	d.BindResources(res, "")

	_, err := d.Resolve(t.Context(), []string{logoURI}, analystAuthor(), "")

	require.Error(t, err)
	assert.NotErrorIs(t, err, assetrefs.ErrRefused)
}

// TestResolveAssetStoreFailureIsARefusal states the asset arm's deliberate
// conflation, so it is a decision on the record rather than an accident: the
// asset store reports a missing row as an error and no implementation
// distinguishes it from a fault, so a fault refuses the declaration. The
// operator's log carries the underlying error; the author is told only that the
// reference did not resolve.
func TestResolveAssetStoreFailureIsARefusal(t *testing.T) {
	assets := fixtureAssets()
	assets.getErr = errStore

	_, err := assetrefs.NewDeclarer(newFakeRefs(), assets).
		Resolve(t.Context(), []string{readableAssetRef}, analystAuthor(), "")

	require.ErrorIs(t, err, assetrefs.ErrRefused)
	assert.Contains(t, err.Error(), "no asset at")
}

// TestApplyReportsStoreFailures pins that a write the store could not make is
// reported rather than swallowed, on both the read-back and the write.
func TestApplyReportsStoreFailures(t *testing.T) {
	declared := []assetrefs.Declared{
		{Kind: assetrefs.TargetResource, TargetID: "res-logo", URI: logoURI},
	}

	listFails := newFakeRefs()
	listFails.listErr = errStore
	_, err := declarer(listFails).Apply(t.Context(), testAssetID, declared, author)
	require.ErrorIs(t, err, errStore)

	writeFails := newFakeRefs()
	writeFails.putErr = errStore
	_, err = declarer(writeFails).Apply(t.Context(), testAssetID, declared, author)
	require.ErrorIs(t, err, errStore)
}

// TestGrantNoticeNamesTheConsequence pins the sentence surfaces show at the
// moment a reference is made. It must state what the grant does in the terms it
// matters in, including that a public link carries it.
func TestGrantNoticeNamesTheConsequence(t *testing.T) {
	assert.Contains(t, assetrefs.GrantNotice, "shared")
	assert.Contains(t, assetrefs.GrantNotice, "public link")
}

// TestGrantLogRecordsBothDoors is the operator's half of the reference model.
//
// A reference is made from two places -- an agent's save through Apply, and a
// person's add through the portal panel (#1475) -- and both call these. A log
// that carried only one of them would show half the grants and read as
// complete, which is why the wording lives here rather than at each caller.
func TestGrantLogRecordsBothDoors(t *testing.T) {
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	assetrefs.LogGranted("asset-1", assetrefs.Ref{
		TargetKind: assetrefs.TargetResource,
		TargetID:   "res-logo",
		URI:        "mcp://global/brand/logo.png",
		DeclaredBy: "analyst@example.com",
	})
	assetrefs.LogRevoked("asset-1", assetrefs.TargetAsset, "ast-2", "owner@example.com")

	out := buf.String()
	assert.Contains(t, out, "asset_reference.granted")
	assert.Contains(t, out, "declared_by=analyst@example.com")
	assert.Contains(t, out, "asset_reference.revoked")
	assert.Contains(t, out, "revoked_by=owner@example.com")
	assert.Contains(t, out, "target_id=res-logo")
	assert.Contains(t, out, "target_kind=asset", "the log says which id space the target is in")
}

// A name carrying a control character reaches the log sanitized, because the
// asset id and the address both come from callers.
func TestGrantLogSanitizes(t *testing.T) {
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	assetrefs.LogRevoked("asset\n1", assetrefs.TargetResource, "res\r2", "owner\n@example.com")

	out := buf.String()
	assert.NotContains(t, out, "asset\n1")
	assert.NotContains(t, out, "res\r2")
}

// TestDeclareRecordsAnAssetReference is the acceptance criterion for #1488: an
// asset the author may read is resolved to an asset-kind reference, stored
// under the reference string the author wrote.
func TestDeclareRecordsAnAssetReference(t *testing.T) {
	store := newFakeRefs()
	d := declarer(store)

	declared, err := d.Resolve(t.Context(), []string{readableAssetRef}, analystAuthor(), "")
	require.NoError(t, err)
	require.Len(t, declared, 1)
	assert.Equal(t, assetrefs.TargetAsset, declared[0].Kind)
	assert.Equal(t, readableAssetID, declared[0].TargetID)

	refs, err := d.Apply(t.Context(), testAssetID, declared, author)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, readableAssetRef, refs[0].URI, "the reference is stored as declared")
	assert.Equal(t, assetrefs.TargetAsset, refs[0].TargetKind)
	assert.NotEmpty(t, refs[0].RefToken)
}

// TestResolveRefusesAnAssetTheAuthorCannotRead is the other half of the rule
// this issue settled: the audience a reference carries is the referencing
// asset's, so the ONE check is the author's own read at declaration time.
func TestResolveRefusesAnAssetTheAuthorCannotRead(t *testing.T) {
	_, err := declarer(newFakeRefs()).Resolve(t.Context(), []string{closedAssetRef}, analystAuthor(), "")

	require.ErrorIs(t, err, assetrefs.ErrRefused)
	assert.Contains(t, err.Error(), closedAssetRef)
	assert.NotContains(t, err.Error(), "Someone else's",
		"a refusal names the reference the author wrote and nothing about the asset behind it")
}

// TestResolveRefusesAnAssetWithNoReadGate proves a caller that established no
// identity references no asset. An author arm left nil must refuse rather than
// admit, since the reference it would record is a permanent grant.
func TestResolveRefusesAnAssetWithNoReadGate(t *testing.T) {
	_, err := declarer(newFakeRefs()).Resolve(t.Context(),
		[]string{readableAssetRef}, assetrefs.Author{Claims: analystClaims()}, "")

	require.ErrorIs(t, err, assetrefs.ErrRefused)
	assert.Contains(t, err.Error(), "cannot read")
}

// TestResolveRefusesAMissingOrDeletedAsset covers the two ways an asset
// reference resolves to nothing. A soft-deleted asset is refused as absent
// rather than referenced, so a declaration cannot resurrect a deleted asset's
// content into a page that outlives it.
func TestResolveRefusesAMissingOrDeletedAsset(t *testing.T) {
	deleted := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	assets := fixtureAssets()
	assets.byID["ast-gone"] = &portaldomain.Asset{
		ID: "ast-gone", OwnerID: anyPerson, DeletedAt: &deleted,
	}
	d := assetrefs.NewDeclarer(newFakeRefs(), assets)

	for name, ref := range map[string]string{
		"no such asset": "mcp:asset:ast-nope",
		"deleted asset": "mcp:asset:ast-gone",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := d.Resolve(t.Context(), []string{ref}, analystAuthor(), "")
			require.ErrorIs(t, err, assetrefs.ErrRefused)
			assert.Contains(t, err.Error(), "no asset at")
		})
	}
}

// TestResolveRefusesAReferenceInNeitherForm proves the refusal for a string
// that is neither a resource URI nor an asset reference names both forms, so an
// author who pasted a plain URL learns what the field takes.
func TestResolveRefusesAReferenceInNeitherForm(t *testing.T) {
	for _, uri := range []string{
		"https://example.com/logo.png",
		"mcp:knowledge_page:kp-1",
		"mcp:resource:res-logo",
	} {
		_, err := declarer(newFakeRefs()).Resolve(t.Context(), []string{uri}, analystAuthor(), "")
		require.ErrorIs(t, err, assetrefs.ErrRefused, uri)
		assert.Contains(t, err.Error(), "mcp:asset:<id>")
		assert.Contains(t, err.Error(), uri)
	}
}

// TestApplyKeepsTokensPerKind proves the kind is part of a reference's
// identity: a resource and an asset that share an id are two references with
// two tokens, and neither inherits the other's.
func TestApplyKeepsTokensPerKind(t *testing.T) {
	store := newFakeRefs()
	d := declarer(store)

	refs, err := d.Apply(t.Context(), testAssetID, []assetrefs.Declared{
		{Kind: assetrefs.TargetResource, TargetID: "same-id", URI: logoURI},
		{Kind: assetrefs.TargetAsset, TargetID: "same-id", URI: readableAssetRef},
	}, author)
	require.NoError(t, err)
	require.Len(t, refs, 2)
	assert.NotEqual(t, refs[0].RefToken, refs[1].RefToken)

	again, err := d.Apply(t.Context(), testAssetID, []assetrefs.Declared{
		{Kind: assetrefs.TargetResource, TargetID: "same-id", URI: logoURI},
		{Kind: assetrefs.TargetAsset, TargetID: "same-id", URI: readableAssetRef},
	}, author)
	require.NoError(t, err)
	assert.Equal(t, refs[0].RefToken, again[0].RefToken, "a surviving reference keeps its token")
	assert.Equal(t, refs[1].RefToken, again[1].RefToken)
}

// TestResolveRefusesAnAssetReferencingItself is what keeps the two doors onto
// one mechanism agreeing: the portal's add refuses a self-reference, and so
// does a save. Serving answers such a reference rather than following it, so
// nothing breaks -- but it resolves to the content it was written in, which is
// no reading an author could have meant.
func TestResolveRefusesAnAssetReferencingItself(t *testing.T) {
	_, err := declarer(newFakeRefs()).Resolve(t.Context(),
		[]string{readableAssetRef}, analystAuthor(), readableAssetID)

	require.ErrorIs(t, err, assetrefs.ErrRefused)
	assert.Contains(t, err.Error(), "cannot reference itself")
}

// A create names no asset of its own, so the same reference stands where there
// is no id to collide with.
func TestResolveAdmitsAnAssetReferenceOnACreate(t *testing.T) {
	declared, err := declarer(newFakeRefs()).Resolve(t.Context(),
		[]string{readableAssetRef}, analystAuthor(), "")

	require.NoError(t, err)
	assert.Len(t, declared, 1)
}

// TestResolveRecordsTheTrimmedReference is what stops a save reporting a
// reference that never renders. The reference parser trims, so a padded entry
// resolves; the rewrite matches on the stored URI, so a padded one stored as
// written would match nothing in the content.
func TestResolveRecordsTheTrimmedReference(t *testing.T) {
	declared, err := declarer(newFakeRefs()).Resolve(t.Context(),
		[]string{"  " + readableAssetRef + "\n"}, analystAuthor(), "")

	require.NoError(t, err)
	require.Len(t, declared, 1)
	assert.Equal(t, readableAssetRef, declared[0].URI,
		"the stored URI is what the rewrite matches on, so it is the trimmed one")
}

// The same entry padded and unpadded is one reference, not two: the trim
// happens before the duplicate check, so it cannot consume two slots of the cap
// or be recorded twice.
func TestResolveCollapsesPaddedDuplicates(t *testing.T) {
	declared, err := declarer(newFakeRefs()).Resolve(t.Context(),
		[]string{readableAssetRef, " " + readableAssetRef}, analystAuthor(), "")

	require.NoError(t, err)
	assert.Len(t, declared, 1)
}

// TestResolveRefusesAnAssetReferenceWithNoAssetStore covers the mirror of the
// missing-resource-layer case: a declarer with a reference store and no asset
// store refuses an asset reference with that reason rather than accepting one
// it could never resolve.
func TestResolveRefusesAnAssetReferenceWithNoAssetStore(t *testing.T) {
	d := assetrefs.NewDeclarer(newFakeRefs(), nil)
	d.BindResources(fixtureResources(), "")

	_, err := d.Resolve(t.Context(), []string{readableAssetRef}, analystAuthor(), "")

	require.ErrorIs(t, err, assetrefs.ErrRefused)
	assert.Contains(t, err.Error(), "no asset store")
}

// TestBindResourcesOnANilDeclarerIsANoOp proves the binder tolerates the
// deployment that never built a declarer, so the composition root binds without
// first asking whether there is anything to bind to.
func TestBindResourcesOnANilDeclarerIsANoOp(t *testing.T) {
	var d *assetrefs.Declarer
	assert.NotPanics(t, func() { d.BindResources(fixtureResources(), "") })
	assert.False(t, d.Available())
}

// TestResolveHonorsAConfiguredURIScheme proves the prefix this package matches
// on follows the deployment's own scheme rather than assuming the default: a
// deployment that renamed it would otherwise have every resource URI refused as
// neither form.
func TestResolveHonorsAConfiguredURIScheme(t *testing.T) {
	res := &fakeResources{byID: map[string]*resource.Resource{
		"res-logo": {ID: "res-logo", Scope: resource.ScopeGlobal, URI: "acme://global/brand/logo.png"},
	}}
	d := assetrefs.NewDeclarer(newFakeRefs(), fixtureAssets())
	d.BindResources(res, "acme")

	declared, err := d.Resolve(t.Context(),
		[]string{"acme://global/brand/logo.png"}, analystAuthor(), "")

	require.NoError(t, err)
	require.Len(t, declared, 1)
	assert.Equal(t, assetrefs.TargetResource, declared[0].Kind)
}
