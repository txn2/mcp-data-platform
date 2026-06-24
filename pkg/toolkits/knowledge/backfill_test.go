package knowledge

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// fakeBackfillStore is a knowledgepage.Store that records the inline and promoted
// references the backfill writes; the unused methods are stubs.
type fakeBackfillStore struct {
	pages       []knowledgepage.Page
	inline      map[string][]knowledgepage.EntityRef
	promoted    map[string][]knowledgepage.EntityRef
	listErr     error
	inlineErr   error // ReplaceEntityRefsBySource error (best-effort path)
	promotedErr error // AddEntityRefs error (best-effort path)
	listCalls   int
}

func newFakeBackfillStore(pages ...knowledgepage.Page) *fakeBackfillStore {
	return &fakeBackfillStore{
		pages:    pages,
		inline:   map[string][]knowledgepage.EntityRef{},
		promoted: map[string][]knowledgepage.EntityRef{},
	}
}

// List honors Offset and Limit so the backfill's pagination is exercised (and
// terminates), mirroring the real store rather than masking it.
func (f *fakeBackfillStore) List(_ context.Context, filter knowledgepage.Filter) ([]knowledgepage.Page, int, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	start := min(filter.Offset, len(f.pages))
	end := start + filter.Limit
	if filter.Limit <= 0 || end > len(f.pages) {
		end = len(f.pages)
	}
	return f.pages[start:end], len(f.pages), nil
}

func (f *fakeBackfillStore) ReplaceEntityRefsBySource(_ context.Context, pageID, _ string, refs []knowledgepage.EntityRef) error {
	if f.inlineErr != nil {
		return f.inlineErr
	}
	f.inline[pageID] = refs
	return nil
}

func (f *fakeBackfillStore) AddEntityRefs(_ context.Context, pageID string, refs []knowledgepage.EntityRef) error {
	if f.promotedErr != nil {
		return f.promotedErr
	}
	f.promoted[pageID] = append(f.promoted[pageID], refs...)
	return nil
}

// --- unused Store methods (stubs).
func (*fakeBackfillStore) Insert(context.Context, knowledgepage.Page) error { return nil }

func (*fakeBackfillStore) Get(context.Context, string) (*knowledgepage.Page, error) {
	return nil, knowledgepage.ErrNotFound
}

func (*fakeBackfillStore) GetBySlug(context.Context, string) (*knowledgepage.Page, error) {
	return nil, knowledgepage.ErrNotFound
}
func (*fakeBackfillStore) Update(context.Context, string, knowledgepage.Update) error { return nil }
func (*fakeBackfillStore) SoftDelete(context.Context, string) error                   { return nil }
func (*fakeBackfillStore) ListVersions(context.Context, string, int, int) ([]knowledgepage.Version, int, error) {
	return nil, 0, nil
}

func (*fakeBackfillStore) GetVersion(context.Context, string, int) (*knowledgepage.Version, error) {
	return nil, knowledgepage.ErrNotFound
}

func (*fakeBackfillStore) ListEntityRefs(context.Context, string) ([]knowledgepage.EntityRef, error) {
	return nil, nil
}

func (*fakeBackfillStore) ReplaceEntityRefs(context.Context, string, []knowledgepage.EntityRef) error {
	return nil
}

func (*fakeBackfillStore) ListPagesReferencing(context.Context, knowledgepage.EntityRef) ([]knowledgepage.PageRef, error) {
	return nil, nil
}

// filterCSStore is a ChangesetStore that respects the EntityURN (target) filter,
// so the backfill's per-page changeset query is exercised correctly.
type filterCSStore struct{ byTarget map[string][]Changeset }

func (s *filterCSStore) ListChangesets(_ context.Context, f ChangesetFilter) ([]Changeset, int, error) {
	cs := s.byTarget[f.EntityURN]
	return cs, len(cs), nil
}
func (*filterCSStore) InsertChangeset(context.Context, Changeset) error { return nil }
func (*filterCSStore) GetChangeset(context.Context, string) (*Changeset, error) {
	return nil, nil //nolint:nilnil // unused stub
}
func (*filterCSStore) RollbackChangeset(context.Context, string, string) error { return nil }

