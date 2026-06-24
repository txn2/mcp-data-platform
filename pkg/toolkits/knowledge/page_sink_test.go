package knowledge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// errKP is a sentinel error for page-sink error-path tests.
var errKP = errors.New("kp boom")

// fakePageWriter is an in-memory pageWriter for sink-router tests.
type fakePageWriter struct {
	pages     map[string]*knowledgepage.Page       // slug -> page
	refs      map[string][]knowledgepage.EntityRef // pageID -> refs
	inserted  []string
	updated   []string
	deleted   []string
	insertErr error
	updateErr error
	getErr    error
}

func newFakePageWriter() *fakePageWriter {
	return &fakePageWriter{pages: map[string]*knowledgepage.Page{}, refs: map[string][]knowledgepage.EntityRef{}}
}

// fakeRefKey mirrors the store's per-target uniqueness for the in-memory union.
func fakeRefKey(r knowledgepage.EntityRef) string {
	return r.TargetType + "|" + r.AssetID + "|" + r.PromptID + "|" + r.CollectionID + "|" +
		r.RefPageID + "|" + r.ConnectionKind + "/" + r.ConnectionName + "|" + r.EntityURN
}

func (f *fakePageWriter) ListEntityRefs(_ context.Context, pageID string) ([]knowledgepage.EntityRef, error) {
	return f.refs[pageID], nil
}

func (f *fakePageWriter) AddEntityRefs(_ context.Context, pageID string, refs []knowledgepage.EntityRef) error {
	seen := map[string]bool{}
	for _, e := range f.refs[pageID] {
		seen[fakeRefKey(e)] = true
	}
	for _, r := range refs {
		if seen[fakeRefKey(r)] {
			continue
		}
		f.refs[pageID] = append(f.refs[pageID], r)
		seen[fakeRefKey(r)] = true
	}
	return nil
}

func (f *fakePageWriter) ReplaceEntityRefs(_ context.Context, pageID string, refs []knowledgepage.EntityRef) error {
	f.refs[pageID] = append([]knowledgepage.EntityRef{}, refs...)
	return nil
}

func (f *fakePageWriter) GetBySlug(_ context.Context, slug string) (*knowledgepage.Page, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if p, ok := f.pages[slug]; ok {
		return p, nil
	}
	return nil, knowledgepage.ErrNotFound
}

func (f *fakePageWriter) Insert(_ context.Context, p knowledgepage.Page) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	cp := p
	if cp.CurrentVersion == 0 {
		cp.CurrentVersion = 1
	}
	f.pages[p.Slug] = &cp
	f.inserted = append(f.inserted, p.ID)
	return nil
}

func (f *fakePageWriter) Update(_ context.Context, id string, u knowledgepage.Update) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	f.updated = append(f.updated, id)
	for _, p := range f.pages {
		if p.ID != id {
			continue
		}
		if u.Title != nil {
			p.Title = *u.Title
		}
		if u.Body != nil {
			p.Body = *u.Body
		}
		p.CurrentVersion++
	}
	return nil
}

func (f *fakePageWriter) SoftDelete(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}

func refURNs(refs []knowledgepage.EntityRef) []string {
	urns := make([]string, 0, len(refs))
	for _, r := range refs {
		urns = append(urns, r.EntityURN)
	}
	return urns
}

