package scripthandler

import (
	"context"
	"errors"
	"fmt"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// idOf is the handler a result names, failing the test when it names none.
func idOf(t *testing.T, out map[string]any) string {
	t.Helper()
	id, ok := out["handler"].(string)
	require.True(t, ok, "the result names its handler: %v", out)
	return id
}

var (
	jane  = Caller{Email: "jane@example.com"}
	bob   = Caller{Email: "bob@example.com"}
	admin = Caller{Email: "admin@example.com", Admin: true}
)

func TestNew(t *testing.T) {
	assert.Nil(t, New(nil, nil, nil), "with no database there is nowhere to keep a handler")

	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	endpoints := &epcmd.Handler{}
	scripts := scriptstore.New(db)
	s := New(db, endpoints, scripts)
	require.NotNil(t, s)
	assert.Equal(t, View{DB: db}, s.View)
	assert.Same(t, endpoints, s.Endpoints)
	assert.IsType(t, &scriptstore.Store{}, s.Schedules)
	assert.IsType(t, &scriptevents.PostgresStore{}, s.Webhooks)
	assert.NotNil(t, s.Sources, "a deployment with a database decides sources by the rule")
	assert.NotNil(t, s.SourceList)
	assert.NotNil(t, s.Now)
}

// TestSet_Refusals is what every kind's set refuses before the kind is asked.
func TestSet_Refusals(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	daily := w.add("s1", "daily", "jane@example.com", mainSource)
	other := w.add("s2", "other", "jane@example.com", mainSource)
	svc := w.service()
	theirs, err := svc.Set(ctx, SetRequest{Caller: jane, Script: other, Kind: KindSchedule, Cron: "@daily"})
	require.NoError(t, err)
	mine, err := svc.Set(ctx, SetRequest{Caller: jane, Script: daily, Kind: KindSchedule, Cron: "@daily"})
	require.NoError(t, err)

	cases := []struct {
		name string
		req  SetRequest
		is   error
		msg  string
	}{
		{name: "no kind", req: SetRequest{Caller: jane, Script: daily}, is: ErrInvalid, msg: "kind is required"},
		{name: "unknown kind", req: SetRequest{Caller: jane, Script: daily, Kind: "policy"}, is: ErrUnknownKind},
		{name: "no script", req: SetRequest{Caller: jane, Kind: KindSchedule}, is: ErrInvalid, msg: "names its script"},
		{
			name: "not the owner", req: SetRequest{Caller: bob, Script: daily, Kind: KindSchedule, Cron: "@daily"},
			is: ErrNotAllowed, msg: "the script daily is not yours",
		},
		{name: "another script's handler", req: SetRequest{
			Caller: jane, Script: daily, Kind: KindSchedule,
			Handler: idOf(t, theirs), Cron: "@hourly",
		}, is: ErrNotFound, msg: "script daily has no schedule handler"},
		{name: "a handler of another kind", req: SetRequest{
			Caller: jane, Script: daily, Kind: KindWebhook,
			Handler: idOf(t, mine), EventSource: "form-x",
		}, is: ErrNotFound, msg: "script daily has no webhook handler"},
		{name: "no such handler", req: SetRequest{Caller: jane, Script: daily, Kind: KindSchedule, Handler: "nope"}, is: ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Set(ctx, tc.req)
			require.ErrorIs(t, err, tc.is)
			assert.Contains(t, err.Error(), tc.msg)
		})
	}
	assert.Equal(t, "@daily", w.schedules.byID[idOf(t, theirs)].CronSpec, "another script's schedule is untouched")

	w.viewErr = errDown
	_, err = svc.Set(ctx, SetRequest{Caller: jane, Script: daily, Kind: KindSchedule, Handler: "sched_1"})
	require.ErrorIs(t, err, ErrStore)
	require.ErrorIs(t, err, errDown)
}

