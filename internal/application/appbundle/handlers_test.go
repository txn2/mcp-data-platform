package appbundle

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
)

// TestHandlersOf reads a script's handlers whatever the bundle's version
// (#2441): version 2's list as it is, and version 1's one endpoint, one
// schedule and one webhook source as one handler each.
func TestHandlersOf(t *testing.T) {
	v2 := &Handlers{Endpoints: []Endpoint{{Name: "a"}, {Name: "b"}}, Schedules: []Schedule{{CronSpec: "@daily"}}, Webhooks: []Webhook{}}
	assert.Equal(t, *v2, handlersOf(&Script{Handlers: v2, Endpoint: &Endpoint{Name: "ignored"}}),
		"a version 2 script's handlers are its own, whatever version 1 fields it carries")

	v1 := &Script{Endpoint: &Endpoint{Name: "shop"}, Schedule: &Schedule{CronSpec: "0 3 * * *", Enabled: true}, EventSource: "orders-hook"}
	assert.Equal(t, Handlers{
		Endpoints: []Endpoint{{Name: "shop"}},
		Schedules: []Schedule{{CronSpec: "0 3 * * *", Enabled: true}},
		Webhooks:  []Webhook{{Source: "orders-hook", Enabled: true}},
	}, handlersOf(v1), "version 1's webhook source ran whenever it was set, so it imports on")

	assert.Equal(t, Handlers{}, handlersOf(&Script{}), "a version 1 script with none has none")
}

func TestCheckFormat(t *testing.T) {
	for _, v := range []int{FormatVersion1, FormatVersion} {
		require.NoError(t, checkFormat(&Bundle{Format: Format, FormatVersion: v}), v)
	}
	for _, b := range []*Bundle{nil, {Format: Format, FormatVersion: 3}, {Format: "other", FormatVersion: 2}} {
		err := checkFormat(b)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `this deployment reads "mcp-data-platform.application" versions 1 and 2`)
	}
}

// asVersion1 is a version 2 export written as version 1 wrote it: the
// script's first endpoint, its enabled schedule and its webhook source.
func asVersion1(t *testing.T, data string) string {
	t.Helper()
	var b Bundle
	require.NoError(t, json.Unmarshal([]byte(data), &b))
	b.FormatVersion = FormatVersion1
	s := &b.Scripts[0]
	require.NotNil(t, s.Handlers)
	s.Endpoint, s.Schedule, s.EventSource = &s.Handlers.Endpoints[0], &s.Handlers.Schedules[1], s.Handlers.Webhooks[0].Source
	s.Handlers = nil
	out, err := json.Marshal(b)
	require.NoError(t, err)
	return string(out)
}

// TestImport_AVersion1BundleImportsAsTheHandlersAVersion2ExportWritesBack is
// #2441's acceptance on the bundle: a version 1 bundle's endpoint, schedule
// and webhook source import as handlers, and an export of the deployment
// that imported it writes them back as version 2 handlers.
func TestImport_AVersion1BundleImportsAsTheHandlersAVersion2ExportWritesBack(t *testing.T) {
	v1 := asVersion1(t, exported(t))
	var sent Bundle
	require.NoError(t, json.Unmarshal([]byte(v1), &sent))
	require.Equal(t, 1, sent.FormatVersion)

	w := newWorld()
	b := bundler(w)
	b.Reader = reader{v1}
	_, err := b.Import(adminCtx(), admin, "mcp:resource:r1", fullMap)
	require.NoError(t, err)
	endpoints, schedules, webhooks := w.held("shop-api")
	require.Len(t, endpoints, 1)
	assert.Equal(t, "shop", endpoints[0].Name)
	require.Len(t, schedules, 1)
	assert.Equal(t, "0 3 * * *", schedules[0].CronSpec)
	require.Len(t, webhooks, 1)
	assert.Equal(t, "orders-hook", webhooks[0].Source)

	_, err = b.Export(context.Background(), admin, "shop", 0)
	require.NoError(t, err)
	var back Bundle
	require.NoError(t, json.Unmarshal(w.landed, &back))
	assert.Equal(t, FormatVersion, back.FormatVersion)
	require.NotNil(t, back.Scripts[0].Handlers)
	h := back.Scripts[0].Handlers
	require.Len(t, h.Endpoints, 1)
	got, want := h.Endpoints[0], *sent.Scripts[0].Endpoint
	assert.Equal(t, want.Name, got.Name)
	assert.Equal(t, want.Methods, got.Methods)
	assert.Equal(t, strings.TrimSuffix(want.Path, "/"), got.Path, "the binding stores the path template normalized")
	assert.Equal(t, want.AuthMode, got.AuthMode)
	assert.Equal(t, want.BotCheck, got.BotCheck)
	assert.Equal(t, want.ResponseMode, got.ResponseMode)
	assert.Equal(t, want.PageFrameOrigins, got.PageFrameOrigins)
	assert.Equal(t, want.IPRateLimit, got.IPRateLimit)
	assert.Equal(t, want.Enabled, got.Enabled)
	assert.Equal(t, []Schedule{*sent.Scripts[0].Schedule}, h.Schedules)
	assert.Equal(t, []Webhook{{Source: "orders-hook", Enabled: true}}, h.Webhooks)
}

