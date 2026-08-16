package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// carol owns the global script in portalStore. She is the caller the whole
// change is for: the owner of a shared automation who is not an administrator
// and, before #1307, could not pause her own report.
var carol = &PortalIdentity{UserID: "u4", Email: "carol@example.com", Persona: "analyst"}

// portalSchedulePath is the route under test, on the global script carol owns.
const portalSchedulePath = "/api/v1/portal/scripts/script_2/schedule"

// servePortalRequest mounts the portal routes and runs one request with a body
// against them.
func servePortalRequest(t *testing.T, deps Deps, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	New(deps).RegisterPortal(mux, func(h http.Handler) http.Handler { return h })
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// approvedPortalStore returns the portal store with the global script
// executable and its approved version declaring one required date parameter.
func approvedPortalStore() *stubStore {
	store := portalStore()
	store.scripts[1].ApprovedVersionID = "sver_1"
	store.version.Params = []script.Param{{Name: "report_date", Type: script.ParamTypeDate, Required: true}}
	return store
}

// TestPortalSetSchedule_AnOwnerRetimesTheirOwnSharedScript is the point of the
// change: the cadence of a GLOBAL script is set by the person who owns it,
// without an administrator and without going back through review.
func TestPortalSetSchedule_AnOwnerRetimesTheirOwnSharedScript(t *testing.T) {
	store := approvedPortalStore()
	rec := servePortalRequest(t, portalDeps(store, nil, nil, carol), http.MethodPut, portalSchedulePath,
		`{"cron":"0 7 * * 1-5","timezone":"America/Los_Angeles","params":{"report_date":"${fire_date}"}}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var sched script.Schedule
	decodeInto(t, rec, &sched)
	assert.Equal(t, "0 7 * * 1-5", sched.CronSpec)
	assert.Equal(t, "America/Los_Angeles", sched.Timezone)
	assert.True(t, sched.Enabled)
	assert.False(t, sched.NextRunAt.IsZero())

	require.NotNil(t, store.schedule)
	assert.Equal(t, "carol@example.com", store.schedule.CreatedBy, "the change is stamped with who made it")
	assert.Equal(t, map[string]any{"report_date": script.FireDateToken}, store.schedule.Params,
		"the token is stored unexpanded; it means the day of the fire, not today")
}

// TestPortalSetSchedule_OnAnUnapprovedScriptSavesAndStaysInert pins that a
// cadence may be prepared before review. It saves, and nothing executes it —
// the page reports the gate's own refusal rather than implying an approval it
// cannot grant.
func TestPortalSetSchedule_OnAnUnapprovedScriptSavesAndStaysInert(t *testing.T) {
	store := portalStore()
	rec := servePortalRequest(t, portalDeps(store, nil, nil, carol), http.MethodPut, portalSchedulePath,
		`{"cron":"@daily"}`)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, store.schedule)
	assert.False(t, store.scripts[1].Executable(), "nothing is approved, so nothing will fire it")
}

// TestPortalSetSchedule_RefusedForANonOwner pins that the caller who may READ a
// global script still may not re-time it, and is answered exactly as a caller
// who named a script that does not exist.
func TestPortalSetSchedule_RefusedForANonOwner(t *testing.T) {
	store := approvedPortalStore()
	notYours := servePortalRequest(t, portalDeps(store, nil, nil, stranger), http.MethodPut, portalSchedulePath,
		`{"cron":"@daily","params":{"report_date":"${fire_date}"}}`)
	require.Equal(t, http.StatusNotFound, notYours.Code)
	assert.Nil(t, store.schedule, "a refused request writes nothing")

	missing := servePortalRequest(t, portalDeps(store, nil, nil, stranger), http.MethodPut,
		"/api/v1/portal/scripts/nope/schedule", `{"cron":"@daily"}`)
	require.Equal(t, http.StatusNotFound, missing.Code)
	assert.Equal(t, missing.Body.String(), notYours.Body.String(),
		"not yours and does not exist are one answer, or the difference reports that the script exists")
}

// TestPortalSetSchedule_AdminIsUnrestricted pins that an administrator's reach
// into this surface is what it is everywhere else: they may re-time a script
// they do not own.
func TestPortalSetSchedule_AdminIsUnrestricted(t *testing.T) {
	store := approvedPortalStore()
	rec := servePortalRequest(t, portalDeps(store, nil, nil, admin), http.MethodPut, portalSchedulePath,
		`{"cron":"@daily","params":{"report_date":"${fire_date}"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, store.schedule)
	assert.Equal(t, "admin@example.com", store.schedule.CreatedBy)
}

// TestPortalSetSchedule_RefusesWhatWouldNeverFire pins that the validation is
// the tool's and the admin route's, not a second one: a cadence that cannot
// parse or a binding the contract refuses is a 400 that names what to fix.
func TestPortalSetSchedule_RefusesWhatWouldNeverFire(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"a cadence that does not parse", `{"cron":"every tuesday"}`, "not a cron expression"},
		{"an unknown zone", `{"cron":"@daily","timezone":"Mars/Olympus"}`, "not a known timezone"},
		{"a sub-minute cadence", `{"cron":"@every 10s"}`, "once a minute"},
		{"a missing required parameter", `{"cron":"@daily"}`, "required"},
		{"an unknown token", `{"cron":"@daily","params":{"report_date":"${last_month}"}}`, "unknown schedule token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := servePortalRequest(t, portalDeps(approvedPortalStore(), nil, nil, carol),
				http.MethodPut, portalSchedulePath, tt.body)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tt.wantErr)
		})
	}

	t.Run("an unreadable body", func(t *testing.T) {
		rec := servePortalRequest(t, portalDeps(approvedPortalStore(), nil, nil, carol),
			http.MethodPut, portalSchedulePath, "{not json")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	// The route is reachable by every authenticated user rather than only by
	// administrators, so the decoder is not handed whatever it was sent.
	t.Run("a body past the bound", func(t *testing.T) {
		store := approvedPortalStore()
		body := `{"cron":"@daily","params":{"report_date":"` +
			strings.Repeat("x", maxScheduleBodyBytes) + `"}}`
		rec := servePortalRequest(t, portalDeps(store, nil, nil, carol),
			http.MethodPut, portalSchedulePath, body)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Nil(t, store.schedule)
	})
}

// TestPortalScheduleEnableDisable pins the pause path from the page: the owner
// stops and resumes their own automation, and the fire it was parked on does
// not move.
func TestPortalScheduleEnableDisable(t *testing.T) {
	parked := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	store := approvedPortalStore()
	store.schedule = &script.Schedule{
		ID: "sched_1", ScriptID: "script_2", CronSpec: "@daily", Timezone: "UTC",
		Enabled: true, NextRunAt: parked, CreatedBy: "carol@example.com",
	}
	deps := portalDeps(store, nil, nil, carol)

	rec := servePortalRequest(t, deps, http.MethodPost, portalSchedulePath+"/disable", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var paused script.Schedule
	decodeInto(t, rec, &paused)
	assert.False(t, paused.Enabled)
	assert.Equal(t, "carol@example.com", store.schedule.UpdatedBy)
	assert.True(t, store.schedule.NextRunAt.Equal(parked), "pausing does not move the next fire")

	rec = servePortalRequest(t, deps, http.MethodPost, portalSchedulePath+"/enable", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var resumed script.Schedule
	decodeInto(t, rec, &resumed)
	assert.True(t, resumed.Enabled)
	assert.True(t, store.schedule.NextRunAt.Equal(parked))
}

func TestPortalScheduleEnable_Failures(t *testing.T) {
	t.Run("a script with no schedule is a 404", func(t *testing.T) {
		rec := servePortalRequest(t, portalDeps(approvedPortalStore(), nil, nil, carol),
			http.MethodPost, portalSchedulePath+"/enable", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("a script the caller does not own is a 404", func(t *testing.T) {
		store := approvedPortalStore()
		store.schedule = &script.Schedule{ID: "sched_1", ScriptID: "script_2", Enabled: true}
		rec := servePortalRequest(t, portalDeps(store, nil, nil, stranger),
			http.MethodPost, portalSchedulePath+"/disable", "")
		require.Equal(t, http.StatusNotFound, rec.Code)
		assert.True(t, store.schedule.Enabled, "a refused pause changes nothing")
	})

	t.Run("a store failure is a 500", func(t *testing.T) {
		store := approvedPortalStore()
		store.schedule = &script.Schedule{ID: "sched_1", ScriptID: "script_2"}
		store.scheduleWriteErr = errors.New("boom")
		rec := servePortalRequest(t, portalDeps(store, nil, nil, carol),
			http.MethodPost, portalSchedulePath+"/enable", "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// TestPortalListScripts_BindingsAreTheOwners pins the line between the cadence
// and its bindings: everyone entitled to see a script sees when it runs, and
// only its owner sees the values it runs WITH.
func TestPortalListScripts_BindingsAreTheOwners(t *testing.T) {
	store := approvedPortalStore()
	store.schedule = &script.Schedule{
		ID: "sched_1", ScriptID: "script_2", CronSpec: "@daily", Timezone: "UTC", Enabled: true,
		Params: map[string]any{"account_id": "acct-9"}, CreatedBy: "carol@example.com",
	}

	rec := servePortalRequest(t, portalDeps(store, nil, nil, stranger), http.MethodGet,
		"/api/v1/portal/scripts", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var seen portalScriptListResponse
	decodeInto(t, rec, &seen)
	shared := scheduleOf(t, seen, "script_2")
	assert.Equal(t, "@daily", shared.CronSpec, "the cadence is contract-level")
	assert.Nil(t, shared.Params, "the values it binds are not")
	assert.Empty(t, shared.CreatedBy)

	rec = servePortalRequest(t, portalDeps(store, nil, nil, carol), http.MethodGet,
		"/api/v1/portal/scripts", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var owned portalScriptListResponse
	decodeInto(t, rec, &owned)
	assert.Equal(t, map[string]any{"account_id": "acct-9"}, scheduleOf(t, owned, "script_2").Params)
}

// TestPortalListScripts_SourceIsTheOwners pins the other half of the same
// line: a caller entitled to know a script exists is not thereby entitled to
// read the code the platform executes for its owner.
func TestPortalListScripts_SourceIsTheOwners(t *testing.T) {
	store := approvedPortalStore()
	store.scripts[0].Source, store.scripts[1].Source = reportSource, reportSource

	rec := servePortalRequest(t, portalDeps(store, nil, nil, stranger), http.MethodGet,
		"/api/v1/portal/scripts", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var seen portalScriptListResponse
	decodeInto(t, rec, &seen)
	require.NotEmpty(t, seen.Data)
	for i := range seen.Data {
		assert.False(t, seen.Data[i].Owned)
		assert.Empty(t, seen.Data[i].Script.Source, seen.Data[i].Script.Name)
	}

	rec = servePortalRequest(t, portalDeps(store, nil, nil, admin), http.MethodGet,
		"/api/v1/portal/scripts", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var byAdmin portalScriptListResponse
	decodeInto(t, rec, &byAdmin)
	require.NotEmpty(t, byAdmin.Data)
	assert.Equal(t, reportSource, byAdmin.Data[0].Script.Source, "an administrator is unrestricted")
}

// scheduleOf finds one script's cadence in a listing.
func scheduleOf(t *testing.T, body portalScriptListResponse, scriptID string) *script.Schedule {
	t.Helper()
	for i := range body.Data {
		if body.Data[i].Script.ID == scriptID {
			require.NotNil(t, body.Data[i].Schedule)
			return body.Data[i].Schedule
		}
	}
	t.Fatalf("script %s is not in the listing", scriptID)
	return nil
}

func TestPortalGetSchedule(t *testing.T) {
	t.Run("the owner reads the bindings their editor prefills from", func(t *testing.T) {
		store := approvedPortalStore()
		store.schedule = &script.Schedule{
			ID: "sched_1", ScriptID: "script_2", CronSpec: "@daily", Timezone: "UTC",
			Enabled: true, Params: map[string]any{"report_date": script.FireDateToken},
		}
		rec := servePortalRequest(t, portalDeps(store, nil, nil, carol), http.MethodGet, portalSchedulePath, "")
		require.Equal(t, http.StatusOK, rec.Code)

		var sched script.Schedule
		decodeInto(t, rec, &sched)
		assert.Equal(t, "@daily", sched.CronSpec)
		assert.Equal(t, map[string]any{"report_date": script.FireDateToken}, sched.Params)
	})

	t.Run("a script with no schedule is a 404", func(t *testing.T) {
		rec := servePortalRequest(t, portalDeps(approvedPortalStore(), nil, nil, carol),
			http.MethodGet, portalSchedulePath, "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("a script the caller does not own is a 404", func(t *testing.T) {
		store := approvedPortalStore()
		store.schedule = &script.Schedule{ID: "sched_1", ScriptID: "script_2", CronSpec: "@daily"}
		rec := servePortalRequest(t, portalDeps(store, nil, nil, stranger),
			http.MethodGet, portalSchedulePath, "")
		require.Equal(t, http.StatusNotFound, rec.Code)
		assert.Contains(t, rec.Body.String(), errScriptNot)
	})

	t.Run("a store failure is a 500", func(t *testing.T) {
		store := approvedPortalStore()
		store.scheduleErr = errors.New("boom")
		rec := servePortalRequest(t, portalDeps(store, nil, nil, carol),
			http.MethodGet, portalSchedulePath, "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestPortalScheduleRoutesRequireAuthentication(t *testing.T) {
	deps := portalDeps(approvedPortalStore(), nil, nil, nil)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, portalSchedulePath},
		{http.MethodPut, portalSchedulePath},
		{http.MethodPost, portalSchedulePath + "/enable"},
		{http.MethodPost, portalSchedulePath + "/disable"},
	} {
		rec := servePortalRequest(t, deps, route.method, route.path, `{"cron":"@daily"}`)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, route.path)
	}
}

// TestPortalScheduleRoutesUnmountedWithoutAStore pins the degraded deployment:
// a portal that cannot keep a schedule offers no cadence controls, rather than
// offering controls that fail per request.
func TestPortalScheduleRoutesUnmountedWithoutAStore(t *testing.T) {
	deps := portalDeps(approvedPortalStore(), nil, nil, carol)
	deps.Schedules = nil
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, portalSchedulePath},
		{http.MethodPut, portalSchedulePath},
		{http.MethodPost, portalSchedulePath + "/enable"},
		{http.MethodPost, portalSchedulePath + "/disable"},
	} {
		rec := servePortalRequest(t, deps, route.method, route.path, `{"cron":"@daily"}`)
		assert.Equal(t, http.StatusNotFound, rec.Code, route.path)
	}

	// The listing is still there: reading what you own does not depend on
	// being able to schedule it.
	rec := servePortalRequest(t, deps, http.MethodGet, "/api/v1/portal/scripts", "")
	assert.Equal(t, http.StatusOK, rec.Code)
}
