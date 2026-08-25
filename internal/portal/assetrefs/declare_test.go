package assetrefs_test

import (
	"bytes"
	"log/slog"
	"testing"

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
	return assetrefs.NewDeclarer(refs, fixtureResources(), "")
}

// TestDeclareRecordsWhatTheAuthorCanRead is the ordinary path: a readable
// resource is resolved, recorded in declared order, and stamped with the author
// whose permission admitted it.
func TestDeclareRecordsWhatTheAuthorCanRead(t *testing.T) {
	store := newFakeRefs()
	d := declarer(store)

	declared, err := d.Resolve(t.Context(), []string{logoURI}, analystClaims())
	require.NoError(t, err)
	require.Len(t, declared, 1)
	assert.Equal(t, "res-logo", declared[0].ResourceID)

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
	_, err := declarer(newFakeRefs()).Resolve(t.Context(), []string{chartURI}, analystClaims())

	require.Error(t, err)
	require.ErrorIs(t, err, assetrefs.ErrRefused)
	assert.Contains(t, err.Error(), chartURI)
	assert.NotContains(t, err.Error(), "Forecast",
		"a refusal names the URI the author wrote and nothing about the resource behind it")
}

// TestResolveAdmitsThePersonaThatOwnsIt is the other half of the check above:
// the rule withholds from outsiders without withholding from the audience.
func TestResolveAdmitsThePersonaThatOwnsIt(t *testing.T) {
	declared, err := declarer(newFakeRefs()).Resolve(t.Context(), []string{chartURI}, financeClaims())

	require.NoError(t, err)
	require.Len(t, declared, 1)
	assert.Equal(t, "res-chart", declared[0].ResourceID)
}

// TestResolveIsAllOrNothing proves a declaration naming one unreadable resource
// records none of the others. A partly applied declaration would leave the
// markup referring to files that resolve for some readers and not others.
func TestResolveIsAllOrNothing(t *testing.T) {
	store := newFakeRefs()
	_, err := declarer(store).Resolve(t.Context(), []string{logoURI, chartURI}, analystClaims())

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
		{"not a resource URI", []string{"https://example.com/logo.png"}, "is not a managed resource URI", true},
		{"unknown scope", []string{"mcp://nowhere/logo.png"}, "is not a managed resource URI", true},
		{"no such resource", []string{"mcp://global/brand/missing.png"}, "no managed resource at", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := declarer(newFakeRefs()).Resolve(t.Context(), tt.uris, analystClaims())
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
	uris := make([]string, portaldomain.MaxAssetResourceRefs+1)
	for i := range uris {
		uris[i] = logoURI
	}

	_, err := declarer(newFakeRefs()).Resolve(t.Context(), uris, analystClaims())

	require.ErrorIs(t, err, assetrefs.ErrRefused)
	assert.Contains(t, err.Error(), "20", "the cap must be named in the refusal")
	assert.Contains(t, err.Error(), "21", "and so must the number the author declared")
}

// TestResolveCollapsesDuplicates proves one file cannot consume two slots of the
// cap, and that the surviving entry keeps the position it was first declared at.
func TestResolveCollapsesDuplicates(t *testing.T) {
	declared, err := declarer(newFakeRefs()).Resolve(t.Context(),
		[]string{logoURI, logoURI}, analystClaims())

	require.NoError(t, err)
	assert.Len(t, declared, 1)
}

// TestApplyKeepsAnExistingToken is what stops a reader's open page breaking
// every time the author saves: a reference that survives a save keeps the URL
// already rendered into that page.
func TestApplyKeepsAnExistingToken(t *testing.T) {
	store := newFakeRefs()
	d := declarer(store)

	declared, err := d.Resolve(t.Context(), []string{logoURI}, analystClaims())
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

	declared, err := d.Resolve(t.Context(), []string{logoURI}, analystClaims())
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
		"no ref store":  assetrefs.NewDeclarer(nil, fixtureResources(), ""),
		"no resources":  assetrefs.NewDeclarer(newFakeRefs(), nil, ""),
		"nothing wired": assetrefs.NewDeclarer(nil, nil, ""),
	} {
		t.Run(name, func(t *testing.T) {
			assert.False(t, d.Available())

			_, err := d.Resolve(t.Context(), []string{logoURI}, analystClaims())
			assert.ErrorIs(t, err, assetrefs.ErrRefused)

			_, err = d.Apply(t.Context(), testAssetID, nil, author)
			assert.ErrorIs(t, err, assetrefs.ErrRefused)
		})
	}
}

// TestResolveStoreFailureIsNotARefusal separates a database fault from a
// permission decision: telling an author they may not reference a file when the
// lookup merely failed is a different and worse answer.
func TestResolveStoreFailureIsNotARefusal(t *testing.T) {
	res := fixtureResources()
	res.uriErr = errStore

	_, err := assetrefs.NewDeclarer(newFakeRefs(), res, "").
		Resolve(t.Context(), []string{logoURI}, analystClaims())

	require.Error(t, err)
	assert.NotErrorIs(t, err, assetrefs.ErrRefused)
}

// TestApplyReportsStoreFailures pins that a write the store could not make is
// reported rather than swallowed, on both the read-back and the write.
func TestApplyReportsStoreFailures(t *testing.T) {
	declared := []assetrefs.Declared{{ResourceID: "res-logo", URI: logoURI}}

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

	assetrefs.LogGranted("asset-1", portaldomain.AssetResourceRef{
		ResourceID: "res-logo",
		URI:        "mcp://global/brand/logo.png",
		DeclaredBy: "analyst@example.com",
	})
	assetrefs.LogRevoked("asset-1", "res-logo", "owner@example.com")

	out := buf.String()
	assert.Contains(t, out, "asset_resource_reference.granted")
	assert.Contains(t, out, "declared_by=analyst@example.com")
	assert.Contains(t, out, "asset_resource_reference.revoked")
	assert.Contains(t, out, "revoked_by=owner@example.com")
	assert.Contains(t, out, "resource_id=res-logo")
}

// A name carrying a control character reaches the log sanitized, because the
// asset id and the address both come from callers.
func TestGrantLogSanitizes(t *testing.T) {
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	assetrefs.LogRevoked("asset\n1", "res\r2", "owner\n@example.com")

	out := buf.String()
	assert.NotContains(t, out, "asset\n1")
	assert.NotContains(t, out, "res\r2")
}
