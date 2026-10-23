package caldav

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two calendars on one account, laid out as Radicale serves them: Work holds
// a weekly standup in Berlin time, Home a dentist appointment. The address
// book beside them is not a calendar.
var standup = strings.Join([]string{
	"BEGIN:VCALENDAR", "VERSION:2.0",
	"BEGIN:VEVENT", "UID:standup", "DTSTART;TZID=Europe/Berlin:20261005T100000",
	"DTEND;TZID=Europe/Berlin:20261005T101500", "RRULE:FREQ=WEEKLY;COUNT=4",
	"SUMMARY:Standup\u200b", "ATTENDEE;CN=Sam;PARTSTAT=ACCEPTED:mailto:sam@example.com",
	"END:VEVENT", "END:VCALENDAR",
}, "\r\n")

var dentist = strings.Join([]string{
	"BEGIN:VCALENDAR", "VERSION:2.0",
	"BEGIN:VEVENT", "UID:dentist", "DTSTART:20261007T070000Z", "DTEND:20261007T080000Z",
	"SUMMARY:Dentist", "END:VEVENT", "END:VCALENDAR",
}, "\r\n")

func esc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func fakeServer(t *testing.T) *httptest.Server {
	t.Helper()
	ms := func(w http.ResponseWriter, body string) {
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = io.WriteString(w, `<?xml version="1.0"?><multistatus xmlns="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav">`+body+`</multistatus>`)
	}
	ok := `<status>HTTP/1.1 200 OK</status>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, okAuth := r.BasicAuth(); !okAuth || u != "dana" || p != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == "PROPFIND" && strings.Contains(string(body), "current-user-principal"):
			ms(w, `<response><href>/</href><propstat><prop><current-user-principal><href>/dana/</href></current-user-principal></prop>`+ok+`</propstat></response>`)
		case r.Method == "PROPFIND" && strings.Contains(string(body), "calendar-home-set"):
			ms(w, `<response><href>/dana/</href><propstat><prop><C:calendar-home-set><href>/dana/</href></C:calendar-home-set></prop>`+ok+`</propstat></response>`)
		case r.Method == "PROPFIND":
			ms(w, `<response><href>/dana/work/</href><propstat><prop><resourcetype><collection/><C:calendar/></resourcetype><displayname>Work</displayname>`+
				`<C:supported-calendar-component-set><C:comp name="VEVENT"/></C:supported-calendar-component-set></prop>`+ok+`</propstat></response>`+
				`<response><href>/dana/home/</href><propstat><prop><resourcetype><collection/><C:calendar/></resourcetype><displayname>Home</displayname></prop>`+ok+`</propstat></response>`+
				`<response><href>/dana/tasks/</href><propstat><prop><resourcetype><collection/><C:calendar/></resourcetype><displayname>Tasks</displayname>`+
				`<C:supported-calendar-component-set><C:comp name="VTODO"/></C:supported-calendar-component-set></prop>`+ok+`</propstat></response>`)
		case r.Method == "REPORT" && r.URL.Path == "/dana/work/":
			ms(w, `<response><href>/dana/work/standup.ics</href><propstat><prop><C:calendar-data>`+esc(standup)+`</C:calendar-data></prop>`+ok+`</propstat></response>`)
		case r.Method == "REPORT" && r.URL.Path == "/dana/home/":
			ms(w, `<response><href>/dana/home/dentist.ics</href><propstat><prop><C:calendar-data>`+esc(dentist)+`</C:calendar-data></prop>`+ok+`</propstat></response>`)
		case r.Method == http.MethodGet && r.URL.Path == "/dana/work/standup.ics":
			_, _ = io.WriteString(w, standup)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func connConfig(url string) map[string]any {
	return map[string]any{"url": url, "auth_mode": "basic", "username": "dana", "password": "pw", "time_zone": "Europe/Berlin"}
}

func session(t *testing.T, tk *Toolkit) *mcp.ClientSession {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "s", Version: "v0"}, nil)
	tk.RegisterTools(server)
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (out map[string]any, text string, isErr bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	text = asText(res.Content[0]).Text
	_ = json.Unmarshal([]byte(text), &out)
	if msg, ok := out["error"].(string); ok && res.IsError {
		text = msg
	}
	return out, text, res.IsError
}

func newToolkit(t *testing.T) (*Toolkit, *mcp.ClientSession) {
	t.Helper()
	srv := fakeServer(t)
	cfg, err := ParseConfig(connConfig(srv.URL))
	require.NoError(t, err)
	tk := NewMulti(MultiConfig{DefaultName: "team", Instances: map[string]Config{"team": cfg}})
	t.Cleanup(func() { _ = tk.Close() })
	return tk, session(t, tk)
}

func TestTools_ListCalendars(t *testing.T) {
	_, cs := newToolkit(t)
	out, text, isErr := call(t, cs, ToolList, map[string]any{})
	require.False(t, isErr, text)
	assert.Equal(t, "team", out["connection"])
	cals := asList(out["calendars"])
	require.Len(t, cals, 3)
	assert.Equal(t, "Work", asMap(cals[0])["name"])
}

func TestTools_EventsAcrossCalendars(t *testing.T) {
	_, cs := newToolkit(t)
	out, text, isErr := call(t, cs, ToolEvents, map[string]any{"start": "2026-10-05", "end": "2026-10-13"})
	require.False(t, isErr, text)
	assert.Equal(t, []any{"Work", "Home"}, out["calendars"], "the task list holds no events and is not read")
	evs := asList(out["events"])
	require.Len(t, evs, 3, "two standups and the dentist, in order")
	first := asMap(evs[0])
	assert.Equal(t, "2026-10-05T08:00:00Z", first["start"])
	assert.Equal(t, "Standup", asMap(first["external"])["summary"], "hidden characters are removed")
	assert.Equal(t, "Dentist", asMap(asMap(evs[1])["external"])["summary"])
	assert.Contains(t, out["notice"], "written by a third party")
	assert.Equal(t, "2026-10-04T22:00:00Z", out["start"], "a date is midnight in the connection's zone")
}

func TestTools_EventsOnOneCalendarAndRefusals(t *testing.T) {
	_, cs := newToolkit(t)
	out, text, isErr := call(t, cs, ToolEvents, map[string]any{"calendar": "home", "start": "2026-10-01T00:00:00Z", "end": "2026-10-31T00:00:00Z"})
	require.False(t, isErr, text)
	assert.Len(t, out["events"], 1)

	_, text, isErr = call(t, cs, ToolEvents, map[string]any{"calendar": "Holidays"})
	assert.True(t, isErr)
	assert.Contains(t, text, `has no calendar "Holidays"; its calendars are Work, Home, Tasks`)

	_, text, isErr = call(t, cs, ToolEvents, map[string]any{"start": "2026-10-10", "end": "2026-10-01"})
	assert.True(t, isErr)
	assert.Contains(t, text, "must be after start")

	_, text, isErr = call(t, cs, ToolEvents, map[string]any{"start": "2020-01-01", "end": "2026-01-01"})
	assert.True(t, isErr)
	assert.Contains(t, text, "max_range_days")

	_, text, isErr = call(t, cs, ToolEvents, map[string]any{"start": "next tuesday"})
	assert.True(t, isErr)
	assert.Contains(t, text, "neither an RFC 3339 time nor a YYYY-MM-DD date")

	_, text, isErr = call(t, cs, ToolEvents, map[string]any{"start": "2026-10-01", "end": "tomorrow"})
	assert.True(t, isErr)
	assert.Contains(t, text, "end:")

	_, text, isErr = call(t, cs, ToolList, map[string]any{"connection": "nope"})
	assert.True(t, isErr)
	assert.Contains(t, text, "connection not found")
}

func TestTools_OneEvent(t *testing.T) {
	_, cs := newToolkit(t)
	out, text, isErr := call(t, cs, ToolEvent, map[string]any{"href": "/dana/work/standup.ics", "include_raw": true})
	require.False(t, isErr, text)
	evs := asList(out["events"])
	require.Len(t, evs, 1)
	ev := asMap(evs[0])
	assert.Equal(t, true, ev["recurring"])
	assert.Equal(t, "/dana/work/", ev["calendar"])
	assert.Contains(t, ev["raw"], "RRULE")

	_, text, isErr = call(t, cs, ToolEvent, map[string]any{"href": ""})
	assert.True(t, isErr)
	assert.Contains(t, text, "href is required")

	_, text, isErr = call(t, cs, ToolEvent, map[string]any{"href": "/dana/work/missing.ics"})
	assert.True(t, isErr)
	assert.Contains(t, text, "404")
}

func TestTools_ServerRefusesCredential(t *testing.T) {
	srv := fakeServer(t)
	cfg := connConfig(srv.URL)
	cfg["password"] = "wrong"
	tk := NewMulti(MultiConfig{})
	require.NoError(t, tk.AddConnection("team", cfg))
	cs := session(t, tk)
	_, text, isErr := call(t, cs, ToolList, map[string]any{})
	assert.True(t, isErr)
	assert.Contains(t, text, "401")
	_, text, isErr = call(t, cs, ToolEvents, map[string]any{})
	assert.True(t, isErr)
	assert.Contains(t, text, "reading the calendars of team")
	assert.False(t, tk.ProbeConnection(context.Background(), "team").OK)
}

func TestToolkit_ConnectionManagement(t *testing.T) {
	srv := fakeServer(t)
	tk := NewMulti(MultiConfig{DefaultName: "team", Instances: map[string]Config{"broken": {URL: "::"}}})
	assert.Equal(t, Kind, tk.Kind())
	assert.Equal(t, "team", tk.Name())
	assert.Equal(t, "team", tk.Connection())
	assert.Equal(t, []string{ToolList, ToolEvents, ToolEvent}, tk.Tools())
	tk.SetSemanticProvider(nil)
	tk.SetQueryProvider(nil)
	tk.SetConnectionStore(nil)
	assert.False(t, tk.HasConnection("broken"), "a connection that cannot be built is skipped")

	require.NoError(t, tk.AddConnection("team", connConfig(srv.URL)))
	assert.True(t, tk.HasConnection("team"))
	require.Error(t, tk.AddConnection("team", connConfig(srv.URL)))
	require.NoError(t, tk.UpdateConnection("team", connConfig(srv.URL)))
	require.NoError(t, tk.AdoptConnection("other", connConfig(srv.URL)))
	require.Error(t, tk.ValidateConnection("x", map[string]any{}))
	require.NoError(t, tk.ValidateConnection("x", connConfig(srv.URL)))

	list := tk.ListConnections()
	require.Len(t, list, 2)
	assert.True(t, *list[1].ReadOnly)
	assert.True(t, list[1].IsDefault)

	probe := tk.ProbeConnection(context.Background(), "team")
	assert.True(t, probe.OK, probe.Error)
	assert.Contains(t, probe.Detail, "3 calendar(s): Work, Home, Tasks")
	assert.False(t, tk.ProbeConnection(context.Background(), "nope").OK)

	require.NoError(t, tk.RemoveConnection("other"))
	assert.True(t, errors.Is(tk.RemoveConnection("other"), ErrConnectionNotFound))
}

func TestConn_DiscoveryIsCached(t *testing.T) {
	tk, _ := newToolkit(t)
	c, err := tk.conns.Get(context.Background(), "team")
	require.NoError(t, err)
	first, err := c.discover(context.Background(), false)
	require.NoError(t, err)
	c.http.CloseIdleConnections()
	c.mu.Lock()
	c.found = time.Now()
	c.mu.Unlock()
	again, err := c.discover(context.Background(), false)
	require.NoError(t, err)
	assert.Equal(t, first, again)
}

// asMap, asList and asText read a decoded result's parts, empty when the part
// is some other shape, which the assertion that reads it then reports.
func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func asText(v mcp.Content) *mcp.TextContent {
	t, _ := v.(*mcp.TextContent)
	if t == nil {
		return &mcp.TextContent{}
	}
	return t
}
