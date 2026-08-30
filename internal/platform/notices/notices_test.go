package notices

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/portalnoop"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

// --- fakes ---

// fakeAssets answers List from a fixed page; every other AssetStore method is
// inherited from the no-database implementation and unused here.
type fakeAssets struct {
	portaldomain.AssetStore
	owned    []portaldomain.Asset
	total    int
	err      error
	gotID    string
	gotOwner portaldomain.AssetOwner
}

func (f *fakeAssets) List(_ context.Context, filter portaldomain.AssetFilter) ([]portaldomain.Asset, int, error) {
	f.gotID = filter.Owner.UserID
	f.gotOwner = filter.Owner
	if f.err != nil {
		return nil, 0, f.err
	}
	total := f.total
	if total == 0 {
		total = len(f.owned)
	}
	return f.owned, total, nil
}

type fakeShares struct {
	portaldomain.ShareStore
	refs      []portaldomain.SharedTargetRef
	err       error
	gotSince  time.Time
	gotUserID string
	gotEmail  string
}

func (f *fakeShares) ListSharedWithUserSince(_ context.Context, userID, email string, since time.Time, _ int) ([]portaldomain.SharedTargetRef, error) {
	f.gotUserID, f.gotEmail, f.gotSince = userID, email, since
	return f.refs, f.err
}

// fakeThreads implements the thread store contract; only ListThreads is
// exercised, and it records the filter it was given so the scoping and
// self-exclusion the digest depends on are asserted rather than assumed.
type fakeThreads struct {
	found  []threads.ThreadWithMeta
	total  int
	err    error
	gotFil threads.ThreadFilter
}

func (f *fakeThreads) ListThreads(_ context.Context, filter threads.ThreadFilter) ([]threads.ThreadWithMeta, int, error) {
	f.gotFil = filter
	if f.err != nil {
		return nil, 0, f.err
	}
	total := f.total
	if total == 0 {
		total = len(f.found)
	}
	return f.found, total, nil
}

func (*fakeThreads) CreateThread(context.Context, threads.Thread, threads.ThreadEvent) (*threads.Thread, error) {
	return nil, nil //nolint:nilnil // unused by the digest
}

func (*fakeThreads) GetThread(context.Context, string) (*threads.Thread, error) {
	return nil, nil //nolint:nilnil // unused by the digest
}

func (*fakeThreads) ListEvents(context.Context, string) ([]threads.ThreadEvent, error) {
	return nil, nil
}

func (*fakeThreads) AppendEvent(context.Context, threads.ThreadEvent) (*threads.ThreadEvent, error) {
	return nil, nil //nolint:nilnil // unused by the digest
}

func (*fakeThreads) UpdateThread(context.Context, string, threads.ThreadUpdate, string, string) error {
	return nil
}
func (*fakeThreads) SoftDeleteThread(context.Context, string) error { return nil }
func (*fakeThreads) LinkInsight(context.Context, []string, string, string, string) ([]string, error) {
	return nil, nil
}
func (*fakeThreads) RequestValidation(context.Context, string, string, string) error { return nil }
func (*fakeThreads) RespondValidation(context.Context, string, threads.ValidationResponse, string, string) error {
	return nil
}

func (*fakeThreads) CountOpenByTargets(context.Context, string, []string) (map[string]int, error) {
	return map[string]int{}, nil
}
func (*fakeThreads) CountSignoffs(context.Context, string, string) (int, error) { return 0, nil }

// fakeMarks is an in-memory watermark store that records every advance.
type fakeMarks struct {
	mark    *time.Time
	nowErr  error
	getErr  error
	setErr  error
	setKey  string
	setAt   time.Time
	setCall int
}

func (f *fakeMarks) Now(context.Context) (time.Time, error) { return testNow, f.nowErr }

func (f *fakeMarks) Get(context.Context, string) (*time.Time, error) { return f.mark, f.getErr }

func (f *fakeMarks) Set(_ context.Context, key string, at time.Time) error {
	f.setCall++
	f.setKey, f.setAt = key, at
	return f.setErr
}

// --- harness ---

var (
	testNow  = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	testMark = time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
)

const (
	callerID    = "user-1"
	callerEmail = "Owner@Example.com"
)

func testHandle(a *fakeAssets, s *fakeShares, t *fakeThreads, m *fakeMarks) *Handle {
	a.AssetStore, s.ShareStore = portalnoop.NewAssetStore(), portalnoop.NewShareStore()
	return &Handle{
		assets:  a,
		shares:  s,
		threads: t,
		marks:   m,
	}
}