// TestEndpointHandlers is the endpoint kind through the service (#2441): a
// script holds several, each acted on by its id, an administrator's to add
// to a script that is not a form.
func TestEndpointHandlers(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	lookup := w.add("s1", "lookup", "jane@example.com", endpointSource)
	svc := w.service()
	set := func(c Caller, name string) (map[string]any, error) {
		return svc.Set(ctx, SetRequest{
			Caller: c, Script: lookup, Kind: KindEndpoint,
			Endpoint: epcmd.Input{Endpoint: name, AuthMode: epbinding.AuthAuthenticated}, TimeoutSeconds: 15,
		})
	}

	_, err := set(jane, "orders")
	require.ErrorIs(t, err, epbinding.ErrNotAllowed, "the owner of a script that is not a form does not publish it")
	orders, err := set(admin, "orders")
	require.NoError(t, err)
	archive, err := set(admin, "orders-archive")
	require.NoError(t, err)
	require.NotEqual(t, orders["handler"], archive["handler"])
	assert.Equal(t, 15, w.bindings.byID[idOf(t, orders)].TimeoutSeconds)
	held, _, err := svc.List(ctx, Filter{ScriptIDs: []string{"s1"}}, admin)
	require.NoError(t, err)
	require.Len(t, held, 2, "a script holds several endpoints")

	t.Run("changed by its id", func(t *testing.T) {
		out, err := svc.Set(ctx, SetRequest{
			Caller: admin, Script: lookup, Kind: KindEndpoint, Handler: idOf(t, orders),
			Endpoint: epcmd.Input{Endpoint: "orders-v2"},
		})
		require.NoError(t, err)
		assert.Equal(t, orders["handler"], out["handler"])
		assert.Equal(t, "orders-v2", w.bindings.byID[idOf(t, orders)].Name)
	})

	t.Run("get, disable, enable", func(t *testing.T) {
		id := idOf(t, orders)
		got, err := svc.Get(ctx, id, jane)
		require.NoError(t, err, "the owner reads their script's endpoint")
		assert.Equal(t, "orders-v2", got["endpoint"])
		assert.Equal(t, []epbinding.Failure{}, got["failures"])

		off, err := svc.Enable(ctx, id, false, admin)
		require.NoError(t, err)
		assert.Equal(t, false, off["enabled"])
		assert.True(t, w.bindings.byID[idOf(t, archive)].Enabled, "its sibling stays on")

		_, err = svc.Enable(ctx, id, true, jane)
		require.ErrorIs(t, err, epbinding.ErrNotAllowed)
		on, err := svc.Enable(ctx, id, true, admin)
		require.NoError(t, err)
		assert.Equal(t, true, on["enabled"])
	})

	t.Run("at the bound", func(t *testing.T) {
		for i := len(w.bindings.byID); i < MaxPerKind; i++ {
			id := fmt.Sprintf("ep_fill_%d", i)
			w.bindings.byID[id] = epbinding.Binding{ID: id, ScriptID: "s1", Name: id}
		}
		_, err := set(admin, "one-too-many")
		require.ErrorIs(t, err, epbinding.ErrNotAllowed)
		assert.Contains(t, err.Error(), fmt.Sprintf("already has %d endpoint handlers", MaxPerKind))
		for id, b := range w.bindings.byID {
			if b.Name != "orders-v2" && b.Name != "orders-archive" {
				delete(w.bindings.byID, id)
			}
		}
	})

	t.Run("delete", func(t *testing.T) {
		_, err := svc.Delete(ctx, idOf(t, archive), jane)
		require.ErrorIs(t, err, epbinding.ErrNotAllowed)
		out, err := svc.Delete(ctx, idOf(t, archive), admin)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"handler": archive["handler"], "kind": KindEndpoint, "name": "lookup", "deleted": true}, out)
		_, still := w.bindings.byID[idOf(t, archive)]
		assert.False(t, still)
	})

	t.Run("store failures", func(t *testing.T) {
		id := idOf(t, orders)
		w.bindings.getErr = errDown
		_, err := svc.Get(ctx, id, admin)
		require.ErrorIs(t, err, ErrStore)
		_, err = svc.Enable(ctx, id, false, admin)
		require.ErrorIs(t, err, ErrStore)
		_, err = svc.Delete(ctx, id, admin)
		require.ErrorIs(t, err, ErrStore)
		w.bindings.getErr = nil

		w.bindings.err = errDown
		_, err = svc.Delete(ctx, id, admin)
		require.ErrorIs(t, err, epcmd.ErrStore)
		assert.True(t, Failed(err))
		w.bindings.err = nil
	})
}

// TestEndpointHandlers_ADeletedBindingIsDone: the view listed a binding the
// table no longer holds (a concurrent delete); deleting it again is done.
func TestEndpointHandlers_ADeletedBindingIsDone(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	lookup := w.add("s1", "lookup", "jane@example.com", endpointSource)
	svc := w.service()
	out, err := svc.Set(ctx, SetRequest{
		Caller: admin, Script: lookup, Kind: KindEndpoint,
		Endpoint: epcmd.Input{Endpoint: "orders", AuthMode: epbinding.AuthAuthenticated},
	})
	require.NoError(t, err)
	h, err := svc.View.Get(ctx, idOf(t, out))
	require.NoError(t, err)
	delete(w.bindings.byID, h.ID)
	require.NoError(t, svc.deleteEndpoint(ctx, h, lookup, admin))
}

// TestEndpointHandlers_ADeploymentWithNoEndpoints refuses the kind plainly.
func TestEndpointHandlers_ADeploymentWithNoEndpoints(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	lookup := w.add("s1", "lookup", "jane@example.com", endpointSource)
	w.bindings.byID["ep_1"] = epbinding.Binding{ID: "ep_1", ScriptID: "s1", Name: "orders"}
	svc := w.service()
	svc.Endpoints = nil

	_, err := svc.Set(ctx, SetRequest{Caller: admin, Script: lookup, Kind: KindEndpoint, Endpoint: epcmd.Input{Endpoint: "x"}})
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "cannot bind scripts to endpoints")
	_, err = svc.Get(ctx, "ep_1", admin)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = svc.Enable(ctx, "ep_1", true, admin)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = svc.Delete(ctx, "ep_1", admin)
	require.ErrorIs(t, err, ErrInvalid)
}