// TestPromoteToPage_CarriesAndUnionsInsightRefs proves #664's core: a promoted
// insight's entity_urns land on the page as promoted DataHub references, survive
// into the changeset after-image, and union (no duplicates) across promotions.
func TestPromoteToPage_CarriesAndUnionsInsightRefs(t *testing.T) {
	urnA := "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.a,PROD)"
	urnB := "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.b,PROD)"
	store := &fullSpyStore{Insights: []Insight{
		{ID: "i1", SinkClass: memory.SinkBusinessKnowledge, EntityURNs: []string{urnA}},
		{ID: "i2", SinkClass: memory.SinkBusinessKnowledge, EntityURNs: []string{urnA, urnB}},
	}}
	cs := &spyChangesetStore{}
	pw := newFakePageWriter()
	tk := newApplyToolkit(t, store, cs, &spyWriter{})
	tk.SetPageWriter(pw)

	// First promotion creates the page carrying urnA.
	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput([]string{"i1"}))
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error result")
	page := pw.pages["seasons"]
	require.NotNil(t, page)
	require.Len(t, pw.refs[page.ID], 1)
	assert.Equal(t, urnA, pw.refs[page.ID][0].EntityURN)
	assert.Equal(t, knowledgepage.RefTargetDataHub, pw.refs[page.ID][0].TargetType)
	assert.Equal(t, knowledgepage.RefSourcePromoted, pw.refs[page.ID][0].Source)

	// Second promotion to the same slug unions urnB; urnA is not duplicated.
	res2, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput([]string{"i2"}))
	require.NoError(t, err)
	require.False(t, res2.IsError, "unexpected error result")
	assert.ElementsMatch(t, []string{urnA, urnB}, refURNs(pw.refs[page.ID]))

	// The changeset after-image carries the page's full URN set.
	require.Len(t, cs.Changesets, 2)
	gotURNs, ok := cs.Changesets[1].NewValue[pageFieldEntityURNs].([]string)
	require.True(t, ok, "after-image should carry entity_urns as []string")
	assert.ElementsMatch(t, []string{urnA, urnB}, gotURNs)
}

func applyPageInput(insightIDs []string) applyKnowledgeInput {
	return applyKnowledgeInput{
		Action:     actionApply,
		Sink:       sinkKnowledgePage,
		InsightIDs: insightIDs,
		Page:       &pagePromotionInput{Slug: "seasons", Title: "Seasons", Body: "# Seasons\n\nQ1 starts in Feb."},
	}
}

func pageCtx() context.Context { return ctxWithUser("admin@example.com", "sess", "admin") }

func TestPromoteToPage_CreatesNewPage(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{{ID: "i1", SinkClass: memory.SinkBusinessKnowledge}}}
	cs := &spyChangesetStore{}
	pw := newFakePageWriter()
	tk := newApplyToolkit(t, store, cs, &spyWriter{})
	tk.SetPageWriter(pw)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput([]string{"i1"}))
	require.NoError(t, err)
	require.False(t, res.IsError, "unexpected error result")
	out := parseJSONResult(t, res)

	assert.Equal(t, "created", out["action"])
	assert.NotEmpty(t, out["changeset_id"])
	assert.NotEmpty(t, out["page_id"])
	require.Contains(t, pw.pages, "seasons")
	require.Len(t, pw.inserted, 1)
	assert.Equal(t, out["page_id"], pw.inserted[0])
	require.Len(t, cs.Changesets, 1)
	assert.Equal(t, pageTargetPrefix+"seasons", cs.Changesets[0].TargetURN)
	assert.Equal(t, changeCreatePage, cs.Changesets[0].ChangeType)
	require.Len(t, store.MarkAppliedCalls, 1)
	assert.Equal(t, "i1", store.MarkAppliedCalls[0].ID)
	// Origin sink-class is tagged on the page.
	assert.Contains(t, pw.pages["seasons"].Tags, memory.SinkBusinessKnowledge)
}

func TestPromoteToPage_UpdatesExistingBySlug(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{{ID: "i1", SinkClass: memory.SinkOperationalRule}}}
	cs := &spyChangesetStore{}
	pw := newFakePageWriter()
	pw.pages["seasons"] = &knowledgepage.Page{ID: "kp-existing", Slug: "seasons", Title: "Old", Body: "old body", CurrentVersion: 3}
	tk := newApplyToolkit(t, store, cs, &spyWriter{})
	tk.SetPageWriter(pw)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput([]string{"i1"}))
	require.NoError(t, err)
	out := parseJSONResult(t, res)

	assert.Equal(t, "updated", out["action"])
	assert.Equal(t, "kp-existing", out["page_id"])
	require.Len(t, cs.Changesets, 1)
	c := cs.Changesets[0]
	assert.Equal(t, changeUpdatePage, c.ChangeType)
	assert.Equal(t, "Old", c.PreviousValue[pageFieldTitle])
	assert.EqualValues(t, 4, c.NewValue[pageFieldVersion]) // existing 3 -> produced 4
	assert.Contains(t, pw.updated, "kp-existing")
}

func TestPromoteToPage_RejectsSchemaEntityInsight(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{{ID: "i1", SinkClass: memory.SinkSchemaEntity}}}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})
	tk.SetPageWriter(newFakePageWriter())
	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput([]string{"i1"}))
	require.NoError(t, err)
	assert.True(t, res.IsError, "schema_entity insight must be rejected from the page sink")
}

