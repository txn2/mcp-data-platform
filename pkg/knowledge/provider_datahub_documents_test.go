package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

type fakeDocumentSearcher struct {
	docs     []semantic.DocumentResult
	related  map[string][]semantic.DocumentResult
	err      error
	relErr   error
	gotQuery string
	gotLimit int
	gotURNs  []string
	doc      *semantic.DocumentResult // GetDocument result
	docErr   error
	gotGetID string
}

func (f *fakeDocumentSearcher) GetDocument(_ context.Context, urn string) (*semantic.DocumentResult, error) {
	f.gotGetID = urn
	if f.docErr != nil {
		return nil, f.docErr
	}
	return f.doc, nil
}

func (f *fakeDocumentSearcher) SearchDocuments(_ context.Context, query string, limit int) ([]semantic.DocumentResult, error) {
	f.gotQuery, f.gotLimit = query, limit
	return f.docs, f.err
}

func (f *fakeDocumentSearcher) GetRelatedDocuments(_ context.Context, urn string) ([]semantic.DocumentResult, error) {
	f.gotURNs = append(f.gotURNs, urn)
	if f.relErr != nil {
		return nil, f.relErr
	}
	return f.related[urn], nil
}

func TestDocumentsProvider_Search(t *testing.T) {
	f := &fakeDocumentSearcher{docs: []semantic.DocumentResult{
		{URN: "urn:li:document:doc-1", Title: "Observability Runbook", SubType: "runbook", Snippet: "how to query prometheus", ShowInGlobalContext: true, Status: "PUBLISHED", RelatedAssetURNs: []string{"urn:li:dataset:(x)"}},
		{URN: "urn:li:document:doc-2", Title: "Vocabulary", Snippet: "terms", ShowInGlobalContext: true, Status: "PUBLISHED"},
	}}
	p := NewContextDocumentsProvider(f)

	assert.Equal(t, SourceContextDocuments, p.Name())
	assert.Equal(t, ScopeShared, p.Scope())

	hits, err := p.Search(context.Background(), Query{Intent: "prometheus", Limit: 5})
	require.NoError(t, err)
	require.Len(t, hits, 2)
	assert.Equal(t, "prometheus", f.gotQuery)
	// Fetch the candidate budget directly, like every other source (no over-fetch).
	assert.Equal(t, 5, f.gotLimit)

	// The URN both drills in and is the citation; related assets ride along.
	assert.Equal(t, SourceContextDocuments, hits[0].Source)
	assert.Equal(t, "urn:li:document:doc-1", hits[0].Ref)
	assert.Equal(t, "urn:li:document:doc-1", hits[0].Reference)
	assert.Equal(t, []string{"urn:li:dataset:(x)"}, hits[0].EntityURNs)
	assert.Contains(t, hits[0].Text, "Observability Runbook")
	assert.Contains(t, hits[0].Text, "how to query prometheus")
	assert.Greater(t, hits[0].Score, hits[1].Score, "positional score descends with rank")
}

func TestDocumentsProvider_FiltersHiddenAndUnpublished(t *testing.T) {
	f := &fakeDocumentSearcher{docs: []semantic.DocumentResult{
		{URN: "urn:li:document:visible", Title: "Visible", ShowInGlobalContext: true, Status: "PUBLISHED"},
		{URN: "urn:li:document:hidden", Title: "Hidden", ShowInGlobalContext: false, Status: "PUBLISHED"},
		{URN: "urn:li:document:draft", Title: "Draft", ShowInGlobalContext: true, Status: "UNPUBLISHED"},
		{URN: "urn:li:document:nostatus", Title: "No status (upstream defaults unset status to PUBLISHED)", ShowInGlobalContext: true},
		{URN: "urn:li:document:archived", Title: "Unknown future state", ShowInGlobalContext: true, Status: "ARCHIVED"},
	}}
	p := NewContextDocumentsProvider(f)

	hits, err := p.Search(context.Background(), Query{Intent: "x", Limit: 10})
	require.NoError(t, err)

	// Only globally-visible, published documents surface: a published doc and one with
	// unset status (the upstream create path defaults unset status to PUBLISHED). Hidden,
	// explicitly UNPUBLISHED, and any unknown non-published state are all excluded, so
	// neither a steward's hidden doc, a draft, nor an unrecognized state leaks.
	got := make([]string, 0, len(hits))
	for _, h := range hits {
		got = append(got, h.Ref)
	}
	assert.ElementsMatch(t, []string{"urn:li:document:visible", "urn:li:document:nostatus"}, got)
}

// TestDocumentsProvider_DefaultVisibleSurfaces proves a published document with no
// explicit settings aspect (DataHub default: globally visible) is surfaced rather
// than dropped, the mirror of the hidden-document case.
func TestDocumentsProvider_DefaultVisibleSurfaces(t *testing.T) {
	f := &fakeDocumentSearcher{docs: []semantic.DocumentResult{
		{URN: "urn:li:document:defvis", Title: "Default visible", ShowInGlobalContext: true, Status: "PUBLISHED"},
	}}
	hits, err := NewContextDocumentsProvider(f).Search(context.Background(), Query{Intent: "x", Limit: 10})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "urn:li:document:defvis", hits[0].Ref)
}