func TestBackfillPageRefs(t *testing.T) {
	store := newFakeBackfillStore(
		// kp1 has an inline body ref AND a changeset source insight (promoted).
		knowledgepage.Page{ID: "kp1", Slug: "fiscal", Body: "see [a](mcp:asset:asset-1) and urn:li:glossaryTerm:revenue"},
		// kp2 has neither -> no refs.
		knowledgepage.Page{ID: "kp2", Slug: "empty", Body: "just prose"},
	)
	csStore := &filterCSStore{byTarget: map[string][]Changeset{
		"kp:fiscal": {{TargetURN: "kp:fiscal", ChangeType: changeCreatePage, SourceInsightIDs: []string{"ins-1"}}},
	}}
	insightStore := &fullSpyStore{Insights: []Insight{
		// A duplicate and an empty URN exercise the de-dup/skip path.
		{ID: "ins-1", EntityURNs: []string{"urn:li:dataset:promoted-x", "urn:li:dataset:promoted-x", ""}},
	}}
	tk := newApplyToolkit(t, insightStore, csStore, &spyWriter{})

	stats, err := tk.BackfillPageRefs(context.Background(), store)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.PagesScanned)

	// kp1 inline: the two body references reconciled as inline.
	inline := refURNset(store.inline["kp1"])
	assert.Contains(t, inline, "mcp:asset:asset-1")
	assert.Contains(t, inline, "urn:li:glossaryTerm:revenue")
	// kp1 promoted: the insight's entity URN, source=promoted.
	require.Len(t, store.promoted["kp1"], 1)
	assert.Equal(t, "urn:li:dataset:promoted-x", store.promoted["kp1"][0].EntityURN)
	assert.Equal(t, knowledgepage.RefSourcePromoted, store.promoted["kp1"][0].Source)

	// kp2: inline reconciled to empty (no refs), no promoted.
	assert.Empty(t, store.inline["kp2"])
	assert.Empty(t, store.promoted["kp2"])
}

func TestBackfillPageRefs_EdgesAndError(t *testing.T) {
	// A List error aborts the backfill.
	errStore := newFakeBackfillStore()
	errStore.listErr = errors.New("list boom")
	tk := newApplyToolkit(t, &fullSpyStore{}, &filterCSStore{byTarget: map[string][]Changeset{}}, &spyWriter{})
	_, err := tk.BackfillPageRefs(context.Background(), errStore)
	require.Error(t, err)

	// An empty-slug page gets no promoted refs (no changeset link), and a
	// changeset whose source insight is gone is skipped, not fatal.
	store := newFakeBackfillStore(
		knowledgepage.Page{ID: "kp3", Slug: "", Body: "no refs"},
		knowledgepage.Page{ID: "kp4", Slug: "has-cs", Body: ""},
	)
	cs := &filterCSStore{byTarget: map[string][]Changeset{
		"kp:has-cs": {{SourceInsightIDs: []string{"missing-insight"}}},
	}}
	tk2 := newApplyToolkit(t, &fullSpyStore{}, cs, &spyWriter{}) // no insights -> Get not found
	stats, err := tk2.BackfillPageRefs(context.Background(), store)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.PagesScanned)
	assert.Empty(t, store.promoted["kp3"])
	assert.Empty(t, store.promoted["kp4"])
}

func TestBackfillPageRefs_Paginates(t *testing.T) {
	orig := backfillPageBatch
	backfillPageBatch = 2
	defer func() { backfillPageBatch = orig }()

	pages := make([]knowledgepage.Page, 5)
	for i := range pages {
		pages[i] = knowledgepage.Page{ID: fmt.Sprintf("kp%d", i), Slug: fmt.Sprintf("s%d", i)}
	}
	store := newFakeBackfillStore(pages...)
	tk := newApplyToolkit(t, &fullSpyStore{}, &filterCSStore{byTarget: map[string][]Changeset{}}, &spyWriter{})

	stats, err := tk.BackfillPageRefs(context.Background(), store)
	require.NoError(t, err)
	assert.Equal(t, 5, stats.PagesScanned, "every page across all batches is processed")
	assert.GreaterOrEqual(t, store.listCalls, 3, "should page through (2 + 2 + 1)")
}

