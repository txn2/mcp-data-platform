package headless

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/egressguard"
)

const shot = "aGVsbG8=" // "hello": Render returns the screenshot's bytes untouched

func happy(overrides map[string]func(call) answer) func(call) answer {
	return func(c call) answer {
		if f, ok := overrides[c.Method]; ok {
			return f(c)
		}
		switch c.Method {
		case "Target.createBrowserContext":
			return ok(map[string]any{"browserContextId": "ctx1"})
		case "Target.createTarget":
			return ok(map[string]any{"targetId": "tgt1"})
		case "Target.attachToTarget":
			return ok(map[string]any{"sessionId": "page1"})
		case "Page.navigate":
			return ok(map[string]any{"frameId": "f1"})
		case "Runtime.evaluate":
			return ok(map[string]any{"result": map[string]any{"value": ""}})
		case "Page.captureScreenshot":
			return ok(map[string]any{"data": shot})
		}
		return answer{}
	}
}

// firstFeature is the first media feature an emulation call set.
func firstFeature(t *testing.T, c call) map[string]any {
	t.Helper()
	features, _ := c.Params["features"].([]any)
	if len(features) == 0 {
		t.Fatalf("%s set no media feature: %v", c.Method, c.Params)
	}
	f, _ := features[0].(map[string]any)
	return f
}

func tile() Page {
	return Page{Document: []byte("<p>x</p>"), Ready: "Promise.resolve('')", Width: 1280, Height: 960, Scale: 0.3125}
}

func find(t *testing.T, calls []call, method, session string) call {
	t.Helper()
	i := indexOf(calls, method, session)
	if i < 0 {
		t.Fatalf("no %s on session %q; calls: %v", method, session, methods(calls))
	}
	return calls[i]
}

func TestRender_ArmsThePageBeforeItNavigatesAndReturnsTheScreenshot(t *testing.T) {
	fb := newFakeBrowser(t, happy(nil))
	got, err := New(fb.endpoint(), nil).Render(context.Background(), tile())
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("Render returned %q, want the screenshot's bytes", got)
	}
	calls := fb.waitFor(func(c []call) bool { return has(c, "Target.disposeBrowserContext") })

	bc := find(t, calls, "Target.createBrowserContext", "")
	if bc.Params["proxyServer"] != deadProxy || bc.Params["proxyBypassList"] != noBypass {
		t.Errorf("the render's context is not behind the dead proxy: %v", bc.Params)
	}
	nav := indexOf(calls, "Page.navigate", "page1")
	for _, m := range []string{"Fetch.enable", "Page.addScriptToEvaluateOnNewDocument", "Target.setAutoAttach", "Emulation.setDeviceMetricsOverride"} {
		if i := indexOf(calls, m, "page1"); i < 0 || i > nav {
			t.Errorf("%s was not issued before the page navigated: %v", m, methods(calls))
		}
	}
	if src, _ := find(t, calls, "Page.addScriptToEvaluateOnNewDocument", "page1").Params["source"].(string); !contains(src, "WebSocket") {
		t.Errorf("the page's scrub does not remove WebSocket: %q", src)
	}
	aa := find(t, calls, "Target.setAutoAttach", "page1").Params
	if wait, _ := aa["waitForDebuggerOnStart"].(bool); !wait {
		t.Errorf("children are not held until armed: %v", aa)
	}
	if flat, _ := aa["flatten"].(bool); !flat {
		t.Errorf("children are not held until armed: %v", aa)
	}
	if u, _ := find(t, calls, "Page.navigate", "page1").Params["url"].(string); !strings.HasSuffix(strings.TrimSuffix(u, "/"), originSuffix) {
		t.Errorf("the page loads from %q, not a render origin", u)
	}
	media := firstFeature(t, find(t, calls, "Emulation.setEmulatedMedia", "page1"))
	if media["value"] != "light" {
		t.Errorf("color scheme = %v, want light", media["value"])
	}
	clip, _ := find(t, calls, "Page.captureScreenshot", "page1").Params["clip"].(map[string]any)
	if clip["scale"] != 0.3125 || clip["width"] != float64(1280) {
		t.Errorf("screenshot clip = %v", clip)
	}
	if dispose := find(t, calls, "Target.disposeBrowserContext", ""); dispose.Params["browserContextId"] != "ctx1" {
		t.Errorf("disposed %v, want the render's own context", dispose.Params)
	}
}