func testCaller() *middleware.PlatformContext {
	return &middleware.PlatformContext{UserID: callerID, UserEmail: callerEmail, AuthType: "oidc"}
}

func ownedAsset() []portaldomain.Asset {
	return []portaldomain.Asset{{ID: "asset-1", Name: "Q3 revenue"}}
}

func openThread() []threads.ThreadWithMeta {
	return []threads.ThreadWithMeta{{
		Thread: threads.Thread{
			ID: "thr-1", Kind: threads.ThreadKindCorrection, Status: threads.ThreadStatusOpen,
			Title: "wrong currency", AuthorEmail: "sme@example.com", AssetID: "asset-1",
		},
		LastEventAt: testMark.Add(time.Hour),
	}}
}

func newShare() []portaldomain.SharedTargetRef {
	return []portaldomain.SharedTargetRef{{
		ShareID: "shr-1", TargetType: portaldomain.TargetTypeCollection, TargetID: "col-1",
		TargetName: "Board pack", SharedBy: "lead@example.com",
		SharedAt: testMark.Add(2 * time.Hour), Permission: portaldomain.PermissionViewer,
	}}
}

// --- tests ---

func TestNewReturnsNilWhenAnyInputIsMissing(t *testing.T) {
	assert.Nil(t, New(nil, &fakeAssets{}, &fakeShares{}, &fakeThreads{}))
	// A nil Handle answers empty rather than panicking, which is what a
	// deployment without a portal holds.
	var h *Handle
	assert.Nil(t, h.Build(context.Background(), testCaller()))
}

func TestBuildRefusesCallersThereIsNoOneToBrief(t *testing.T) {
	tests := []struct {
		name string
		pc   *middleware.PlatformContext
	}{
		{name: "no platform context", pc: nil},
		{name: "anonymous identity", pc: &middleware.PlatformContext{
			UserID: "anonymous", UserEmail: "a@b.c", AuthType: middleware.AuthTypeAnonymous,
		}},
		{name: "neither id nor email", pc: &middleware.PlatformContext{AuthType: "apikey"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marks := &fakeMarks{}
			h := testHandle(&fakeAssets{owned: ownedAsset()}, &fakeShares{refs: newShare()},
				&fakeThreads{found: openThread()}, marks)
			assert.Nil(t, h.Build(context.Background(), tt.pc))
			assert.Zero(t, marks.setCall, "an unbriefable caller must not move a watermark")
		})
	}
}

func TestBuildReportsFeedbackAndSharesSinceTheWatermark(t *testing.T) {
	assets := &fakeAssets{owned: ownedAsset()}
	shares := &fakeShares{refs: newShare()}
	thr := &fakeThreads{found: openThread(), total: 4}
	marks := &fakeMarks{mark: &testMark}

	digest := testHandle(assets, shares, thr, marks).Build(context.Background(), testCaller())

	require.NotNil(t, digest)
	assert.Equal(t, "2026-08-10T09:00:00Z", digest.Since)

	require.Len(t, digest.Feedback, 1)
	assert.Equal(t, FeedbackNotice{
		ThreadID: "thr-1", Kind: threads.ThreadKindCorrection, Status: threads.ThreadStatusOpen,
		Title: "wrong currency", AuthorEmail: "sme@example.com",
		AssetID: "asset-1", AssetName: "Q3 revenue", AssetReference: "mcp:asset:asset-1",
		LastActivityAt: "2026-08-10T10:00:00Z",
	}, digest.Feedback[0])
	// The cap truncated the list, so the count must still report the whole set.
	assert.Equal(t, 4, digest.FeedbackTotal)

	require.Len(t, digest.NewShares, 1)
	assert.Equal(t, ShareNotice{
		Kind: portaldomain.TargetTypeCollection, ID: "col-1", Name: "Board pack",
		Reference: "mcp:collection:col-1", SharedBy: "lead@example.com",
		SharedAt: "2026-08-10T11:00:00Z", Permission: string(portaldomain.PermissionViewer),
	}, digest.NewShares[0])

	feedbackCount, shareCount := digest.Counts()
	assert.Equal(t, 4, feedbackCount)
	assert.Equal(t, 1, shareCount)

	// Both queries were scoped to the caller and to the watermark.
	assert.Equal(t, callerID, assets.gotID)
	assert.Equal(t, []string{"asset-1"}, thr.gotFil.TargetAssetIDs)
	assert.True(t, thr.gotFil.Unresolved)
	require.NotNil(t, thr.gotFil.ActivityAfter)
	assert.Equal(t, testMark, *thr.gotFil.ActivityAfter)
	assert.Equal(t, callerID, thr.gotFil.ExcludeAuthorID)
	assert.Equal(t, callerEmail, thr.gotFil.ExcludeAuthorEmail)
	assert.Equal(t, testMark, shares.gotSince)
	assert.Equal(t, callerID, shares.gotUserID)
	assert.Equal(t, callerEmail, shares.gotEmail)

	// Delivery advanced the watermark to the instant the build started, keyed
	// by the caller's lowercased email.
	assert.Equal(t, 1, marks.setCall)
	assert.Equal(t, "owner@example.com", marks.setKey)
	assert.Equal(t, testNow, marks.setAt)
}