func TestPromoteToPage_NotConfigured(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{})
	// no SetPageWriter
	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput(nil))
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestPromoteToPage_Validation(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{})
	tk.SetPageWriter(newFakePageWriter())
	cases := []applyKnowledgeInput{
		{Action: actionApply, Sink: sinkKnowledgePage},                                                                                                                 // nil page
		{Action: actionApply, Sink: sinkKnowledgePage, Page: &pagePromotionInput{Title: "T", Body: "B"}},                                                               // no slug
		{Action: actionApply, Sink: sinkKnowledgePage, Page: &pagePromotionInput{Slug: "s", Body: "B"}},                                                                // no title
		{Action: actionApply, Sink: sinkKnowledgePage, Page: &pagePromotionInput{Slug: "s", Title: "T"}},                                                               // no body
		{Action: actionApply, Sink: sinkKnowledgePage, Page: &pagePromotionInput{Slug: strings.Repeat("x", maxPageSlugLen+1), Title: "T", Body: "B"}},                  // slug too long
		{Action: actionApply, Sink: sinkKnowledgePage, Page: &pagePromotionInput{Slug: "s", Title: strings.Repeat("x", maxPageTitleLen+1), Body: "B"}},                 // title too long
		{Action: actionApply, Sink: sinkKnowledgePage, Page: &pagePromotionInput{Slug: "s", Title: "T", Summary: strings.Repeat("x", maxPageSummaryLen+1), Body: "B"}}, // summary too long
		{Action: actionApply, Sink: sinkKnowledgePage, Page: &pagePromotionInput{Slug: "s", Title: "T", Body: strings.Repeat("x", maxPageBodyLen+1)}},                  // body too long
	}
	for i, in := range cases {
		res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, in)
		require.NoError(t, err)
		assert.Truef(t, res.IsError, "case %d should be an error", i)
	}
}

func TestPromoteToPage_ConfirmationRequired(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{{ID: "i1", SinkClass: memory.SinkBusinessKnowledge}}}
	tk := newApplyToolkit(t, store, &spyChangesetStore{}, &spyWriter{})
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, &spyChangesetStore{}, &spyWriter{})
	tk.SetPageWriter(newFakePageWriter())
	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput([]string{"i1"}))
	require.NoError(t, err)
	out := parseJSONResult(t, res)
	assert.Equal(t, true, out["confirmation_required"])
}

func TestHandleApply_UnknownSink(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{})
	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyKnowledgeInput{Action: actionApply, Sink: "bogus"})
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// --- page rollback ---

func pageChangeset(slug, changeType string, producedVersion int, prev map[string]any) Changeset {
	return Changeset{
		ID:               "cs1",
		TargetURN:        pageTargetPrefix + slug,
		ChangeType:       changeType,
		PreviousValue:    prev,
		NewValue:         map[string]any{pageFieldVersion: float64(producedVersion)},
		SourceInsightIDs: []string{"i1"},
	}
}

func TestRevertPageChangeset_CreateDeletes(t *testing.T) {
	cs := pageChangeset("seasons", changeCreatePage, 1, map[string]any{})
	pw := newFakePageWriter()
	pw.pages["seasons"] = &knowledgepage.Page{ID: "kp1", Slug: "seasons", CurrentVersion: 1}
	store := &fullSpyStore{}
	csStore := &spyChangesetStore{Changesets: []Changeset{cs}}
	tk := newApplyToolkit(t, store, csStore, &spyWriter{})
	tk.SetPageWriter(pw)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyKnowledgeInput{Action: actionRollback, ChangesetID: "cs1", Confirm: true})
	require.NoError(t, err)
	require.False(t, res.IsError, "rollback should succeed")
	assert.Contains(t, pw.deleted, "kp1")
	assert.True(t, csStore.Changesets[0].RolledBack)
	assert.Contains(t, store.MarkRolledBackIDs, "i1")
}