// TestScheduleHandlers is the schedule kind (#2434): a script holds several
// cadences, each its own handler; a repeated cadence is refused naming the
// one it repeats; a schedule its runs name is kept.
func TestScheduleHandlers(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	daily := w.add("s1", "daily", "jane@example.com", mainSource)
	daily.Params = []script.Param{{Name: "region", Type: script.ParamTypeString}}
	svc := w.service()
	set := func(req SetRequest) (map[string]any, error) {
		req.Caller, req.Script, req.Kind = jane, daily, KindSchedule
		return svc.Set(ctx, req)
	}

	morning, err := set(SetRequest{Cron: "0 7 * * 1-5", Timezone: "America/Los_Angeles", Args: map[string]any{"region": "west"}})
	require.NoError(t, err)
	assert.Equal(t, "0 7 * * 1-5", morning["cron"])
	assert.Equal(t, "America/Los_Angeles", morning["timezone"])
	assert.Equal(t, map[string]any{"region": "west"}, morning["args"])
	assert.Equal(t, true, morning["enabled"])
	assert.Equal(t, "jane@example.com", morning["created_by"])
	assert.NotEmpty(t, morning["next_run_at"])
	assert.Contains(t, morning["message"], "latest saved version on this cadence")

	evening, err := set(SetRequest{Cron: "0 19 * * *"})
	require.NoError(t, err)
	assert.NotEqual(t, morning["handler"], evening["handler"], "a second cadence is a second handler")
	assert.Equal(t, script.DefaultTimezone, evening["timezone"])
	assert.Equal(t, map[string]any{}, evening["args"], "no args is {} and never null")

	t.Run("a repeated cadence is refused naming the one it repeats", func(t *testing.T) {
		_, err := set(SetRequest{Cron: "0 19 * * *"})
		require.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), fmt.Sprintf("already has schedule handler %s on 0 19 * * *", evening["handler"]))
		_, err = set(SetRequest{Cron: "0 19 * * *", Args: map[string]any{"region": "east"}})
		require.NoError(t, err, "the same cadence with other args is another schedule")
	})

	t.Run("changed by its id, keeping it", func(t *testing.T) {
		out, err := set(SetRequest{Handler: idOf(t, evening), Cron: "0 20 * * *"})
		require.NoError(t, err)
		assert.Equal(t, evening["handler"], out["handler"])
		assert.Equal(t, "0 20 * * *", w.schedules.byID[idOf(t, evening)].CronSpec)
	})

	t.Run("an invalid cadence", func(t *testing.T) {
		_, err := set(SetRequest{Cron: "every tuesday"})
		require.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), "not a cron expression")
	})

	t.Run("get, disable, enable", func(t *testing.T) {
		id := idOf(t, morning)
		off, err := svc.Enable(ctx, id, false, jane)
		require.NoError(t, err)
		assert.Equal(t, false, off["enabled"])
		assert.NotContains(t, off, "next_run_at", "a paused schedule is not reported as about to fire")
		assert.Equal(t, "jane@example.com", w.schedules.byID[id].UpdatedBy)
		on, err := svc.Enable(ctx, id, true, admin)
		require.NoError(t, err)
		assert.NotEmpty(t, on["next_run_at"])
		_, err = svc.Get(ctx, id, bob)
		require.ErrorIs(t, err, ErrNotAllowed)
	})

	t.Run("delete", func(t *testing.T) {
		id := idOf(t, morning)
		w.schedules.hasRuns = map[string]bool{id: true}
		_, err := svc.Delete(ctx, id, jane)
		require.ErrorIs(t, err, ErrNotAllowed)
		assert.Contains(t, err.Error(), "produced runs, which name it, so it is kept: disable it with handler_disable")
		_, kept := w.schedules.byID[id]
		assert.True(t, kept)

		out, err := svc.Delete(ctx, idOf(t, evening), jane)
		require.NoError(t, err)
		assert.Equal(t, true, out["deleted"])
	})

	t.Run("at the bound", func(t *testing.T) {
		for i := len(w.schedules.byID); i < MaxPerKind; i++ {
			id := fmt.Sprintf("sched_fill_%d", i)
			w.schedules.byID[id] = script.Schedule{ID: id, ScriptID: "s1", CronSpec: fmt.Sprintf("%d * * * *", i), Timezone: "UTC"}
		}
		_, err := set(SetRequest{Cron: "@weekly"})
		require.ErrorIs(t, err, ErrNotAllowed)
		assert.Contains(t, err.Error(), fmt.Sprintf("already has %d schedule handlers", MaxPerKind))
		_, err = set(SetRequest{Handler: idOf(t, morning), Cron: "@weekly"})
		require.NoError(t, err, "changing one it holds is not adding one")
	})

	t.Run("store failures", func(t *testing.T) {
		id := idOf(t, morning)
		w.schedules.listErr = errDown
		_, err := set(SetRequest{Cron: "@monthly"})
		require.ErrorIs(t, err, ErrStore)
		w.schedules.listErr = nil

		w.schedules.err = errDown
		_, err = set(SetRequest{Handler: id, Cron: "@monthly"})
		require.ErrorIs(t, err, ErrStore)
		_, err = svc.Enable(ctx, id, true, jane)
		require.ErrorIs(t, err, ErrStore)
		_, err = svc.Delete(ctx, id, jane)
		require.ErrorIs(t, err, ErrStore)
		w.schedules.err = nil

		w.schedules.readErr = errDown
		_, err = svc.Get(ctx, id, jane)
		require.ErrorIs(t, err, ErrStore)
		w.schedules.readErr = nil
	})
}