// TestImport_AFormsSourceIsMadeOnTheMappedConnection: an endpoint on a form
// script asks for the form's webhook source on the connection the mapping
// names (#2178).
func TestImport_AFormsSourceIsMadeOnTheMappedConnection(t *testing.T) {
	var bundle Bundle
	require.NoError(t, json.Unmarshal([]byte(exported(t)), &bundle))
	bundle.Scripts[0].Source = "def form_spec(req):\n    return {}\n\ndef form_submit(req, values):\n    return None\n"
	data, err := json.Marshal(bundle)
	require.NoError(t, err)

	w := newWorld()
	b := bundler(w)
	b.Reader = reader{string(data)}
	_, err = b.Import(adminCtx(), admin, "mcp:resource:r1", mapWith("warehouse", "lake"))
	require.NoError(t, err)
	require.NotEmpty(t, w.formSources)
	assert.Equal(t, "shop", w.formSources[0].Endpoint)
	assert.Equal(t, "lake", w.formSources[0].Connection)
}

// TestExport_AHandlerThatCannotBeReadRefuses: each kind's read failing
// refuses the export naming what it was reading.
func TestExport_AHandlerThatCannotBeReadRefuses(t *testing.T) {
	cases := map[string]struct {
		fault func(f *handlerFaults)
		want  string
	}{
		"endpoints": {func(f *handlerFaults) { f.byScript = errDown }, "reading the script's endpoints"},
		"schedules": {func(f *handlerFaults) { f.listSchedules = errDown }, "reading the script's schedules"},
		"webhooks":  {func(f *handlerFaults) { f.handlers = errDown }, "reading the script's webhook sources"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := source(t)
			tc.fault(&w.faults)
			_, err := bundler(w).Export(context.Background(), admin, "shop", 0)
			require.ErrorIs(t, err, errDown)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestImport_AHandlerThatCannotBeSetRefuses: each kind's write failing
// refuses the import naming the handler.
func TestImport_AHandlerThatCannotBeSetRefuses(t *testing.T) {
	data := exported(t)
	var badCron Bundle
	require.NoError(t, json.Unmarshal([]byte(data), &badCron))
	badCron.Scripts[0].Handlers.Schedules[0].CronSpec = "every tuesday"
	bad, err := json.Marshal(badCron)
	require.NoError(t, err)

	cases := map[string]struct {
		bundle string
		fault  func(f *handlerFaults)
		want   string
	}{
		"a schedule read":   {bundle: data, fault: func(f *handlerFaults) { f.view = errDown }, want: "reading the schedules of script shop-api"},
		"a schedule":        {bundle: string(bad), want: "the schedule of script shop-api"},
		"a webhook source":  {bundle: data, fault: func(f *handlerFaults) { f.addHandler = errDown }, want: "running script shop-api for webhook source orders-hook"},
		"turning one off":   {bundle: data, fault: func(f *handlerFaults) { f.enable = errDown }, want: "turning handler"},
		"an endpoint taken": {bundle: data, want: "binding script shop-api to endpoint shop"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld()
			if tc.fault != nil {
				tc.fault(&w.faults)
			}
			if name == "an endpoint taken" {
				w.scripts["other"] = &scriptOther
				w.bindings["ep_x"] = bindingOf("ep_x", "other", "shop")
			}
			b := bundler(w)
			b.Reader = reader{tc.bundle}
			_, err := b.Import(adminCtx(), admin, "mcp:resource:r1", fullMap)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestImport_AScriptWithNoHandlersNeedsNoHandlerService: a bundle whose
// scripts hold none imports on a deployment that keeps no handlers.
func TestImport_AScriptWithNoHandlersNeedsNoHandlerService(t *testing.T) {
	var bundle Bundle
	require.NoError(t, json.Unmarshal([]byte(exported(t)), &bundle))
	bundle.Scripts[0].Handlers = &Handlers{Endpoints: []Endpoint{}, Schedules: []Schedule{}, Webhooks: []Webhook{}}
	data, err := json.Marshal(bundle)
	require.NoError(t, err)
	w := newWorld()
	b := bundler(w)
	b.Reader, b.Handlers = reader{string(data)}, nil
	out, err := b.Import(adminCtx(), admin, "mcp:resource:r1", fullMap)
	require.NoError(t, err)
	im, ok := out.(*Imported)
	require.True(t, ok)
	assert.Equal(t, partCreated, im.Scripts["shop-api"])
	assert.Equal(t, application.ActionImport, im.Release.Action)
}