func TestRevertPageChangeset_UpdateRestores(t *testing.T) {
	prev := map[string]any{pageFieldTitle: "Old", pageFieldBody: "old body", pageFieldSummary: "", pageFieldTags: []any{}}
	cs := pageChangeset("seasons", changeUpdatePage, 4, prev)
	pw := newFakePageWriter()
	pw.pages["seasons"] = &knowledgepage.Page{ID: "kp1", Slug: "seasons", Title: "New", Body: "new body", CurrentVersion: 4}
	csStore := &spyChangesetStore{Changesets: []Changeset{cs}}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, &spyWriter{})
	tk.SetPageWriter(pw)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyKnowledgeInput{Action: actionRollback, ChangesetID: "cs1", Confirm: true})
	require.NoError(t, err)
	require.False(t, res.IsError)
	assert.Contains(t, pw.updated, "kp1")
	assert.Equal(t, "Old", pw.pages["seasons"].Title)
	assert.True(t, csStore.Changesets[0].RolledBack)
}

// TestRevertPageChangeset_RestoresRefs proves a page rollback restores the prior
// reference set from the changeset previous-value (#664).
func TestRevertPageChangeset_RestoresRefs(t *testing.T) {
	urnA := "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.a,PROD)"
	urnB := "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.b,PROD)"
	prev := map[string]any{
		pageFieldTitle: "Old", pageFieldBody: "old body", pageFieldSummary: "", pageFieldTags: []any{},
		pageFieldEntityURNs: []any{urnA}, // []any mirrors a JSONB-decoded changeset value
	}
	cs := pageChangeset("seasons", changeUpdatePage, 4, prev)
	pw := newFakePageWriter()
	pw.pages["seasons"] = &knowledgepage.Page{ID: "kp1", Slug: "seasons", Title: "New", CurrentVersion: 4}
	// The page currently has both URNs (urnB was added by the promotion being reverted).
	pw.refs["kp1"] = []knowledgepage.EntityRef{
		knowledgepage.DataHubRef(urnA, knowledgepage.RefSourcePromoted),
		knowledgepage.DataHubRef(urnB, knowledgepage.RefSourcePromoted),
	}
	csStore := &spyChangesetStore{Changesets: []Changeset{cs}}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, &spyWriter{})
	tk.SetPageWriter(pw)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyKnowledgeInput{Action: actionRollback, ChangesetID: "cs1", Confirm: true})
	require.NoError(t, err)
	require.False(t, res.IsError)
	assert.ElementsMatch(t, []string{urnA}, refURNs(pw.refs["kp1"]), "rollback should restore the prior ref set")
}

func TestRevertPageChangeset_ConflictRefused(t *testing.T) {
	cs := pageChangeset("seasons", changeUpdatePage, 4, map[string]any{pageFieldTitle: "Old"})
	pw := newFakePageWriter()
	pw.pages["seasons"] = &knowledgepage.Page{ID: "kp1", Slug: "seasons", CurrentVersion: 7} // edited since
	csStore := &spyChangesetStore{Changesets: []Changeset{cs}}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, &spyWriter{})
	tk.SetPageWriter(pw)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyKnowledgeInput{Action: actionRollback, ChangesetID: "cs1", Confirm: true})
	require.NoError(t, err)
	assert.True(t, res.IsError, "rollback must be refused when the page was edited after promotion")
	assert.False(t, csStore.Changesets[0].RolledBack)
	assert.Empty(t, pw.updated)
}

func TestRevertPageChangeset_AlreadyRolledBack(t *testing.T) {
	cs := pageChangeset("seasons", changeCreatePage, 1, map[string]any{})
	cs.RolledBack = true
	pw := newFakePageWriter()
	pw.pages["seasons"] = &knowledgepage.Page{ID: "kp1", Slug: "seasons", CurrentVersion: 1}
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{Changesets: []Changeset{cs}}, &spyWriter{})
	tk.SetPageWriter(pw)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyKnowledgeInput{Action: actionRollback, ChangesetID: "cs1", Confirm: true})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Empty(t, pw.deleted)
}