func TestBuildFirstEverDigestReportsABoundedWindow(t *testing.T) {
	shares := &fakeShares{refs: newShare()}
	thr := &fakeThreads{found: openThread()}
	h := testHandle(&fakeAssets{owned: ownedAsset()}, shares, thr, &fakeMarks{})

	digest := h.Build(context.Background(), testCaller())

	require.NotNil(t, digest)
	want := testNow.Add(-firstRunLookback)
	assert.Equal(t, want, shares.gotSince, "a caller never briefed gets the lookback window, not all history")
	require.NotNil(t, thr.gotFil.ActivityAfter)
	assert.Equal(t, want, *thr.gotFil.ActivityAfter)
	assert.Equal(t, "2026-07-18T12:00:00Z", digest.Since)
}

func TestBuildKeyedByUserIDWhenTheCallerHasNoEmail(t *testing.T) {
	marks := &fakeMarks{mark: &testMark}
	h := testHandle(&fakeAssets{owned: ownedAsset()}, &fakeShares{}, &fakeThreads{found: openThread()}, marks)

	digest := h.Build(context.Background(), &middleware.PlatformContext{UserID: callerID, AuthType: "apikey"})

	require.NotNil(t, digest)
	assert.Equal(t, callerID, marks.setKey)
}

func TestBuildNothingToSayReturnsNilAndLeavesTheWatermark(t *testing.T) {
	marks := &fakeMarks{mark: &testMark}
	h := testHandle(&fakeAssets{owned: ownedAsset()}, &fakeShares{}, &fakeThreads{}, marks)

	assert.Nil(t, h.Build(context.Background(), testCaller()))
	assert.Zero(t, marks.setCall, "an empty digest was never delivered, so nothing was seen")
}

func TestBuildUnidentifiedCallerHasNoOwnedAssetsToCarryFeedback(t *testing.T) {
	assets := &fakeAssets{owned: ownedAsset()}
	thr := &fakeThreads{found: openThread()}
	h := testHandle(assets, &fakeShares{refs: newShare()}, thr, &fakeMarks{mark: &testMark})

	digest := h.Build(context.Background(), &middleware.PlatformContext{AuthType: "oidc"})

	assert.Nil(t, digest, "a caller with neither identifier owns nothing to be told about")
	assert.False(t, assets.gotOwner.Identified(), "an unscoped asset listing is every owner's")
	assert.Empty(t, thr.gotFil.TargetAssetIDs, "the thread query must never run unscoped")
}

// A caller carrying only an address still owns what is recorded under it: that
// is how an asset a managed script wrote reaches the person who owns the script
// (#1551), and feedback on it has to reach the same person.
func TestBuildAnAddressOnlyCallerOwnsWhatIsRecordedUnderIt(t *testing.T) {
	assets := &fakeAssets{owned: ownedAsset()}
	thr := &fakeThreads{found: openThread()}
	h := testHandle(assets, &fakeShares{refs: newShare()}, thr, &fakeMarks{mark: &testMark})

	digest := h.Build(context.Background(), &middleware.PlatformContext{UserEmail: callerEmail, AuthType: "oidc"})

	require.NotNil(t, digest)
	assert.Equal(t, callerEmail, assets.gotOwner.Email)
	assert.Len(t, digest.Feedback, 1)
	assert.Equal(t, []string{"asset-1"}, thr.gotFil.TargetAssetIDs)
	assert.Len(t, digest.NewShares, 1, "shares still resolve by email")
}