func TestRender_DarkEmulatesADarkScheme(t *testing.T) {
	fb := newFakeBrowser(t, happy(nil))
	p := tile()
	p.Dark = true
	if _, err := New(fb.endpoint(), nil).Render(context.Background(), p); err != nil {
		t.Fatalf("Render: %v", err)
	}
	media := firstFeature(t, find(t, fb.recorded(), "Emulation.setEmulatedMedia", "page1"))
	if media["value"] != "dark" {
		t.Errorf("color scheme = %v, want dark", media["value"])
	}
}

func TestRender_LayerSwitchesTurnOffOnlyTheirLayer(t *testing.T) {
	fb := newFakeBrowser(t, happy(nil))
	r := New(fb.endpoint(), nil)
	r.withoutProxy, r.withoutScrub = true, true
	if _, err := r.Render(context.Background(), tile()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	calls := fb.recorded()
	if _, ok := find(t, calls, "Target.createBrowserContext", "").Params["proxyServer"]; ok {
		t.Error("withoutProxy still set a proxy")
	}
	if has(calls, "Page.addScriptToEvaluateOnNewDocument") {
		t.Error("withoutScrub still installed the scrub")
	}
	if indexOf(calls, "Fetch.enable", "page1") < 0 {
		t.Error("switching layers off must never switch interception off")
	}
}

func TestRender_EachFailureIsReported(t *testing.T) {
	refuse := func(call) answer { return refused("nope") }
	for _, tc := range []struct {
		name      string
		overrides map[string]func(call) answer
		want      string
	}{
		{"context", map[string]func(call) answer{"Target.createBrowserContext": refuse}, "createBrowserContext"},
		{"target", map[string]func(call) answer{"Target.createTarget": refuse}, "createTarget"},
		{"attach", map[string]func(call) answer{"Target.attachToTarget": refuse}, "attachToTarget"},
		{"arming", map[string]func(call) answer{"Fetch.enable": refuse}, "Fetch.enable"},
		{"navigate refused", map[string]func(call) answer{"Page.navigate": refuse}, "Page.navigate"},
		{"navigate error", map[string]func(call) answer{"Page.navigate": func(call) answer {
			return ok(map[string]any{"errorText": "net::ERR_BLOCKED"})
		}}, "did not load: net::ERR_BLOCKED"},
		{"ready refused", map[string]func(call) answer{"Runtime.evaluate": refuse}, "Runtime.evaluate"},
		{"ready threw", map[string]func(call) answer{"Runtime.evaluate": func(call) answer {
			return ok(map[string]any{"exceptionDetails": map[string]any{"text": "Uncaught", "exception": map[string]any{"description": "TypeError: boom"}}})
		}}, "TypeError: boom"},
		{"ready threw without description", map[string]func(call) answer{"Runtime.evaluate": func(call) answer {
			return ok(map[string]any{"exceptionDetails": map[string]any{"text": "Uncaught"}})
		}}, "threw: Uncaught"},
		{"ready reason", map[string]func(call) answer{"Runtime.evaluate": func(call) answer {
			return ok(map[string]any{"result": map[string]any{"value": "a referenced file did not load"}})
		}}, "a referenced file did not load"},
		{"screenshot refused", map[string]func(call) answer{"Page.captureScreenshot": refuse}, "captureScreenshot"},
		{"screenshot garbled", map[string]func(call) answer{"Page.captureScreenshot": func(call) answer {
			return ok(map[string]any{"data": "%%%"})
		}}, "decoding the screenshot"},
		{"screenshot empty", map[string]func(call) answer{"Page.captureScreenshot": func(call) answer {
			return ok(map[string]any{"data": ""})
		}}, "empty screenshot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fb := newFakeBrowser(t, happy(tc.overrides))
			_, err := New(fb.endpoint(), nil).Render(context.Background(), tile())
			if err == nil || !contains(err.Error(), tc.want) {
				t.Fatalf("Render = %v, want an error naming %q", err, tc.want)
			}
		})
	}
}

