package sessionview

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKindOf(t *testing.T) {
	tests := []struct {
		id   string
		want Kind
	}{
		{"dps_9f2c1a4b", KindAgent},
		{"dpp_9f2c1a4b", KindPortal},
		{"dpx_9f2c1a4b", KindScript},
		{"9f2c1a4b8e7d", KindTransport},
		{"", KindTransport},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			assert.Equal(t, tt.want, KindOf(tt.id))
		})
	}
}

func TestPrefixForKind(t *testing.T) {
	for _, k := range []Kind{KindAgent, KindPortal, KindScript} {
		prefix, ok := prefixForKind(k)
		require.True(t, ok, "%s is matched by a prefix", k)
		assert.Equal(t, k, KindOf(prefix+"abc"), "the prefix round-trips to its kind")
	}
	for _, k := range []Kind{KindTransport, Kind("bogus")} {
		_, ok := prefixForKind(k)
		assert.False(t, ok, "%q has no prefix", k)
	}
}

// fakeStore records the calls Load makes and returns canned answers.
type fakeStore struct {
	summary  *Summary
	timeline []TimelineEntry
	total    int
	assets   []AssetRef
	insights []InsightRef

	getErr      error
	timelineErr error
	assetsErr   error
	insightsErr error

	gotLimit  int
	gotOffset int
}

func (*fakeStore) List(context.Context, Filter) ([]Summary, error) { return nil, nil }
func (*fakeStore) Count(context.Context, Filter) (int, error)      { return 0, nil }

// Get models the real store's not-found contract: no summary means
// ErrNotFound, never a nil summary with a nil error.
func (f *fakeStore) Get(context.Context, string) (*Summary, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.summary == nil {
		return nil, ErrNotFound
	}
	return f.summary, nil
}

func (f *fakeStore) Timeline(_ context.Context, _ string, limit, offset int) ([]TimelineEntry, int, error) {
	f.gotLimit, f.gotOffset = limit, offset
	return f.timeline, f.total, f.timelineErr
}

func (f *fakeStore) Assets(context.Context, string) ([]AssetRef, error) {
	return f.assets, f.assetsErr
}

func (f *fakeStore) Insights(context.Context, string) ([]InsightRef, error) {
	return f.insights, f.insightsErr
}

func TestLoad_AssemblesDetail(t *testing.T) {
	store := &fakeStore{
		summary:  &Summary{SessionID: "dps_abc", CallCount: 5},
		timeline: []TimelineEntry{{EventID: "evt-1", ToolName: "search"}},
		total:    5,
		assets:   []AssetRef{{ID: "ast_1"}},
		insights: []InsightRef{{ID: "ins_1"}},
	}

	detail, err := Load(context.Background(), store, "dps_abc", 25, 50)
	require.NoError(t, err)
	assert.Equal(t, "dps_abc", detail.SessionID)
	assert.Equal(t, 5, detail.TimelineTotal)
	require.Len(t, detail.Timeline, 1)
	require.Len(t, detail.Assets, 1)
	require.Len(t, detail.Insights, 1)
	assert.Equal(t, 25, store.gotLimit, "the timeline page reaches the store")
	assert.Equal(t, 50, store.gotOffset)
}

func TestLoad_NotFound(t *testing.T) {
	_, err := Load(context.Background(), &fakeStore{}, "dps_missing", 25, 0)
	assert.ErrorIs(t, err, ErrNotFound)
}

// Every read Load makes must surface its failure: a partially assembled
// session reported as complete is worse than an error.
func TestLoad_PropagatesEveryReadError(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name  string
		store *fakeStore
	}{
		{"summary", &fakeStore{getErr: boom}},
		{"timeline", &fakeStore{summary: &Summary{}, timelineErr: boom}},
		{"assets", &fakeStore{summary: &Summary{}, assetsErr: boom}},
		{"insights", &fakeStore{summary: &Summary{}, insightsErr: boom}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(context.Background(), tt.store, "dps_abc", 25, 0)
			assert.ErrorIs(t, err, boom)
		})
	}
}