func TestScheduleHandlers_ADeploymentWithNoScheduleStore(t *testing.T) {
	w := newWorld()
	daily := w.add("s1", "daily", "jane@example.com", mainSource)
	svc := w.service()
	svc.Schedules = nil
	_, err := svc.Set(context.Background(), SetRequest{Caller: jane, Script: daily, Kind: KindSchedule, Cron: "@daily"})
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "cannot store schedules")
	_, err = svc.SameSchedule(context.Background(), "sched_1", "@daily", "", nil)
	require.ErrorIs(t, err, ErrInvalid)
}

// TestScheduleNote says plainly what a saved schedule will do.
func TestScheduleNote(t *testing.T) {
	runs := &script.Script{Enabled: true, Status: script.StatusActive}
	stopped := &script.Script{Enabled: false, Status: script.StatusActive}
	assert.Contains(t, scheduleNote(stopped, &script.Schedule{Enabled: true}), "nothing will execute this script: the script is disabled")
	assert.Contains(t, scheduleNote(runs, &script.Schedule{Enabled: false}), "disabled; enable it with command=handler_enable")
	assert.Contains(t, scheduleNote(runs, &script.Schedule{Enabled: true}), "as the script's own principal")
}

// TestSameSchedule is how an import matches a bundle's schedule to one the
// script already holds.
func TestSameSchedule(t *testing.T) {
	w := newWorld()
	w.add("s1", "daily", "jane@example.com", mainSource)
	w.schedules.byID["sched_1"] = script.Schedule{
		ID: "sched_1", ScriptID: "s1", CronSpec: "@daily", Timezone: script.DefaultTimezone, Params: map[string]any{"n": float64(1)},
	}
	w.schedules.byID["sched_2"] = script.Schedule{ID: "sched_2", ScriptID: "s1", CronSpec: "@hourly", Timezone: "UTC"}
	svc := w.service()
	ctx := context.Background()

	cases := []struct {
		name, id, cron, tz string
		args               map[string]any
		want               bool
	}{
		{name: "same, default timezone", id: "sched_1", cron: "@daily", args: map[string]any{"n": float64(1)}, want: true},
		{name: "other args", id: "sched_1", cron: "@daily", args: map[string]any{"n": float64(2)}},
		{name: "other cadence", id: "sched_1", cron: "@hourly", args: map[string]any{"n": float64(1)}},
		{name: "other timezone", id: "sched_2", cron: "@hourly", tz: "America/Chicago"},
		{name: "no args either way", id: "sched_2", cron: "@hourly", tz: "UTC", args: map[string]any{}, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.SameSchedule(ctx, tc.id, tc.cron, tc.tz, tc.args)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
	_, err := svc.SameSchedule(ctx, "nope", "@daily", "", nil)
	require.ErrorIs(t, err, script.ErrScheduleNotFound)
	w.schedules.readErr = errDown
	_, err = svc.SameSchedule(ctx, "sched_1", "@daily", "", nil)
	require.ErrorIs(t, err, ErrStore)
}

// TestWebhookHandlers is the webhook kind (#2434): a script runs for several
// sources, one handler each; who may name a source is the rule's; a
// handler's source does not change.
func TestWebhookHandlers(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	daily := w.add("s1", "daily", "jane@example.com", mainSource)
	svc := w.service()
	set := func(c Caller, source, handler string) (map[string]any, error) {
		return svc.Set(ctx, SetRequest{Caller: c, Script: daily, Kind: KindWebhook, EventSource: source, Handler: handler})
	}

	_, err := set(jane, "shop", "")
	require.True(t, Refused(err), "the owner names only their own form's source: %v", err)
	assert.Contains(t, err.Error(), "only an administrator runs a script for source shop")
	_, err = set(jane, "", "")
	require.True(t, Refused(err))
	assert.Contains(t, err.Error(), "event_source is required")
	_, err = set(jane, "broken", "")
	require.ErrorIs(t, err, errDown, "a source that could not be read is not a refusal")
	require.True(t, Failed(err), "it is the store's failure, so no database text reaches the caller")

	contact, err := set(jane, "form-contact", "")
	require.NoError(t, err)
	assert.Equal(t, "form-contact", contact["event_source"])
	assert.Equal(t, "Each event source form-contact stores enqueues one run of daily, reading run.event.", contact["message"])
	shop, err := set(admin, "shop", "")
	require.NoError(t, err, "an administrator names any source")
	assert.NotEqual(t, contact["handler"], shop["handler"], "one script runs for several sources")
	assert.Equal(t, []string{"shop jane@example.com false", "form-contact jane@example.com false", "shop admin@example.com true"},
		[]string{w.rule.asked[0], w.rule.asked[3], w.rule.asked[4]})

	t.Run("naming a handler on its own source changes nothing", func(t *testing.T) {
		id := idOf(t, contact)
		_, err := svc.Enable(ctx, id, false, jane)
		require.NoError(t, err)
		again, err := set(jane, "form-contact", id)
		require.NoError(t, err)
		assert.Equal(t, id, again["handler"])
		assert.Equal(t, false, again["enabled"], "a saved edit does not turn a handler back on; handler_enable does")
		assert.Equal(t, "Webhook handler "+id+" already runs daily for source form-contact; nothing changed.", again["message"])
		assert.Len(t, w.webhooks.byID, 2)
	})

	t.Run("adding a source the script holds turns its handler on", func(t *testing.T) {
		id := idOf(t, contact)
		again, err := set(jane, "form-contact", "")
		require.NoError(t, err)
		assert.Equal(t, id, again["handler"])
		assert.Equal(t, true, again["enabled"])
		assert.Equal(t, "daily already runs for source form-contact; the handler is on.", again["message"])
		assert.Len(t, w.webhooks.byID, 2)
	})

	t.Run("a handler's source does not change", func(t *testing.T) {
		_, err := set(admin, "esp", idOf(t, contact))
		require.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), "runs for form-contact, and a handler's source does not change")
	})

	t.Run("get and delete", func(t *testing.T) {
		got, err := svc.Get(ctx, idOf(t, shop), jane)
		require.NoError(t, err)
		assert.Equal(t, "shop", got["event_source"])
		assert.Equal(t, "admin@example.com", got["created_by"])
		out, err := svc.Delete(ctx, idOf(t, shop), jane)
		require.NoError(t, err)
		assert.Equal(t, KindWebhook, out["kind"])
		assert.Len(t, w.webhooks.byID, 1)
	})

	t.Run("at the bound", func(t *testing.T) {
		for i := len(w.webhooks.byID); i < MaxPerKind; i++ {
			id := fmt.Sprintf("wh_fill_%d", i)
			w.webhooks.byID[id] = scriptevents.Handler{ID: id, ScriptID: "s1", Source: id}
		}
		_, err := set(admin, "one-too-many", "")
		require.ErrorIs(t, err, ErrNotAllowed)
		assert.Contains(t, err.Error(), fmt.Sprintf("already has %d webhook handlers", MaxPerKind))
		_, err = set(jane, "form-contact", "")
		require.NoError(t, err, "a source it already runs for is not one more")
	})

	t.Run("store failures", func(t *testing.T) {
		id := idOf(t, contact)
		w.webhooks.listErr = errDown
		_, err := set(admin, "esp", "")
		require.ErrorIs(t, err, ErrStore)
		w.webhooks.listErr = nil

		w.webhooks.err = errDown
		_, err = set(jane, "form-contact", "")
		require.ErrorIs(t, err, ErrStore)
		_, err = svc.Enable(ctx, id, true, jane)
		require.ErrorIs(t, err, ErrStore)
		_, err = svc.Delete(ctx, id, jane)
		require.ErrorIs(t, err, ErrStore)
		w.webhooks.err = nil

		w.webhooks.readErr = errDown
		_, err = svc.Get(ctx, id, jane)
		require.ErrorIs(t, err, ErrStore)
	})
}