func TestDocumentsProvider_EntityArmAndDedup(t *testing.T) {
	f := &fakeDocumentSearcher{
		related: map[string][]semantic.DocumentResult{
			"urn:li:dataset:(t)": {
				{URN: "urn:li:document:linked", Title: "Linked runbook", ShowInGlobalContext: true, Status: "PUBLISHED", RelatedAssetURNs: []string{"urn:li:dataset:(t)"}},
				{URN: "urn:li:document:nonglobal", Title: "Linked but not global", ShowInGlobalContext: false, Status: "PUBLISHED"},
				{URN: "urn:li:document:draft", Title: "Draft", ShowInGlobalContext: true, Status: "UNPUBLISHED"},
				{URN: "urn:li:document:both", Title: "Both", ShowInGlobalContext: true, Status: "PUBLISHED"},
			},
		},
		docs: []semantic.DocumentResult{
			{URN: "urn:li:document:both", Title: "Both", ShowInGlobalContext: true, Status: "PUBLISHED"},
			{URN: "urn:li:document:textonly", Title: "Text only", ShowInGlobalContext: true, Status: "PUBLISHED"},
		},
	}
	p := NewContextDocumentsProvider(f)

	hits, err := p.Search(context.Background(), Query{Intent: "runbook", EntityURNs: []string{"urn:li:dataset:(t)"}, Limit: 10})
	require.NoError(t, err)

	assert.Equal(t, []string{"urn:li:dataset:(t)"}, f.gotURNs, "the entity arm queries each requested URN")

	refs := make([]string, 0, len(hits))
	scores := map[string]float64{}
	for _, h := range hits {
		assert.Equal(t, SourceContextDocuments, h.Source)
		refs = append(refs, h.Ref)
		scores[h.Ref] = h.Score
		// Provenance: an entity-linked document carries its related assets (populated
		// by the v1.10.1 / #166 full projection), so the queried entity links back.
		if h.Ref == "urn:li:document:linked" {
			assert.Equal(t, []string{"urn:li:dataset:(t)"}, h.EntityURNs)
		}
	}
	// The entity (linked-asset) arm surfaces published linked documents INCLUDING the
	// non-global one (ShowInGlobalContext=false is "accessible only through linked
	// assets" per the DataHub contract, and this IS that path); the draft is excluded.
	// Plus the text-only doc; the doc found both ways de-duplicates to one entity hit.
	assert.ElementsMatch(t, []string{"urn:li:document:linked", "urn:li:document:nonglobal", "urn:li:document:both", "urn:li:document:textonly"}, refs)
	assert.Equal(t, entityMatchScore, scores["urn:li:document:nonglobal"], "a non-global doc is reachable via its linked asset")
	assert.Equal(t, entityMatchScore, scores["urn:li:document:both"], "a doc found via both arms keeps the entity (exact-match) score")
	assert.Less(t, scores["urn:li:document:textonly"], entityMatchScore, "a text-only match ranks below an entity match")
}

func TestDocumentsProvider_EntityArmErrorSkipped(t *testing.T) {
	f := &fakeDocumentSearcher{relErr: errors.New("boom")}
	hits, err := NewContextDocumentsProvider(f).Search(context.Background(), Query{EntityURNs: []string{"urn:li:dataset:(t)"}})
	require.NoError(t, err, "a related-document lookup error is skipped, not surfaced, so it does not blank the search")
	assert.Empty(t, hits)
}

func TestDocumentsProvider_NoIntent(t *testing.T) {
	f := &fakeDocumentSearcher{docs: []semantic.DocumentResult{{URN: "x"}}}
	p := NewContextDocumentsProvider(f)

	hits, err := p.Search(context.Background(), Query{Intent: "   "})
	require.NoError(t, err)
	assert.Nil(t, hits, "no intent yields nothing")
	assert.Equal(t, "", f.gotQuery, "the searcher is not called without an intent")
}