func TestBackfillPageRefs_BestEffortOnInlineError(t *testing.T) {
	store := newFakeBackfillStore(knowledgepage.Page{ID: "kp1", Slug: "s", Body: "[a](mcp:asset:x)"})
	store.inlineErr = errors.New("FK violation: asset gone")
	tk := newApplyToolkit(t, &fullSpyStore{}, &filterCSStore{byTarget: map[string][]Changeset{}}, &spyWriter{})

	stats, err := tk.BackfillPageRefs(context.Background(), store)
	require.NoError(t, err, "a per-page inline error must not abort the pass")
	assert.Equal(t, 1, stats.PagesScanned)
	assert.Zero(t, stats.InlineRefs, "a failed inline reconcile is not counted")
}

func TestBackfillPageRefs_BestEffortOnPromotedError(t *testing.T) {
	store := newFakeBackfillStore(knowledgepage.Page{ID: "kp1", Slug: "fiscal", Body: ""})
	store.promotedErr = errors.New("FK violation")
	cs := &filterCSStore{byTarget: map[string][]Changeset{
		"kp:fiscal": {{SourceInsightIDs: []string{"ins-1"}}},
	}}
	insights := &fullSpyStore{Insights: []Insight{{ID: "ins-1", EntityURNs: []string{"urn:li:dataset:x"}}}}
	tk := newApplyToolkit(t, insights, cs, &spyWriter{})

	stats, err := tk.BackfillPageRefs(context.Background(), store)
	require.NoError(t, err, "a per-page promoted-add error must not abort the pass")
	assert.Equal(t, 1, stats.PagesScanned)
	assert.Zero(t, stats.PromotedRefs)
}

func TestRunGuardedBackfill(t *testing.T) {
	emptyCS := func() *filterCSStore { return &filterCSStore{byTarget: map[string][]Changeset{}} }

	t.Run("runs the backfill and marks the sentinel when not yet done", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("SELECT EXISTS").WithArgs("kp_entity_refs_v1").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectExec("INSERT INTO platform_backfills").WithArgs("kp_entity_refs_v1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		store := newFakeBackfillStore(knowledgepage.Page{ID: "kp1", Slug: "s", Body: "[a](mcp:asset:x)"})
		tk := newApplyToolkit(t, &fullSpyStore{}, emptyCS(), &spyWriter{})
		tk.RunGuardedBackfill(context.Background(), db, store)

		assert.Len(t, store.inline["kp1"], 1, "backfill should have reconciled the inline ref")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("skips when the sentinel already exists", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("SELECT EXISTS").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		store := newFakeBackfillStore(knowledgepage.Page{ID: "kp1", Body: "[a](mcp:asset:x)"})
		tk := newApplyToolkit(t, &fullSpyStore{}, emptyCS(), &spyWriter{})
		tk.RunGuardedBackfill(context.Background(), db, store)

		assert.Empty(t, store.inline["kp1"], "backfill must be skipped when already done")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("sentinel-check error is logged, not fatal", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("SELECT EXISTS").WillReturnError(errors.New("db down"))
		tk := newApplyToolkit(t, &fullSpyStore{}, emptyCS(), &spyWriter{})
		tk.RunGuardedBackfill(context.Background(), db, newFakeBackfillStore())
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("backfill error leaves the sentinel unset", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("SELECT EXISTS").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		// No ExpectExec: the backfill fails before the sentinel is marked.
		errStore := newFakeBackfillStore()
		errStore.listErr = errors.New("list boom")
		tk := newApplyToolkit(t, &fullSpyStore{}, emptyCS(), &spyWriter{})
		tk.RunGuardedBackfill(context.Background(), db, errStore)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("mark error is logged, not fatal", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("SELECT EXISTS").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		mock.ExpectExec("INSERT INTO platform_backfills").WillReturnError(errors.New("insert boom"))
		tk := newApplyToolkit(t, &fullSpyStore{}, emptyCS(), &spyWriter{})
		tk.RunGuardedBackfill(context.Background(), db, newFakeBackfillStore())
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("nil db is a no-op", func(t *testing.T) {
		tk := newApplyToolkit(t, &fullSpyStore{}, emptyCS(), &spyWriter{})
		tk.RunGuardedBackfill(context.Background(), nil, newFakeBackfillStore())
	})
}

func refURNset(refs []knowledgepage.EntityRef) []string {
	urns := make([]string, 0, len(refs))
	for _, r := range refs {
		urns = append(urns, r.URN())
	}
	return urns
}
