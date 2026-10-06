package fireshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// fakeScripts answers the script listing and records the filter it was asked
// with, so the visibility scoping is asserted on the request.
type fakeScripts struct {
	scripts    []script.Script
	lastFilter script.ListFilter
	err        error
}

func (f *fakeScripts) List(_ context.Context, filter script.ListFilter) ([]script.Script, error) {
	f.lastFilter = filter
	return f.scripts, f.err
}

// fakeSchedules holds one schedule per script and answers only the ids asked
// about, which is the real store's contract.
type fakeSchedules struct {
	schedules  []script.Schedule
	lastFilter script.ScheduleFilter
	calls      int
	err        error
}

func (f *fakeSchedules) ListSchedules(_ context.Context, filter script.ScheduleFilter) ([]script.Schedule, error) {
	f.lastFilter = filter
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	allowed := make(map[string]bool, len(filter.ScriptIDs))
	for _, id := range filter.ScriptIDs {
		allowed[id] = true
	}
	out := make([]script.Schedule, 0, len(f.schedules))
	for _, s := range f.schedules {
		if allowed[s.ScriptID] {
			out = append(out, s)
		}
	}
	return out, nil
}

// caller is who a request is answered for.
type caller struct {
	owner   string
	isAdmin bool
	anon    bool
}

func twoScripts() *fakeScripts {
	return &fakeScripts{scripts: []script.Script{
		{ID: "s1", Name: "orders-ingest", DisplayName: "Orders ingest"},
		{ID: "s2", Name: "carols-report"},
	}}
}

func serve(t *testing.T, scripts *fakeScripts, schedules *fakeSchedules, who caller, query string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	New(Deps{
		Scripts: scripts, Schedules: schedules,
		Caller: func(w http.ResponseWriter, _ *http.Request) (string, bool, bool) {
			if who.anon {
				w.WriteHeader(http.StatusUnauthorized)
				return "", false, false
			}
			return who.owner, who.isAdmin, true
		},
	}).Register(mux, func(h http.Handler) http.Handler { return h })
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/scripts/fires"+query, strings.NewReader(""))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestList_LaysOutTheCallersSchedules drives the route end to end over the
// fakes: each schedule is placed on the axis its rate files it under, named by
// its script, with its fires listed.
func TestList_LaysOutTheCallersSchedules(t *testing.T) {
	schedules := &fakeSchedules{schedules: []script.Schedule{
		{ScriptID: "s1", CronSpec: "*/5 * * * *", Timezone: "UTC", Enabled: true},
		{ScriptID: "s2", CronSpec: "0 6 1 * *", Timezone: "UTC", Enabled: false},
	}}
	rec := serve(t, twoScripts(), schedules, caller{owner: "admin@example.com", isAdmin: true}, "?tz=UTC")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var tl Timeline
	decode(t, rec, &tl)
	assert.Equal(t, "UTC", tl.Timezone)
	require.Len(t, tl.Sections, 3)

	intraday := tl.Sections[0]
	assert.Equal(t, SectionIntraday, intraday.Section)
	require.Len(t, intraday.Rows, 1)
	assert.Equal(t, "Orders ingest", intraday.Rows[0].ScriptName, "a display name is what a row reads as")
	assert.Equal(t, 288, intraday.Rows[0].FireCount)
	assert.Equal(t, RhythmMinutes, intraday.Rows[0].Rhythm)

	assert.Empty(t, tl.Sections[1].Rows)
	long := tl.Sections[2]
	require.Len(t, long.Rows, 1)
	assert.Equal(t, "carols-report", long.Rows[0].ScriptName, "and the name when there is none")
	assert.False(t, long.Rows[0].Enabled, "a paused schedule is listed, marked paused")
	assert.Len(t, long.Rows[0].Fires, 3)
}

// TestList_EmptyListsAreArrays pins [] rather than null on the wire for every
// list a reader iterates.
func TestList_EmptyListsAreArrays(t *testing.T) {
	rec := serve(t, twoScripts(), &fakeSchedules{}, caller{owner: "jane@example.com"}, "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	assert.NotContains(t, body, "null")
	assert.Contains(t, body, `"rows":[]`)
	assert.Contains(t, body, `"unreadable":[]`)
}

// TestList_AnOwnerReadsOnlyTheirOwn pins the scoping: a non-admin's script
// listing is narrowed to their own, and only the listed ids reach the schedule
// read.
// TestList_EveryReaderSeesEverySchedule pins #1994: every signed-in caller
// lays out every script's cadence, and scope=mine narrows to their own.
func TestList_EveryReaderSeesEverySchedule(t *testing.T) {
	scripts := twoScripts()
	rec := serve(t, scripts, &fakeSchedules{}, caller{owner: "jane@example.com"}, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, scripts.lastFilter.OwnerEmail, "every script by default")

	scripts = twoScripts()
	rec = serve(t, scripts, &fakeSchedules{}, caller{owner: "jane@example.com"}, "?scope=mine&category=x")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "jane@example.com", scripts.lastFilter.OwnerEmail)
	assert.Empty(t, scripts.lastFilter.Category, "the layout is narrowed by scope alone")

	scripts = twoScripts()
	schedules := &fakeSchedules{}
	rec = serve(t, scripts, schedules, caller{owner: "admin@example.com", isAdmin: true}, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, scripts.lastFilter.OwnerEmail, "an administrator's listing is every script")
	assert.Equal(t, []string{"s1", "s2"}, schedules.lastFilter.ScriptIDs)
	assert.Equal(t, 2, schedules.lastFilter.Limit)
}

// TestList_NoScriptsSkipsTheScheduleRead pins that a caller with no scripts is
// answered without asking the schedule store about an empty set.
func TestList_NoScriptsSkipsTheScheduleRead(t *testing.T) {
	schedules := &fakeSchedules{}
	rec := serve(t, &fakeScripts{}, schedules, caller{owner: "jane@example.com"}, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Zero(t, schedules.calls)
}

func TestList_Failures(t *testing.T) {
	t.Run("no caller is refused before anything is read", func(t *testing.T) {
		scripts := twoScripts()
		rec := serve(t, scripts, &fakeSchedules{}, caller{anon: true}, "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Empty(t, scripts.lastFilter.OwnerEmail)
	})
	t.Run("an unknown zone is a 400", func(t *testing.T) {
		rec := serve(t, twoScripts(), &fakeSchedules{}, caller{owner: "jane@example.com"}, "?tz=Mars/Olympus")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "not a known timezone")
	})
	t.Run("a failed script listing is a 500", func(t *testing.T) {
		rec := serve(t, &fakeScripts{err: errors.New("boom")}, &fakeSchedules{}, caller{owner: "jane@example.com"}, "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
	t.Run("a failed schedule read is a 500", func(t *testing.T) {
		rec := serve(t, twoScripts(), &fakeSchedules{err: errors.New("boom")}, caller{owner: "jane@example.com"}, "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// decode reads a typed JSON response body.
func decode(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	require.NoError(t, json.NewDecoder(rec.Body).Decode(out))
}
