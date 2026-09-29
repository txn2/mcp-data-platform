package notices

import (
	"context"
	"encoding/json"
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

// List applies the enabled and status predicates the store's query applies,
// so a test of which automations are briefed exercises the filter the
// briefing asks for rather than assuming it.
func (f *fakeScripts) List(_ context.Context, filter script.ListFilter) ([]script.Script, error) {
	f.gotFilter = filter
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]script.Script, 0, len(f.owned))
	for _, sc := range f.owned {
		if filter.Enabled != nil && sc.Enabled != *filter.Enabled {
			continue
		}
		if filter.Status != "" && sc.Status != filter.Status {
			continue
		}
		out = append(out, sc)
	}
	return out, nil
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
			{ID: "sc-weather", Name: "acme-dc-weather-watch", DisplayName: "DC weather", Enabled: true, Status: script.StatusActive},
			{ID: "sc-sales", Name: "daily-sales", Enabled: true, Status: script.StatusActive},
			{ID: "sc-idle", Name: "never-run", Enabled: true, Status: script.StatusActive},
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
	assert.Equal(t, script.StatusActive, scripts.gotFilter.Status)
}

// #1973: an automation its owner retired -- superseded, deprecated or
// disabled -- is not briefed again, however its last run ended; the one still
// in service is.
func TestBuildDoesNotBriefARetiredAutomation(t *testing.T) {
	failed := runstate.FailureStreak{
		Failed: 1, LastFailedRunID: "dpx_old", LastFailedVersion: 3, LastCause: "script",
		LastError: "fail: the input was not what this script expects", LastFailedAt: new(testMark.Add(time.Hour)),
	}
	scripts := &fakeScripts{
		owned: []script.Script{
			{ID: "sc-live", Name: "still-in-service", Enabled: true, Status: script.StatusActive},
			{ID: "sc-superseded", Name: "replaced", Enabled: true, Status: script.StatusSuperseded, SupersededBy: "still-in-service"},
			{ID: "sc-deprecated", Name: "retired", Enabled: true, Status: script.StatusDeprecated},
			{ID: "sc-disabled", Name: "switched-off", Enabled: false, Status: script.StatusActive},
		},
		streaks: map[string]runstate.FailureStreak{
			"sc-live": failed, "sc-superseded": failed, "sc-deprecated": failed, "sc-disabled": failed,
		},
	}
	h := testHandle(&fakeAssets{}, &fakeShares{}, &fakeThreads{}, &fakeMarks{mark: &testMark})
	h.scripts = scripts

	digest := h.Build(context.Background(), testCaller())
	require.NotNil(t, digest)
	failing, total := digest.Failing()
	require.Len(t, failing, 1)
	assert.Equal(t, "still-in-service", failing[0].Name)
	assert.Equal(t, 1, total, "a retired automation is not counted either")
}

// A caller whose only failing automation was retired is briefed on nothing.
func TestBuildBriefsNothingWhenTheOnlyFailureWasRetired(t *testing.T) {
	marks := &fakeMarks{mark: &testMark}
	h := testHandle(&fakeAssets{}, &fakeShares{}, &fakeThreads{}, marks)
	h.scripts = &fakeScripts{
		owned: []script.Script{{ID: "sc-old", Name: "replaced", Enabled: true, Status: script.StatusSuperseded}},
		streaks: map[string]runstate.FailureStreak{
			"sc-old": {Failed: 1, LastFailedRunID: "dpx_old", LastCause: "script", LastFailedAt: new(testMark.Add(time.Hour))},
		},
	}
	assert.Nil(t, h.Build(context.Background(), testCaller()))
}

// #1971: the notices block holds feedback and shares only; a digest of
// nothing but failing automations has no block, and the automations are read
// through Failing.
func TestDigestNoticesLeavesFailingAutomationsBeside(t *testing.T) {
	h := testHandle(&fakeAssets{}, &fakeShares{}, &fakeThreads{}, &fakeMarks{mark: &testMark})
	h.scripts = weatherWatch()

	digest := h.Build(context.Background(), testCaller())
	require.NotNil(t, digest)
	assert.Nil(t, digest.Notices(), "only failing automations: no notices block")
	failing, total := digest.Failing()
	assert.Len(t, failing, 1)
	assert.Equal(t, 1, total)

	raw, err := json.Marshal(&Digest{
		Since: "2026-08-10T09:00:00Z", NewShares: []ShareNotice{{Kind: "asset", ID: "a1", Reference: "mcp:asset:a1"}},
		FailingAutomations: failing, FailingAutomationsTotal: total,
	})
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "failing_automations", "the notices block never carries the list")

	var none *Digest
	assert.Nil(t, none.Notices())
	list, n := none.Failing()
	assert.Nil(t, list)
	assert.Zero(t, n)
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
		scripts.owned = append(scripts.owned, script.Script{ID: id, Name: id, Enabled: true, Status: script.StatusActive})
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