func TestPromoteToPage_StoreErrors(t *testing.T) {
	bk := []Insight{{ID: "i1", SinkClass: memory.SinkBusinessKnowledge}}
	t.Run("insert error", func(t *testing.T) {
		pw := newFakePageWriter()
		pw.insertErr = errKP
		tk := newApplyToolkit(t, &fullSpyStore{Insights: bk}, &spyChangesetStore{}, &spyWriter{})
		tk.SetPageWriter(pw)
		res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput([]string{"i1"}))
		require.NoError(t, err)
		assert.True(t, res.IsError)
	})
	t.Run("update error", func(t *testing.T) {
		pw := newFakePageWriter()
		pw.pages["seasons"] = &knowledgepage.Page{ID: "kp1", Slug: "seasons", CurrentVersion: 1}
		pw.updateErr = errKP
		tk := newApplyToolkit(t, &fullSpyStore{Insights: bk}, &spyChangesetStore{}, &spyWriter{})
		tk.SetPageWriter(pw)
		res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput([]string{"i1"}))
		require.NoError(t, err)
		assert.True(t, res.IsError)
	})
	t.Run("get error", func(t *testing.T) {
		pw := newFakePageWriter()
		pw.getErr = errKP
		tk := newApplyToolkit(t, &fullSpyStore{Insights: bk}, &spyChangesetStore{}, &spyWriter{})
		tk.SetPageWriter(pw)
		res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput([]string{"i1"}))
		require.NoError(t, err)
		assert.True(t, res.IsError)
	})
	t.Run("changeset insert error", func(t *testing.T) {
		tk := newApplyToolkit(t, &fullSpyStore{Insights: bk}, &spyChangesetStore{InsertErr: errKP}, &spyWriter{})
		tk.SetPageWriter(newFakePageWriter())
		res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput([]string{"i1"}))
		require.NoError(t, err)
		assert.True(t, res.IsError)
	})
	t.Run("missing insight", func(t *testing.T) {
		tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{})
		tk.SetPageWriter(newFakePageWriter())
		res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{}, applyPageInput([]string{"nope"}))
		require.NoError(t, err)
		assert.True(t, res.IsError)
	})
}

func TestRevertPageChangeset_ErrorBranches(t *testing.T) {
	t.Run("page gone", func(t *testing.T) {
		cs := pageChangeset("gone", changeUpdatePage, 1, map[string]any{})
		tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{Changesets: []Changeset{cs}}, &spyWriter{})
		tk.SetPageWriter(newFakePageWriter())
		res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
			applyKnowledgeInput{Action: actionRollback, ChangesetID: "cs1", Confirm: true})
		require.NoError(t, err)
		assert.True(t, res.IsError)
	})
	t.Run("not configured", func(t *testing.T) {
		cs := pageChangeset("seasons", changeCreatePage, 1, map[string]any{})
		tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{Changesets: []Changeset{cs}}, &spyWriter{})
		// no SetPageWriter
		res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
			applyKnowledgeInput{Action: actionRollback, ChangesetID: "cs1", Confirm: true})
		require.NoError(t, err)
		assert.True(t, res.IsError)
	})
	t.Run("rollback record error", func(t *testing.T) {
		cs := pageChangeset("seasons", changeCreatePage, 1, map[string]any{})
		pw := newFakePageWriter()
		pw.pages["seasons"] = &knowledgepage.Page{ID: "kp1", Slug: "seasons", CurrentVersion: 1}
		tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{Changesets: []Changeset{cs}, RollbackErr: errKP}, &spyWriter{})
		tk.SetPageWriter(pw)
		res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
			applyKnowledgeInput{Action: actionRollback, ChangesetID: "cs1", Confirm: true})
		require.NoError(t, err)
		assert.True(t, res.IsError)
	})
}

func TestPageMapAccessors(t *testing.T) {
	assert.Equal(t, 4, intFromMap(map[string]any{"v": float64(4)}, "v"))
	assert.Equal(t, 7, intFromMap(map[string]any{"v": 7}, "v"))
	assert.Equal(t, 0, intFromMap(map[string]any{"v": "nan"}, "v"))
	assert.Equal(t, []string{"a", "b"}, strsFromMap(map[string]any{"t": []any{"a", "b", 3}}, "t"))
	assert.Equal(t, []string{"c"}, strsFromMap(map[string]any{"t": []string{"c"}}, "t"))
	assert.Equal(t, []string{}, strsFromMap(map[string]any{"t": "notarray"}, "t"))
}

func TestTagsWithOrigin(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "business_knowledge"}, tagsWithOrigin([]string{"a", "b", "a"}, "business_knowledge"))
	assert.Equal(t, []string{"a"}, tagsWithOrigin([]string{"a"}, ""))
	assert.Equal(t, []string{"x"}, tagsWithOrigin([]string{"x"}, "x")) // origin already present
}