func TestWebhookHandlers_ADeploymentWithNoSources(t *testing.T) {
	w := newWorld()
	daily := w.add("s1", "daily", "jane@example.com", mainSource)
	req := SetRequest{Caller: admin, Script: daily, Kind: KindWebhook, EventSource: "shop"}

	svc := w.service()
	svc.Sources = nil
	_, err := svc.Set(context.Background(), req)
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "no webhook sources")

	svc = w.service()
	svc.Webhooks = nil
	_, err = svc.Set(context.Background(), req)
	require.ErrorIs(t, err, ErrInvalid)
}

func TestResolve(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.add("s1", "daily", "jane@example.com", mainSource)
	w.schedules.byID["sched_1"] = script.Schedule{ID: "sched_1", ScriptID: "s1", CronSpec: "@daily", Timezone: "UTC"}
	w.schedules.byID["sched_9"] = script.Schedule{ID: "sched_9", ScriptID: "gone", CronSpec: "@daily", Timezone: "UTC"}
	svc := w.service()

	h, sc, err := svc.Resolve(ctx, "sched_1", jane)
	require.NoError(t, err)
	assert.Equal(t, KindSchedule, h.Kind)
	assert.Equal(t, "daily", sc.Name)
	_, _, err = svc.Resolve(ctx, "sched_1", admin)
	require.NoError(t, err)

	_, _, err = svc.Resolve(ctx, "", jane)
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "handler is required")
	_, _, err = svc.Resolve(ctx, "nope", jane)
	require.ErrorIs(t, err, ErrNotFound)
	_, _, err = svc.Resolve(ctx, "sched_1", bob)
	require.ErrorIs(t, err, ErrNotAllowed)

	// The view joins scripts; a handler whose script went between the reads
	// is missing.
	w.scripts["gone"] = &script.Script{ID: "gone", Name: "gone", OwnerEmail: "jane@example.com"}
	v := view{w}
	row, err := v.Get(ctx, "sched_9")
	require.NoError(t, err)
	delete(w.scripts, "gone")
	svc.View = fixedView{view: v, row: row}
	_, _, err = svc.Resolve(ctx, "sched_9", admin)
	require.ErrorIs(t, err, ErrNotFound)

	svc.View = view{w}
	w.scriptErr = errDown
	_, _, err = svc.Resolve(ctx, "sched_1", jane)
	require.ErrorIs(t, err, ErrStore)
}

