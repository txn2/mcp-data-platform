package notices

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// fakeScripts is the caller's automations, their schedules and their streaks.
type fakeScripts struct {
	owned     []script.Script
	schedules []script.Schedule
	streaks   map[string]runstate.FailureStreak
	listErr   error
	schedErr  error
	streakErr error
	gotFilter script.ListFilter
}

func (f *fakeScripts) List(_ context.Context, filter script.ListFilter) ([]script.Script, error) {
	f.gotFilter = filter
	return f.owned, f.listErr
}

func (f *fakeScripts) ListSchedules(context.Context, script.ScheduleFilter) ([]script.Schedule, error) {
	return f.schedules, f.schedErr
}

func (f *fakeScripts) FailureStreaks(context.Context, []string) (map[string]runstate.FailureStreak, error) {
	return f.streaks, f.streakErr
}

var _ AutomationSource = (*fakeScripts)(nil)

// weatherWatch is the ticket's automation: scheduled, failing its latest run
// on an upstream 500, after an earlier success.
func weatherWatch() *fakeScripts {
	return &fakeScripts{
		owned: []script.Script{
			{ID: "sc-weather", Name: "acme-dc-weather-watch", DisplayName: "DC weather"},
			{ID: "sc-sales", Name: "daily-sales"},
			{ID: "sc-idle", Name: "never-run"},
		},
		schedules: []script.Schedule{{ScriptID: "sc-weather", Enabled: true}},
		streaks: map[string]runstate.FailureStreak{
			"sc-weather": {
				Failed: 2, SameError: 2, LastError: "Error in fail: fail: NWS returned 500 for Phoenix",
				LastFailedRunID: "dpx_0de8", LastFailedVersion: 6, LastCause: "upstream",
				LastFailedAt: new(testMark.Add(3 * time.Hour)), LastSuccessAt: new(testMark.Add(-time.Hour)),
			},
			"sc-sales": {Failed: 0, LastSuccessAt: new(testMark)},
		},
	}
}

// #1934: an owner whose automation failed its latest run is told which, why,
// how often, and what to open; a script whose latest run succeeded, and one
// that never ran, are not named.
func TestBuildNamesFailingAutomations(t *testing.T) {
	scripts := weatherWatch()
	h := testHandle(&fakeAssets{}, &fakeShares{}, &fakeThreads{}, &fakeMarks{mark: &testMark})
	h.scripts = scripts

	digest := h.Build(context.Background(), testCaller())
	require.NotNil(t, digest)
	require.Len(t, digest.FailingAutomations, 1)
	assert.Equal(t, 1, digest.FailingAutomationsTotal)
	assert.Equal(t, AutomationNotice{
		Name: "acme-dc-weather-watch", DisplayName: "DC weather", Reference: "mcp:script:sc-weather",
		Version: 6, RunID: "dpx_0de8", Cause: "upstream", Retryable: true,
		Error: "Error in fail: fail: NWS returned 500 for Phoenix", ConsecutiveFailures: 2,
		FailedAt: "2026-08-10T12:00:00Z", LastSucceededAt: "2026-08-10T08:00:00Z",
		Scheduled: true, New: true,
	}, digest.FailingAutomations[0])
	assert.Equal(t, callerEmail, scripts.gotFilter.OwnerEmail, "the caller's own automations, as their email is stored")
	require.NotNil(t, scripts.gotFilter.Enabled)
	assert.True(t, *scripts.gotFilter.Enabled)
}

// A failure already briefed is still listed, because the automation is still
// failing, but it is not new.
func TestBuildKeepsListingAStillFailingAutomation(t *testing.T) {
	later := testMark.Add(5 * time.Hour)
	h := testHandle(&fakeAssets{}, &fakeShares{}, &fakeThreads{}, &fakeMarks{mark: &later})
	h.scripts = weatherWatch()

	digest := h.Build(context.Background(), testCaller())
	require.NotNil(t, digest)
	require.Len(t, digest.FailingAutomations, 1)
	assert.False(t, digest.FailingAutomations[0].New)
}

// The list is capped, newest failure first, and counted.
func TestBuildCapsFailingAutomations(t *testing.T) {
	scripts := &fakeScripts{streaks: map[string]runstate.FailureStreak{}}
	for i := range maxAutomationNotices + 3 {
		id := fmt.Sprintf("sc-%02d", i)
		scripts.owned = append(scripts.owned, script.Script{ID: id, Name: id})
		scripts.streaks[id] = runstate.FailureStreak{Failed: 1, LastFailedAt: new(testMark.Add(time.Duration(i) * time.Minute))}
	}
	h := testHandle(&fakeAssets{}, &fakeShares{}, &fakeThreads{}, &fakeMarks{mark: &testMark})
	h.scripts = scripts

	digest := h.Build(context.Background(), testCaller())
	require.NotNil(t, digest)
	assert.Len(t, digest.FailingAutomations, maxAutomationNotices)
	assert.Equal(t, maxAutomationNotices+3, digest.FailingAutomationsTotal)
	assert.Equal(t, "sc-12", digest.FailingAutomations[0].Name, "newest failure first")
	_, _, automations := digest.Counts()
	assert.Equal(t, maxAutomationNotices+3, automations)
}

// A read that fails leaves the automations out and holds the watermark, as a
// failed feedback read does.
func TestBuildAFailedAutomationReadHoldsTheWatermark(t *testing.T) {
	for name, scripts := range map[string]*fakeScripts{
		"list":      {listErr: errors.New("down")},
		"streaks":   {owned: weatherWatch().owned, streakErr: errors.New("down")},
		"schedules": {owned: weatherWatch().owned, streaks: weatherWatch().streaks, schedErr: errors.New("down")},
	} {
		t.Run(name, func(t *testing.T) {
			marks := &fakeMarks{mark: &testMark}
			h := testHandle(&fakeAssets{}, &fakeShares{refs: newShare()}, &fakeThreads{}, marks)
			h.scripts = scripts
			digest := h.Build(context.Background(), testCaller())
			require.NotNil(t, digest, "the shares half still has something to say")
			assert.Empty(t, digest.FailingAutomations)
			assert.Zero(t, marks.setCall, "the watermark holds until every half was read")
		})
	}
}

// A caller with no email owns no automation, since ownership is by email.
func TestBuildBriefsNoAutomationsWithoutAnEmail(t *testing.T) {
	h := testHandle(&fakeAssets{}, &fakeShares{}, &fakeThreads{}, &fakeMarks{mark: &testMark})
	h.scripts = weatherWatch()
	pc := testCaller()
	pc.UserEmail = ""
	assert.Nil(t, h.Build(context.Background(), pc))
}

// A caller who owns no automation is briefed on none, and the read is
// complete.
func TestBuildACallerWithNoAutomationsIsBriefedOnNone(t *testing.T) {
	marks := &fakeMarks{mark: &testMark}
	h := testHandle(&fakeAssets{}, &fakeShares{refs: newShare()}, &fakeThreads{}, marks)
	h.scripts = &fakeScripts{}
	digest := h.Build(context.Background(), testCaller())
	require.NotNil(t, digest)
	assert.Empty(t, digest.FailingAutomations)
	assert.Equal(t, 1, marks.setCall, "nothing failed to load, so the watermark advances")
}
