package compactor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whlayout"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// TestPassReplacesEachViewOnce covers #2140: the first pass rewrites every
// source's view, and later passes rewrite none.
func TestPassReplacesEachViewOnce(t *testing.T) {
	r := newRig(t)
	r.sources.list = append(r.sources.list, whsource.Source{Name: "crm", Connection: "warehouse"})
	r.w.Pass(ctx)
	assert.Equal(t, []string{"esp", "crm"}, r.tables.views)
	r.w.Pass(ctx)
	assert.Equal(t, []string{"esp", "crm"}, r.tables.views, "a view made current is not rewritten again")
}

// TestReplaceViewsRetriesOnlyWhatFailed: a source whose view could not be
// rewritten is tried again on the next pass, and one that was is not.
func TestReplaceViewsRetriesOnlyWhatFailed(t *testing.T) {
	r := newRig(t)
	r.tables.err["view"] = errBoom
	err := r.w.ReplaceViews(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "replacing views")
	assert.True(t, r.w.viewsOwed())

	delete(r.tables.err, "view")
	require.NoError(t, r.w.ReplaceViews(ctx))
	assert.Equal(t, []string{"esp"}, r.tables.views)
	assert.False(t, r.w.viewsOwed())

	r.w.viewsCurrent = false
	require.NoError(t, r.w.ReplaceViews(ctx))
	assert.Equal(t, []string{"esp"}, r.tables.views, "a source already rewritten is skipped")
}

// TestReplaceViewsFailures: a source list that cannot be read and a
// connection that cannot be resolved are errors, and the views stay owed.
func TestReplaceViewsFailures(t *testing.T) {
	r := newRig(t)
	r.sources.err = errBoom
	err := r.w.ReplaceViews(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "listing sources to replace their views")

	r = newRig(t)
	r.tables.err["target"] = errBoom
	err = r.w.ReplaceViews(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving the connection of source esp")
	assert.True(t, r.w.viewsOwed())
}

// TestCompactReadsAWindowAnEarlierReleaseMerged: a window holding segments,
// an object an earlier release merged some of them into, and the hidden list
// of what it merged compacts to each event once, never reading the list.
func TestCompactReadsAWindowAnEarlierReleaseMerged(t *testing.T) {
	r := newRig(t)
	r.windows.add("esp", window, 2)
	r.segment(t, window, segAt{"replica-a", 1, 0}, "a", "b")
	r.segment(t, window, segAt{"rollup", 2, 0}, "a", "b", "c")
	dir := whlayout.WindowPrefix("esp", window)
	r.objects.put(dir+".rollup-1792174514131912179.keys", []byte(whlayout.SegmentKey("esp", window, "replica-a", 1)+"\n"))

	assert.True(t, r.w.Pass(ctx))
	row := r.windows.row(whstore.Window{Source: "esp", Start: window})
	require.True(t, row.compacted)
	assert.Equal(t, 2, row.compaction.Segments, "the hidden list is not read as a segment")
	assert.Equal(t, []string{"a", "b", "c"}, r.parquetIDs(t, row.ResourceID))
}
