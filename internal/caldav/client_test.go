package caldav

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeServer answers the requests a CalDAV account is read with, the way
// Radicale lays one out: a principal at /dana/, its home the same collection,
// one calendar and one address book under it.
type fakeServer struct {
	t        *testing.T
	requests []string
	bodies   map[string]string
	depths   map[string]string
	// principalAt is the path that answers current-user-principal.
	principalAt string
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	key := r.Method + " " + r.URL.Path
	f.requests = append(f.requests, key)
	f.bodies[key] = string(body)
	f.depths[key] = r.Header.Get("Depth")
	if u, p, ok := r.BasicAuth(); !ok || u != "dana" || p != "s3cret" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	ms := func(s string) {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = io.WriteString(w, `<?xml version="1.0"?><multistatus xmlns="DAV:" xmlns:C="urn:ietf:params:xml:ns:caldav" xmlns:CS="http://calendarserver.org/ns/" xmlns:ICAL="http://apple.com/ns/ical/">`+s+`</multistatus>`)
	}
	switch {
	case r.Method == "PROPFIND" && strings.Contains(string(body), "current-user-principal"):
		if r.URL.Path != f.principalAt {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		ms(`<response><href>` + r.URL.Path + `</href><propstat><prop><current-user-principal><href>/dana/</href></current-user-principal></prop><status>HTTP/1.1 200 OK</status></propstat></response>`)
	case r.Method == "PROPFIND" && strings.Contains(string(body), "calendar-home-set"):
		ms(`<response><href>/dana/</href><propstat><prop><C:calendar-home-set><href>/dana/</href></C:calendar-home-set></prop><status>HTTP/1.1 200 OK</status></propstat></response>`)
	case r.Method == "PROPFIND" && r.URL.Path == "/dana/":
		ms(`<response><href>/dana/</href><propstat><prop><resourcetype><collection/></resourcetype></prop><status>HTTP/1.1 200 OK</status></propstat></response>` +
			`<response><href>/dana/work/</href><propstat><prop><resourcetype><collection/><C:calendar/></resourcetype><displayname>Work</displayname>` +
			`<C:supported-calendar-component-set><C:comp name="VEVENT"/></C:supported-calendar-component-set><CS:getctag>ctag-7</CS:getctag><ICAL:calendar-color>#2563EB</ICAL:calendar-color></prop><status>HTTP/1.1 200 OK</status></propstat>` +
			`<propstat><prop><C:calendar-description/></prop><status>HTTP/1.1 404 Not Found</status></propstat></response>` +
			`<response><href>/dana/contacts%20book/</href><propstat><prop><resourcetype><collection/><C:calendar/></resourcetype></prop><status>HTTP/1.1 200 OK</status></propstat></response>` +
			`<response><href>/dana/addresses/</href><propstat><prop><resourcetype><collection/></resourcetype><displayname>Addresses</displayname></prop><status>HTTP/1.1 200 OK</status></propstat></response>`)
	case r.Method == "REPORT" && r.URL.Path == "/dana/work/":
		ms(`<response><href>/dana/work/standup.ics</href><propstat><prop><getetag>"e1"</getetag><C:calendar-data>` +
			xmlEscape(weeklyStandup) + `</C:calendar-data></prop><status>HTTP/1.1 200 OK</status></propstat></response>` +
			`<response><href>/dana/work/gone.ics</href><status>HTTP/1.1 404 Not Found</status></response>`)
	case r.Method == http.MethodGet && r.URL.Path == "/dana/work/standup.ics":
		_, _ = io.WriteString(w, weeklyStandup)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func newFake(t *testing.T, principalAt string) (*fakeServer, *httptest.Server) {
	t.Helper()
	f := &fakeServer{t: t, bodies: map[string]string{}, depths: map[string]string{}, principalAt: principalAt}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func basic(req *http.Request) error {
	req.SetBasicAuth("dana", "s3cret")
	return nil
}

func TestClient_DiscoversHomeAndListsCalendars(t *testing.T) {
	f, srv := newFake(t, "/")
	c, err := New(srv.URL, srv.Client(), basic)
	require.NoError(t, err)
	ctx := context.Background()

	home, err := c.HomeSet(ctx)
	require.NoError(t, err)
	assert.Equal(t, "/dana/", home)

	cals, err := c.Calendars(ctx, home)
	require.NoError(t, err)
	require.Len(t, cals, 2, "the address book is not a calendar")
	assert.Equal(t, Calendar{Href: "/dana/work/", DisplayName: "Work", Color: "#2563EB", Components: []string{"VEVENT"}, CTag: "ctag-7"}, cals[0])
	assert.Equal(t, "contacts book", cals[1].DisplayName, "an unnamed calendar is named by its path")
	assert.Equal(t, []string{"VEVENT", "VTODO", "VJOURNAL"}, cals[1].Components)
	assert.Equal(t, "1", f.depths["PROPFIND /dana/"])
}

func TestClient_WellKnownWhenTheRootSaysNothing(t *testing.T) {
	f, srv := newFake(t, "/.well-known/caldav")
	c, err := New(srv.URL, srv.Client(), basic)
	require.NoError(t, err)
	home, err := c.HomeSet(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "/dana/", home)
	assert.Contains(t, f.requests, "PROPFIND /.well-known/caldav")
}

func TestClient_QuerySendsATimeRangeReport(t *testing.T) {
	f, srv := newFake(t, "/")
	c, err := New(srv.URL+"/dana", srv.Client(), basic)
	require.NoError(t, err)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	objs, err := c.Query(context.Background(), "/dana/work/", start, start.AddDate(0, 1, 0))
	require.NoError(t, err)
	require.Len(t, objs, 1, "a response with no calendar data is not an object")
	assert.Equal(t, "/dana/work/standup.ics", objs[0].Href)
	assert.Equal(t, `"e1"`, objs[0].ETag)
	assert.Contains(t, objs[0].Data, "RRULE:FREQ=WEEKLY", "calendar data is kept line for line")
	body := f.bodies["REPORT /dana/work/"]
	assert.Contains(t, body, `<C:time-range start="20261001T000000Z" end="20261101T000000Z"/>`)
	assert.Contains(t, body, `<C:comp-filter name="VEVENT">`)
	assert.Equal(t, "1", f.depths["REPORT /dana/work/"])
}

func TestClient_GetReadsOneObject(t *testing.T) {
	_, srv := newFake(t, "/")
	c, err := New(srv.URL, srv.Client(), basic)
	require.NoError(t, err)
	o, err := c.Get(context.Background(), "/dana/work/standup.ics")
	require.NoError(t, err)
	assert.Equal(t, weeklyStandup, o.Data)
}

func TestClient_Refusals(t *testing.T) {
	_, srv := newFake(t, "/")
	ctx := context.Background()

	wrong, err := New(srv.URL, srv.Client(), func(r *http.Request) error { r.SetBasicAuth("dana", "nope"); return nil })
	require.NoError(t, err)
	_, err = wrong.Calendars(ctx, "/dana/")
	var se *StatusError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, http.StatusUnauthorized, se.Status)
	assert.Contains(t, se.Error(), "401 Unauthorized")

	c, err := New(srv.URL, srv.Client(), basic)
	require.NoError(t, err)
	_, err = c.Get(ctx, "https://elsewhere.example.com/dana/work/x.ics")
	assert.ErrorContains(t, err, "not on the account's server", "credentials never go to another host")

	_, err = New("ftp://example.com", srv.Client(), nil)
	assert.Error(t, err)

	authFail, err := New(srv.URL, srv.Client(), func(*http.Request) error { return errors.New("no token") })
	require.NoError(t, err)
	_, err = authFail.Get(ctx, "/dana/work/standup.ics")
	assert.ErrorContains(t, err, "authorizing request")
}

func TestClient_RefusesAnOversizedAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, maxResponseBytes+1))
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, srv.Client(), nil)
	require.NoError(t, err)
	_, err = c.Get(context.Background(), "/big.ics")
	assert.ErrorIs(t, err, ErrTooLarge)
}

func TestParseMultistatus_Refusals(t *testing.T) {
	_, err := parseMultistatus([]byte(`<other xmlns="DAV:"/>`))
	assert.ErrorContains(t, err, "expected a multistatus")
	_, err = parseMultistatus([]byte(`not xml`))
	assert.Error(t, err)
	deep := strings.Repeat("<a>", maxElementDepth+2) + strings.Repeat("</a>", maxElementDepth+2)
	_, err = parseMultistatus([]byte(deep))
	assert.ErrorContains(t, err, "nested too deeply")
}