func TestRender_APageThatNeverSettlesIsAbandonedAtTheDeadline(t *testing.T) {
	fb := newFakeBrowser(t, happy(map[string]func(call) answer{
		"Runtime.evaluate": func(call) answer {
			time.Sleep(400 * time.Millisecond)
			return ok(map[string]any{"result": map[string]any{"value": ""}})
		},
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := New(fb.endpoint(), nil).Render(ctx, tile())
	if err == nil || !contains(err.Error(), "did not finish drawing") {
		t.Fatalf("Render = %v, want the deadline named", err)
	}
	fb.waitFor(func(c []call) bool { return has(c, "Target.disposeBrowserContext") })
}

func TestRender_NoRendererAnswers(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	if _, err := New(srv.URL, nil).Render(context.Background(), tile()); err == nil || !contains(err.Error(), "no renderer answers") {
		t.Fatalf("Render = %v, want the renderer named as unreachable", err)
	}
}

func TestBrowserSocket(t *testing.T) {
	serve := func(status int, body string) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	good := serve(http.StatusOK, `{"webSocketDebuggerUrl":"ws://0.0.0.0:9222/devtools/browser/abc"}`)
	socket, err := browserSocket(context.Background(), strings.Replace(good, "http://", "ws://", 1))
	if err != nil {
		t.Fatalf("browserSocket: %v", err)
	}
	if want := strings.Replace(good, "http://", "ws://", 1) + "/devtools/browser/abc"; socket != want {
		t.Errorf("socket = %q, want %q: the reported in-container host must be replaced", socket, want)
	}

	for _, tc := range []struct{ name, endpoint, want string }{
		{"not a URL", "::nope", "not a URL with a host"},
		{"no host", "renderer", "not a URL with a host"},
		{"refused status", serve(http.StatusBadGateway, ""), "HTTP 502"},
		{"garbled", serve(http.StatusOK, "{"), "unreadable discovery"},
		{"no socket", serve(http.StatusOK, `{"webSocketDebuggerUrl":"http://x/y"}`), "no DevTools socket"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := browserSocket(context.Background(), tc.endpoint); err == nil || !contains(err.Error(), tc.want) {
				t.Fatalf("browserSocket(%q) = %v, want %q", tc.endpoint, err, tc.want)
			}
		})
	}

	t.Run("https becomes wss", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"webSocketDebuggerUrl":"ws://0.0.0.0:9222/devtools/browser/abc"}`))
		}))
		defer srv.Close()
		saved := http.DefaultClient.Transport
		http.DefaultClient.Transport = srv.Client().Transport
		defer func() { http.DefaultClient.Transport = saved }()
		socket, err := browserSocket(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("browserSocket: %v", err)
		}
		if !strings.HasPrefix(socket, "wss://") {
			t.Errorf("socket = %q, want wss", socket)
		}
	})
}

func TestConn_ACommandThatCannotBeCompletedSaysWhy(t *testing.T) {
	fb := newFakeBrowser(t, func(c call) answer {
		if c.Method == "Slow.command" {
			time.Sleep(300 * time.Millisecond)
		}
		if c.Method == "Wrong.shape" {
			return ok([]int{1})
		}
		return answer{}
	})
	c, err := dial(context.Background(), fb.endpoint(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.close()

	if err := c.call(context.Background(), "", "Bad.params", map[string]any{"x": make(chan int)}, nil); err == nil || !contains(err.Error(), "encoding") {
		t.Errorf("unencodable params = %v", err)
	}
	var out struct{ A string }
	if err := c.call(context.Background(), "", "Wrong.shape", nil, &out); err == nil || !contains(err.Error(), "decoding") {
		t.Errorf("wrong-shaped result = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.call(ctx, "", "Slow.command", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a command outliving its context = %v, want the deadline", err)
	}

	fb.dropClient()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := c.call(context.Background(), "", "After.close", nil, nil)
		if errors.Is(err, errConnClosed) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("a command after the browser went away = %v, want errConnClosed", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.close() // a second close is harmless
}

// TestConn_ACommandInFlightWhenTheBrowserGoesAwayIsReleased pins that a render
// cannot hang on a renderer that died mid-command: the command waiting on it
// returns rather than waiting out its whole deadline.
func TestConn_ACommandInFlightWhenTheBrowserGoesAwayIsReleased(t *testing.T) {
	fb := newFakeBrowser(t, func(c call) answer {
		if c.Method == "Never.answered" {
			time.Sleep(2 * time.Second)
		}
		return answer{}
	})
	c, err := dial(context.Background(), fb.endpoint(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.close()
	done := make(chan error, 1)
	go func() { done <- c.call(context.Background(), "", "Never.answered", nil, nil) }()
	fb.waitFor(func(calls []call) bool { return has(calls, "Never.answered") })
	fb.dropClient()
	select {
	case err := <-done:
		if !errors.Is(err, errConnClosed) {
			t.Fatalf("the in-flight command returned %v, want errConnClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("the in-flight command was not released when the browser went away")
	}
}

func TestConn_EventsReachTheHandler(t *testing.T) {
	fb := newFakeBrowser(t, nil)
	got := make(chan message, 1)
	c, err := dial(context.Background(), fb.endpoint(), func(m message) { got <- m })
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.close()
	if err := c.call(context.Background(), "", "Hello.world", nil, nil); err != nil {
		t.Fatalf("call: %v", err)
	}
	fb.emit("s1", "Some.event", map[string]any{"k": "v"})
	select {
	case m := <-got:
		if m.Method != "Some.event" || m.SessionID != "s1" {
			t.Errorf("event = %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the event never reached the handler")
	}
}

// harness is a render wired to a fake browser, for driving arm and answer
// directly.
func harness(t *testing.T, reply func(call) answer, public *http.Client) (*render, *fakeBrowser) {
	t.Helper()
	fb := newFakeBrowser(t, reply)
	rs := &render{r: New(fb.endpoint(), public), host: "t-0.render.invalid", sessions: map[string]bool{}}
	c, err := dial(context.Background(), fb.endpoint(), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(c.close)
	rs.c = c
	rs.own("page1")
	rs.page = Page{
		Document: []byte("<p>doc</p>"),
		Files: func(path string) (File, bool) {
			if path == "/theme.css" {
				return File{Body: []byte("css"), ContentType: "text/css"}, true
			}
			return File{}, false
		},
	}
	return rs, fb
}

func event(session, method string, params any) message {
	raw, _ := json.Marshal(params)
	return message{Method: method, SessionID: session, Params: raw}
}

func TestArm_AFrameIsArmedThenReleased(t *testing.T) {
	rs, fb := harness(t, nil, nil)
	rs.onEvent(event("page1", "Target.attachedToTarget", map[string]any{"sessionId": "frame1", "targetInfo": map[string]any{"type": "iframe"}}))
	calls := fb.recorded()
	release := indexOf(calls, "Runtime.runIfWaitingForDebugger", "frame1")
	for _, m := range []string{"Fetch.enable", "Page.addScriptToEvaluateOnNewDocument", "Target.setAutoAttach"} {
		if i := indexOf(calls, m, "frame1"); i < 0 || i > release {
			t.Errorf("%s was not issued on the frame before it was released: %v", m, methods(calls))
		}
	}
	if !rs.owns("frame1") {
		t.Error("the frame's session is not the render's to answer")
	}
}

func TestArm_AWorkerIsScrubbedThenReleased(t *testing.T) {
	rs, fb := harness(t, nil, nil)
	rs.onEvent(event("page1", "Target.attachedToTarget", map[string]any{"sessionId": "w1", "targetInfo": map[string]any{"type": "worker"}}))
	calls := fb.recorded()
	scrubAt := indexOf(calls, "Runtime.evaluate", "w1")
	if scrubAt < 0 || scrubAt > indexOf(calls, "Runtime.runIfWaitingForDebugger", "w1") {
		t.Fatalf("the worker was not scrubbed before it ran: %v", methods(calls))
	}
	if expr, _ := calls[scrubAt].Params["expression"].(string); !contains(expr, "WebSocket") {
		t.Errorf("the worker's scrub does not remove WebSocket: %q", expr)
	}
}

func TestArm_AChildThatCannotBeArmedIsNeverReleased(t *testing.T) {
	rs, fb := harness(t, func(c call) answer {
		if c.Session == "frame1" && c.Method == "Fetch.enable" {
			return refused("no")
		}
		return answer{}
	}, nil)
	rs.onEvent(event("page1", "Target.attachedToTarget", map[string]any{"sessionId": "frame1", "targetInfo": map[string]any{"type": "iframe"}}))
	if indexOf(fb.recorded(), "Runtime.runIfWaitingForDebugger", "frame1") >= 0 {
		t.Fatal("a frame that could not be armed was released")
	}
	if err := rs.failedArming(); err == nil || !contains(err.Error(), "could not be secured") {
		t.Fatalf("failedArming = %v, want the arming failure", err)
	}
}

func TestOnEvent_IgnoresWhatIsNotTheRendersOwn(t *testing.T) {
	rs, fb := harness(t, nil, nil)
	rs.onEvent(event("someone-else", "Target.attachedToTarget", map[string]any{"sessionId": "x", "targetInfo": map[string]any{"type": "iframe"}}))
	rs.onEvent(event("someone-else", "Fetch.requestPaused", map[string]any{"requestId": "r1", "request": map[string]any{"url": "http://t-0.render.invalid/"}}))
	rs.onEvent(message{Method: "Target.attachedToTarget", SessionID: "page1", Params: json.RawMessage(`{`)})
	rs.onEvent(message{Method: "Fetch.requestPaused", SessionID: "page1", Params: json.RawMessage(`{`)})
	time.Sleep(50 * time.Millisecond)
	if calls := fb.recorded(); len(calls) != 0 {
		t.Fatalf("answered events that were not the render's own: %v", methods(calls))
	}
}

func paused(id, method, url string, headers map[string]string) message {
	return event("page1", "Fetch.requestPaused", map[string]any{
		"requestId": id, "request": map[string]any{"url": url, "method": method, "headers": headers},
	})
}

func decodedBody(t *testing.T, c call) string {
	t.Helper()
	body, _ := c.Params["body"].(string)
	b, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("fulfilled body is not base64: %v", err)
	}
	return string(b)
}

func headerOf(c call, name string) string {
	hs, _ := c.Params["responseHeaders"].([]any)
	for _, h := range hs {
		m, _ := h.(map[string]any)
		if n, _ := m["name"].(string); strings.EqualFold(n, name) {
			v, _ := m["value"].(string)
			return v
		}
	}
	return ""
}

func TestAnswer_ThePagesOwnOrigin(t *testing.T) {
	rs, fb := harness(t, nil, nil)
	rs.answer(paused("doc", "GET", "http://t-0.render.invalid/", nil))
	rs.answer(paused("css", "GET", "http://t-0.render.invalid/theme.css", nil))
	rs.answer(paused("missing", "GET", "http://T-0.RENDER.INVALID/nothing.js", nil))
	calls := fb.recorded()
	byID := map[string]call{}
	for _, c := range calls {
		if id, _ := c.Params["requestId"].(string); id != "" {
			byID[id] = c
		}
	}
	if c := byID["doc"]; c.Method != "Fetch.fulfillRequest" || decodedBody(t, c) != "<p>doc</p>" || headerOf(c, "Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("the document was answered with %+v", c)
	}
	if c := byID["css"]; decodedBody(t, c) != "css" || headerOf(c, "Content-Type") != "text/css" {
		t.Errorf("a declared file was answered with %+v", c)
	}
	if c := byID["missing"]; c.Params["responseCode"] != float64(http.StatusNotFound) {
		t.Errorf("a file the page does not have was answered %v, want 404", c.Params["responseCode"])
	}
}

func TestAnswer_EverythingElseIsRefusedWithoutAPublicClient(t *testing.T) {
	rs, fb := harness(t, nil, nil)
	rs.answer(paused("pub", "GET", "https://example.com/logo.png", nil))
	rs.answer(paused("bad", "GET", "http://%zz", nil))
	calls := fb.waitFor(func(c []call) bool { return count(c, "Fetch.failRequest") == 2 })
	for _, c := range calls {
		if c.Method == "Fetch.failRequest" && c.Params["errorReason"] != "BlockedByClient" {
			t.Errorf("refusal reason = %v", c.Params["errorReason"])
		}
	}
	if has(calls, "Fetch.fulfillRequest") || has(calls, "Fetch.continueRequest") {
		t.Fatalf("a request outside the page's origin was served without a public client: %v", methods(calls))
	}
}

func TestAnswer_APublicResourceIsFetchedByThePlatform(t *testing.T) {
	var gotUA, gotCookie string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotCookie = r.Header.Get("User-Agent"), r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Set-Cookie", "session=secret")
		_, _ = w.Write([]byte("export default 1"))
	}))
	defer upstream.Close()
	rs, fb := harness(t, nil, upstream.Client())

	rs.answer(paused("mod", "GET", upstream.URL+"/react.js", map[string]string{"user-agent": "Chrome/151", "Cookie": "c=1"}))
	c := find(t, fb.recorded(), "Fetch.fulfillRequest", "page1")
	if decodedBody(t, c) != "export default 1" || c.Params["responseCode"] != float64(http.StatusOK) {
		t.Fatalf("the public resource was answered with %+v", c)
	}
	if headerOf(c, "Access-Control-Allow-Origin") != "*" {
		t.Error("the CORS header a cross-origin module needs was dropped")
	}
	if headerOf(c, "Set-Cookie") != "" {
		t.Error("a header outside the allow-list reached the page")
	}
	if gotUA != "Chrome/151" || gotCookie != "" {
		t.Errorf("forwarded User-Agent %q, Cookie %q: only the allow-listed request headers may be sent", gotUA, gotCookie)
	}
}

type failingTransport struct{ err error }

func (f failingTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

func TestAnswer_APublicFetchThatCannotCompleteFails(t *testing.T) {
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, maxPublicBody+1))
	}))
	defer huge.Close()
	for _, tc := range []struct {
		name   string
		client *http.Client
		url    string
		method string
		reason string
	}{
		{"the guard refuses it", &http.Client{Transport: failingTransport{&egressguard.BlockedError{Host: "10.0.0.1", Reason: "private"}}}, "http://10.0.0.1/x", "GET", "BlockedByClient"},
		{"the network fails", &http.Client{Transport: failingTransport{errors.New("boom")}}, "https://example.com/x", "GET", "Failed"},
		{"the body is too large", huge.Client(), huge.URL + "/x", "GET", "Failed"},
		{"it is not a read", huge.Client(), huge.URL + "/x", "POST", "BlockedByClient"},
		{"it is not http", huge.Client(), "ftp://example.com/x", "GET", "BlockedByClient"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs, fb := harness(t, nil, tc.client)
			rs.answer(paused("r", tc.method, tc.url, nil))
			c := find(t, fb.recorded(), "Fetch.failRequest", "page1")
			if c.Params["errorReason"] != tc.reason {
				t.Errorf("reason = %v, want %s", c.Params["errorReason"], tc.reason)
			}
		})
	}
}

func TestOriginHost_IsUniqueAndReserved(t *testing.T) {
	a, b := originHost(), originHost()
	if a == b {
		t.Errorf("two renders share origin %q", a)
	}
	if !strings.HasSuffix(a, originSuffix) {
		t.Errorf("origin %q is not under %s", a, originSuffix)
	}
}

func TestRender_AnUnreachableRendererIsTellableFromABadDocument(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	r := New(srv.URL, nil)
	if err := r.Ping(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Ping = %v, want ErrUnavailable", err)
	}
	if _, err := r.Render(context.Background(), tile()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Render = %v, want ErrUnavailable", err)
	}

	fb := newFakeBrowser(t, happy(map[string]func(call) answer{
		"Runtime.evaluate": func(call) answer {
			return ok(map[string]any{"result": map[string]any{"value": "a referenced file did not load"}})
		},
	}))
	good := New(fb.endpoint(), nil)
	if err := good.Ping(context.Background()); err != nil {
		t.Fatalf("Ping of a live renderer = %v", err)
	}
	if _, err := good.Render(context.Background(), tile()); err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("a document's own failure = %v; it must not read as the renderer being unavailable", err)
	}
}