// fixedView answers Get with one row whatever the tables hold.
type fixedView struct {
	view
	row Handler
}

func (f fixedView) Get(context.Context, string) (Handler, error) { return f.row, nil }

// TestList scopes a listing to the caller's own scripts' handlers unless
// they are an administrator.
func TestList(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.add("s1", "daily", "jane@example.com", mainSource)
	w.add("s2", "theirs", "bob@example.com", mainSource)
	w.schedules.byID["sched_1"] = script.Schedule{ID: "sched_1", ScriptID: "s1", CronSpec: "@daily", Timezone: "UTC", Enabled: true}
	w.schedules.byID["sched_2"] = script.Schedule{ID: "sched_2", ScriptID: "s2", CronSpec: "@daily", Timezone: "UTC"}
	svc := w.service()

	mine, total, err := svc.List(ctx, Filter{}, jane)
	require.NoError(t, err)
	require.Len(t, mine, 1)
	assert.Equal(t, 1, total)
	assert.Equal(t, "sched_1", mine[0].ID)

	all, total, err := svc.List(ctx, Filter{}, admin)
	require.NoError(t, err)
	assert.Len(t, all, 2)
	assert.Equal(t, 2, total)

	theirs, _, err := svc.List(ctx, Filter{OwnerEmail: "bob@example.com"}, jane)
	require.NoError(t, err)
	assert.Equal(t, "sched_1", theirs[0].ID, "a caller's own filter cannot widen past their scripts")

	nobody, total, err := svc.List(ctx, Filter{}, Caller{})
	require.NoError(t, err)
	assert.Equal(t, []Handler{}, nobody, "a caller with no email owns nothing")
	assert.Zero(t, total)

	_, _, err = svc.List(ctx, Filter{Types: []string{"Schedule"}}, admin)
	require.ErrorIs(t, err, ErrInvalid)

	w.viewErr = errDown
	_, _, err = svc.List(ctx, Filter{}, admin)
	require.ErrorIs(t, err, ErrStore)
}

func TestTypesOf(t *testing.T) {
	w := newWorld()
	w.add("s1", "contact", "jane@example.com", formSource)
	w.bindings.byID["ep_1"] = epbinding.Binding{ID: "ep_1", ScriptID: "s1", Name: "contact"}
	w.schedules.byID["sched_1"] = script.Schedule{ID: "sched_1", ScriptID: "s1", CronSpec: "@daily", Timezone: "UTC"}
	svc := w.service()
	got, err := svc.TypesOf(context.Background(), []string{"s1", "s2"})
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"s1": {TypeForm, KindSchedule}}, got, "a form and a schedule show both")

	w.viewErr = errDown
	_, err = svc.TypesOf(context.Background(), []string{"s1"})
	require.ErrorIs(t, err, ErrStore)
}

// TestCheck is the save gate's question (#2441): a version that could not
// serve one of the script's handlers is refused, naming the handler.
func TestCheck(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	contact := w.add("s1", "contact", "jane@example.com", formSource)
	lookup := w.add("s2", "lookup", "jane@example.com", endpointSource)
	daily := w.add("s3", "daily", "jane@example.com", mainSource)
	w.bindings.byID["ep_1"] = epbinding.Binding{ID: "ep_1", ScriptID: "s1", Name: "contact"}
	w.bindings.byID["ep_2"] = epbinding.Binding{ID: "ep_2", ScriptID: "s2", Name: "lookup"}
	w.schedules.byID["sched_1"] = script.Schedule{ID: "sched_1", ScriptID: "s3", CronSpec: "@daily", Timezone: "UTC"}
	w.webhooks.byID["wh_1"] = scriptevents.Handler{ID: "wh_1", ScriptID: "s3", Source: "shop"}
	svc := w.service()

	cases := []struct {
		name   string
		sc     *script.Script
		source string
		want   []string
	}{
		{name: "a form saved as a form", sc: contact, source: formSource},
		{name: "a form saved without form_submit", sc: contact, source: endpointSource, want: []string{
			`this script is the form "contact" (endpoint handler ep_1)`, "defines no form_spec(req) and form_submit(req, values)",
			"delete the handler first",
		}},
		{name: "an endpoint saved with handle", sc: lookup, source: endpointSource},
		{name: "an endpoint saved without handle", sc: lookup, source: mainSource, want: []string{
			`this script answers the endpoint "lookup" (endpoint handler ep_2)`, "so it was not saved",
		}},
		{name: "a schedule and a webhook ask for main()", sc: daily, source: mainSource},
		{name: "a script with no handler", sc: &script.Script{ID: "s9"}, source: "x = 1\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := svc.Check(ctx, tc.sc, tc.source)
			if tc.want == nil {
				assert.Empty(t, got)
				return
			}
			for _, want := range tc.want {
				assert.Contains(t, got, want)
			}
		})
	}

	var none *Service
	assert.Empty(t, none.Check(ctx, lookup, mainSource))
	assert.Empty(t, svc.Check(ctx, nil, mainSource))
	assert.Empty(t, svc.Check(ctx, &script.Script{}, mainSource), "a new script holds no handler")

	w.viewErr = errDown
	assert.Empty(t, svc.Check(ctx, lookup, mainSource), "a failed read does not refuse the save")
}