func TestBuildAFailedHalfIsReportedButTheWatermarkHolds(t *testing.T) {
	tests := []struct {
		name         string
		assets       *fakeAssets
		shares       *fakeShares
		thr          *fakeThreads
		wantFeedback int
		wantShares   int
	}{
		{
			name:   "share query failed",
			assets: &fakeAssets{owned: ownedAsset()},
			shares: &fakeShares{err: errors.New("db down")},
			thr:    &fakeThreads{found: openThread()}, wantFeedback: 1,
		},
		{
			name:   "thread query failed",
			assets: &fakeAssets{owned: ownedAsset()},
			shares: &fakeShares{refs: newShare()},
			thr:    &fakeThreads{err: errors.New("db down")}, wantShares: 1,
		},
		{
			name:   "owned-asset lookup failed",
			assets: &fakeAssets{err: errors.New("db down")},
			shares: &fakeShares{refs: newShare()},
			thr:    &fakeThreads{}, wantShares: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marks := &fakeMarks{mark: &testMark}
			digest := testHandle(tt.assets, tt.shares, tt.thr, marks).Build(context.Background(), testCaller())

			require.NotNil(t, digest)
			assert.Len(t, digest.Feedback, tt.wantFeedback)
			assert.Len(t, digest.NewShares, tt.wantShares)
			assert.Zero(t, marks.setCall,
				"a half that failed to load must be reported next session, not silently dropped")
		})
	}
}

// Without a clock the digest has no boundary, so it reports nothing rather
// than guessing one and reading out notices the caller has already seen.
func TestBuildUnreadableClockReportsNothingRatherThanEverything(t *testing.T) {
	marks := &fakeMarks{mark: &testMark, nowErr: errors.New("db down")}
	h := testHandle(&fakeAssets{owned: ownedAsset()}, &fakeShares{refs: newShare()},
		&fakeThreads{found: openThread()}, marks)

	assert.Nil(t, h.Build(context.Background(), testCaller()))
	assert.Zero(t, marks.setCall, "a digest that was never built must not advance the watermark")
}

func TestBuildUnreadableWatermarkReportsNothingRatherThanEverything(t *testing.T) {
	h := testHandle(&fakeAssets{owned: ownedAsset()}, &fakeShares{refs: newShare()},
		&fakeThreads{found: openThread()}, &fakeMarks{getErr: errors.New("db down")})

	assert.Nil(t, h.Build(context.Background(), testCaller()))
}

func TestBuildSurvivesAWatermarkThatCannotBeAdvanced(t *testing.T) {
	marks := &fakeMarks{mark: &testMark, setErr: errors.New("db down")}
	h := testHandle(&fakeAssets{owned: ownedAsset()}, &fakeShares{}, &fakeThreads{found: openThread()}, marks)

	assert.NotNil(t, h.Build(context.Background(), testCaller()), "orientation must not fail over a notice")
}

func TestOwnedAssetTruncationIsBounded(t *testing.T) {
	assets := &fakeAssets{owned: ownedAsset(), total: maxOwnedAssets + 50}
	h := testHandle(assets, &fakeShares{}, &fakeThreads{found: openThread()}, &fakeMarks{mark: &testMark})

	digest := h.Build(context.Background(), testCaller())

	require.NotNil(t, digest)
	assert.Len(t, digest.Feedback, 1)
}

// The share query is bounded, not counted, so a full page has to be reported as
// a floor rather than as the whole set.
func TestBuildMarksAShareListThatDidNotFitAsTruncated(t *testing.T) {
	overflow := make([]portaldomain.SharedTargetRef, 0, maxShareNotices+1)
	for i := 0; i <= maxShareNotices; i++ {
		overflow = append(overflow, newShare()[0])
	}
	shares := &fakeShares{refs: overflow}
	h := testHandle(&fakeAssets{}, shares, &fakeThreads{}, &fakeMarks{mark: &testMark})

	digest := h.Build(context.Background(), testCaller())

	require.NotNil(t, digest)
	assert.Len(t, digest.NewShares, maxShareNotices, "the list is capped")
	assert.True(t, digest.NewSharesTruncated, "more arrived than the digest names")

	// A list that exactly fits is complete, not truncated.
	shares.refs = overflow[:maxShareNotices]
	digest = h.Build(context.Background(), testCaller())
	require.NotNil(t, digest)
	assert.Len(t, digest.NewShares, maxShareNotices)
	assert.False(t, digest.NewSharesTruncated)
}

func TestCountsOfANilDigest(t *testing.T) {
	var d *Digest
	feedback, shares := d.Counts()
	assert.Zero(t, feedback)
	assert.Zero(t, shares)
}

// The fakes must satisfy the real contracts, so a store interface that grows a
// method fails here rather than silently narrowing what the digest is tested
// against.
var (
	_ portaldomain.AssetStore = (*fakeAssets)(nil)
	_ portaldomain.ShareStore = (*fakeShares)(nil)
	_ threads.ThreadStore     = (*fakeThreads)(nil)
	_ WatermarkStore          = (*fakeMarks)(nil)
)
