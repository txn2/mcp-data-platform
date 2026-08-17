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

	gotGetScope      Scope
	gotTimelineScope Scope
	gotAssetsID      string
	gotInsightsID    string
}

func (*fakeStore) List(context.Context, Filter) ([]Summary, error) { return nil, nil }
func (*fakeStore) Count(context.Context, Filter) (int, error)      { return 0, nil }

// Get models the real store's not-found contract: no summary means
// ErrNotFound, never a nil summary with a nil error.
func (f *fakeStore) Get(_ context.Context, scope Scope) (*Summary, error) {
	f.gotGetScope = scope
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.summary == nil {
		return nil, ErrNotFound
	}
	return f.summary, nil
}

func (f *fakeStore) Timeline(_ context.Context, scope Scope) ([]TimelineEntry, int, error) {
	f.gotTimelineScope = scope
	return f.timeline, f.total, f.timelineErr
}

func (f *fakeStore) Assets(_ context.Context, sessionID string) ([]AssetRef, error) {
	f.gotAssetsID = sessionID
	return f.assets, f.assetsErr
}

func (f *fakeStore) Insights(_ context.Context, sessionID string) ([]InsightRef, error) {
	f.gotInsightsID = sessionID
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

	scope := Scope{SessionID: "dps_abc", UserID: "user-a", Limit: 25, Offset: 50}
	detail, err := Load(context.Background(), store, scope)
	require.NoError(t, err)
	assert.Equal(t, "dps_abc", detail.SessionID)
	assert.Equal(t, 5, detail.TimelineTotal)
	require.Len(t, detail.Timeline, 1)
	require.Len(t, detail.Assets, 1)
	require.Len(t, detail.Insights, 1)
	assert.Equal(t, scope, store.gotTimelineScope, "the timeline page reaches the store")
	assert.Equal(t, "dps_abc", store.gotAssetsID)
	assert.Equal(t, "dps_abc", store.gotInsightsID)
}

// The caller restriction must reach the store that can enforce it. A Load that
// dropped Scope.UserID on the way to Get would read another user's session and
// return it, with nothing downstream left to catch it.
func TestLoad_CarriesTheCallerToTheScopedRead(t *testing.T) {
	store := &fakeStore{summary: &Summary{SessionID: "dps_abc"}}

	_, err := Load(context.Background(), store, Scope{SessionID: "dps_abc", UserID: "user-a"})
	require.NoError(t, err)
	assert.Equal(t, "user-a", store.gotGetScope.UserID)
	assert.Equal(t, "dps_abc", store.gotGetScope.SessionID)
}

func TestLoad_NotFound(t *testing.T) {
	_, err := Load(context.Background(), &fakeStore{}, Scope{SessionID: "dps_missing", Limit: 25})
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
			_, err := Load(context.Background(), tt.store, Scope{SessionID: "dps_abc", Limit: 25})
			assert.ErrorIs(t, err, boom)
		})
	}
}