// TestCheckOne_AKindWithAContractOtherThanAnEndpoint names the handler by
// its kind, and a kind it does not know asks nothing.
func TestCheckOne_AKindWithAContractOtherThanAnEndpoint(t *testing.T) {
	saved := kinds
	t.Cleanup(func() { kinds = saved })
	kinds = append(append([]Kind{}, saved...), Kind{Name: "watch", Check: func(string) error { return errors.New("defines no watch(rows)") }})
	assert.Equal(t, "this script holds watch handler w1: defines no watch(rows), so it was not saved",
		checkOne(Handler{ID: "w1", Kind: "watch", Type: "watch"}, mainSource))
	assert.Empty(t, checkOne(Handler{ID: "x", Kind: "retired", Type: "retired"}, mainSource))
}

// TestOffers is what adding a handler offers each caller, and why not.
func TestOffers(t *testing.T) {
	w := newWorld()
	lookup := w.add("s1", "lookup", "jane@example.com", endpointSource)
	contact := w.add("s2", "contact", "jane@example.com", formSource)
	lib := w.add("s3", "dates", "jane@example.com", "def f():\n    return 1\n")
	lib.Library = true
	svc := w.service()

	reasons := func(offers []Offer) map[string]string {
		out := map[string]string{}
		for _, o := range offers {
			assert.Equal(t, o.Allowed, o.Reason == "", o.Kind)
			k, _ := KindOf(o.Kind)
			assert.Equal(t, k.Contract, o.Contract)
			out[o.Kind] = o.Reason
		}
		return out
	}
	assert.Equal(t, map[string]string{
		KindEndpoint: "An administrator adds an endpoint to a script that is not a form.", KindSchedule: "", KindWebhook: "",
	}, reasons(svc.Offers(lookup, jane)))
	assert.Equal(t, map[string]string{KindEndpoint: "", KindSchedule: "", KindWebhook: ""}, reasons(svc.Offers(contact, jane)),
		"a form's owner adds its endpoint")
	assert.Equal(t, map[string]string{KindEndpoint: "", KindSchedule: "", KindWebhook: ""}, reasons(svc.Offers(lookup, admin)))
	for _, r := range reasons(svc.Offers(lookup, bob)) {
		assert.Equal(t, "Only the script's owner or an administrator adds its handlers.", r)
	}
	for _, r := range reasons(svc.Offers(lib, jane)) {
		assert.Equal(t, "A library is loaded by other scripts and never runs.", r)
	}

	svc.Endpoints, svc.Sources = nil, nil
	assert.Equal(t, map[string]string{
		KindEndpoint: "This deployment cannot bind scripts to endpoints.", KindSchedule: "", KindWebhook: "This deployment has no webhook sources.",
	}, reasons(svc.Offers(lookup, admin)))
}

// TestSourceNames is the webhook sources a caller may run a script for.
func TestSourceNames(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	w.add("s1", "contact", "jane@example.com", formSource)
	w.add("s2", "other", "bob@example.com", formSource)
	form := func(name, scriptID string) whsource.Source {
		return whsource.Source{Name: name, Config: whsource.Config{Input: whsource.InputForm, Form: &whsource.Form{ScriptID: scriptID}}}
	}
	w.sources.all = []whsource.Source{
		{Name: "shop"},
		form("form-contact", "s1"), form("form-other", "s2"), form("form-gone", "s9"),
		{Name: "form-broken", Config: whsource.Config{Input: whsource.InputForm}},
	}
	svc := w.service()

	got, err := svc.SourceNames(ctx, admin)
	require.NoError(t, err)
	assert.Equal(t, []string{"shop", "form-contact", "form-other", "form-gone", "form-broken"}, got)
	got, err = svc.SourceNames(ctx, jane)
	require.NoError(t, err)
	assert.Equal(t, []string{"form-contact"}, got, "anyone else only the sources of forms they own")

	w.scriptErr = errDown
	got, err = svc.SourceNames(ctx, jane)
	require.NoError(t, err)
	assert.Equal(t, []string{}, got, "a form whose script cannot be read is not offered")
	w.scriptErr = nil

	w.sources.err = errDown
	_, err = svc.SourceNames(ctx, admin)
	require.ErrorIs(t, err, ErrStore)

	svc.SourceList = nil
	got, err = svc.SourceNames(ctx, admin)
	require.NoError(t, err)
	assert.Equal(t, []string{}, got, "no sources is [] and never null")
}