func TestDocumentsProvider_Error(t *testing.T) {
	p := NewContextDocumentsProvider(&fakeDocumentSearcher{err: errors.New("boom")})
	_, err := p.Search(context.Background(), Query{Intent: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "document search")
}

// TestDocumentsProvider_SurfacesInAssembledRouter wires a real Router with the
// documents provider and asserts a context document surfaces end-to-end in a Result
// (CLAUDE.md rule 5: prove the assembled path, not just the function in isolation).
func TestDocumentsProvider_SurfacesInAssembledRouter(t *testing.T) {
	f := &fakeDocumentSearcher{docs: []semantic.DocumentResult{
		{URN: "urn:li:document:doc-1", Title: "Runbook", Snippet: "how to query prometheus", ShowInGlobalContext: true, Status: "PUBLISHED"},
	}}
	r := NewRouter(nil, nil, NewContextDocumentsProvider(f))

	res, err := r.Search(context.Background(), Query{Intent: "runbook"})
	require.NoError(t, err)

	var found bool
	for _, g := range res.Groups {
		for _, h := range g.Hits {
			if h.Source == SourceContextDocuments && h.Ref == "urn:li:document:doc-1" {
				found = true
			}
		}
	}
	assert.True(t, found, "the context document should surface through the assembled router under the documents source")
}

// TestDocumentHitText_Fallback covers an untitled document: it falls back to the
// sub-type, then to a generic label.
func TestDocumentHitText_Fallback(t *testing.T) {
	assert.Equal(t, "runbook", documentHitText(semantic.DocumentResult{SubType: "runbook"}))
	assert.Equal(t, "context document", documentHitText(semantic.DocumentResult{}))
}

func TestContextDocumentsProvider_Fetch(t *testing.T) {
	t.Run("returns full body for a document reference", func(t *testing.T) {
		f := &fakeDocumentSearcher{doc: &semantic.DocumentResult{
			URN:              "urn:li:document:doc-1",
			Title:            "Churn Runbook",
			Body:             "Full body the snippet truncated.",
			RelatedAssetURNs: []string{"urn:li:dataset:(x)"},
		}}
		doc, owned, err := NewContextDocumentsProvider(f).Fetch(context.Background(), "urn:li:document:doc-1", Caller{})
		require.True(t, owned)
		require.NoError(t, err)
		assert.Equal(t, "urn:li:document:doc-1", f.gotGetID)
		assert.Equal(t, "Full body the snippet truncated.", doc.Body)
		assert.Equal(t, SourceContextDocuments, doc.Source)
		assert.Equal(t, "Churn Runbook", doc.Title)
		assert.Equal(t, []string{"urn:li:dataset:(x)"}, doc.EntityURNs)
	})

	t.Run("declines a non-document reference", func(t *testing.T) {
		f := &fakeDocumentSearcher{}
		_, owned, err := NewContextDocumentsProvider(f).Fetch(context.Background(), "urn:li:dataset:(x)", Caller{})
		assert.False(t, owned)
		assert.NoError(t, err)
		assert.Empty(t, f.gotGetID, "GetDocument must not be called for a dataset reference")
	})

	t.Run("missing document is not-found", func(t *testing.T) {
		f := &fakeDocumentSearcher{docErr: semantic.ErrDocumentNotFound}
		_, owned, err := NewContextDocumentsProvider(f).Fetch(context.Background(), "urn:li:document:gone", Caller{})
		assert.True(t, owned)
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("backend error surfaces as a real error", func(t *testing.T) {
		f := &fakeDocumentSearcher{docErr: errors.New("boom")}
		_, owned, err := NewContextDocumentsProvider(f).Fetch(context.Background(), "urn:li:document:d", Caller{})
		assert.True(t, owned)
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrNotFound)
	})

	t.Run("nil document with no error is not-found", func(t *testing.T) {
		f := &fakeDocumentSearcher{doc: nil} // GetDocument returns (nil, nil)
		_, owned, err := NewContextDocumentsProvider(f).Fetch(context.Background(), "urn:li:document:d", Caller{})
		assert.True(t, owned)
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("an unpublished draft is not-found, matching what search excludes", func(t *testing.T) {
		// Both search arms drop non-PUBLISHED docs via publishedDocument; fetch must
		// too, or a steward's draft becomes readable by URN.
		f := &fakeDocumentSearcher{doc: &semantic.DocumentResult{URN: "urn:li:document:d", Title: "Draft", Status: "UNPUBLISHED", Body: "wip"}}
		_, owned, err := NewContextDocumentsProvider(f).Fetch(context.Background(), "urn:li:document:d", Caller{})
		assert.True(t, owned)
		assert.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("a published-but-hidden document stays fetchable by explicit URN", func(t *testing.T) {
		// ShowInGlobalContext=false hides a doc from broad search but it remains
		// reachable through its linked assets / explicit URN, so fetch returns it.
		f := &fakeDocumentSearcher{doc: &semantic.DocumentResult{URN: "urn:li:document:d", Title: "Hidden", Status: "PUBLISHED", ShowInGlobalContext: false, Body: "full"}}
		doc, owned, err := NewContextDocumentsProvider(f).Fetch(context.Background(), "urn:li:document:d", Caller{})
		assert.True(t, owned)
		require.NoError(t, err)
		assert.Equal(t, "full", doc.Body)
	})

	t.Run("title falls back to sub-type then a generic label", func(t *testing.T) {
		f := &fakeDocumentSearcher{doc: &semantic.DocumentResult{URN: "urn:li:document:d", SubType: "RUNBOOK", Body: "b"}}
		doc, _, err := NewContextDocumentsProvider(f).Fetch(context.Background(), "urn:li:document:d", Caller{})
		require.NoError(t, err)
		assert.Equal(t, "RUNBOOK", doc.Title)

		f2 := &fakeDocumentSearcher{doc: &semantic.DocumentResult{URN: "urn:li:document:d", Body: "b"}}
		doc2, _, err := NewContextDocumentsProvider(f2).Fetch(context.Background(), "urn:li:document:d", Caller{})
		require.NoError(t, err)
		assert.Equal(t, "context document", doc2.Title)
	})
}