// TestDescribe_AHandlerOfAKindTheTablesNoLongerHold is the read after the
// view: each kind reads its row from its own table, and a row gone between
// the two reads is missing.
func TestDescribe_AHandlerOfAKindTheTablesNoLongerHold(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	sc := w.add("s1", "daily", "jane@example.com", endpointSource)
	svc := w.service()
	for _, h := range []Handler{
		{ID: "ep_x", Kind: KindEndpoint, ScriptID: "s1"},
		{ID: "sched_x", Kind: KindSchedule, ScriptID: "s1"},
		{ID: "wh_x", Kind: KindWebhook, ScriptID: "s1"},
	} {
		_, err := svc.describe(ctx, h, sc)
		assert.True(t, Missing(err), "%s: %v", h.Kind, err)
		assert.False(t, Failed(err), h.Kind)
	}
}

// TestEnable_TurningAWebhookOnIsHeldToTheSourceRule: a webhook handler an
// administrator added for a source the owner may not name stays off once it
// is turned off. Turning one off is anyone's who may act on the script,
// turning one on is held to the rule adding one is, and an administrator is
// not asked.
func TestEnable_TurningAWebhookOnIsHeldToTheSourceRule(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	daily := w.add("s1", "daily", "jane@example.com", mainSource)
	svc := w.service()
	shop, err := svc.Set(ctx, SetRequest{Caller: admin, Script: daily, Kind: KindWebhook, EventSource: "shop"})
	require.NoError(t, err)
	form, err := svc.Set(ctx, SetRequest{Caller: jane, Script: daily, Kind: KindWebhook, EventSource: "form-daily"})
	require.NoError(t, err)
	shopID, formID := idOf(t, shop), idOf(t, form)

	off, err := svc.Enable(ctx, shopID, false, jane)
	require.NoError(t, err, "the owner turns any of their script's handlers off")
	assert.Equal(t, false, off["enabled"])

	asked := len(w.rule.asked)
	_, err = svc.Enable(ctx, shopID, true, jane)
	require.True(t, Refused(err), "%v", err)
	assert.Contains(t, err.Error(), "only an administrator runs a script for source shop")
	assert.False(t, w.webhooks.byID[shopID].Enabled, "a refused enable leaves it off")
	assert.Equal(t, "shop jane@example.com false", w.rule.asked[asked], "the rule is asked about the handler's own source")

	_, err = svc.Enable(ctx, formID, false, jane)
	require.NoError(t, err)
	on, err := svc.Enable(ctx, formID, true, jane)
	require.NoError(t, err, "the owner turns on the source of their own form")
	assert.Equal(t, true, on["enabled"])

	asked = len(w.rule.asked)
	on, err = svc.Enable(ctx, shopID, true, admin)
	require.NoError(t, err)
	assert.Equal(t, true, on["enabled"])
	assert.Len(t, w.rule.asked, asked, "an administrator is not asked")

	svc.Sources = nil
	_, err = svc.Enable(ctx, formID, false, jane)
	require.NoError(t, err, "turning one off needs no sources")
	_, err = svc.Enable(ctx, formID, true, jane)
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "no webhook sources")
	assert.False(t, w.webhooks.byID[formID].Enabled)
}

// TestSet_ALibraryHoldsNoHandler: a library is loaded by other scripts and
// never runs, so no kind of handler is added to it, whoever asks, and nothing
// is written.
func TestSet_ALibraryHoldsNoHandler(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	lib := w.add("s1", "dates", "jane@example.com", "def handle(req):\n    return 1\n")
	lib.Library = true
	svc := w.service()
	for _, req := range []SetRequest{
		{Caller: jane, Script: lib, Kind: KindSchedule, Cron: "@daily"},
		{Caller: admin, Script: lib, Kind: KindWebhook, EventSource: "shop"},
		{Caller: admin, Script: lib, Kind: KindEndpoint, Endpoint: epcmd.Input{Endpoint: "dates", AuthMode: epbinding.AuthAuthenticated}},
	} {
		_, err := svc.Set(ctx, req)
		require.ErrorIs(t, err, ErrInvalid, req.Kind)
		assert.Contains(t, err.Error(), "dates is a library, which other scripts load and which never runs, so nothing starts it", req.Kind)
	}
	assert.Empty(t, w.schedules.byID)
	assert.Empty(t, w.webhooks.byID)
	assert.Empty(t, w.bindings.byID)
	assert.Empty(t, w.rule.asked, "the source rule is not asked about a script nothing starts")

	_, err := svc.Set(ctx, SetRequest{Caller: bob, Script: lib, Kind: KindSchedule, Cron: "@daily"})
	require.ErrorIs(t, err, ErrNotAllowed, "who may act is decided before what the script is")
}
