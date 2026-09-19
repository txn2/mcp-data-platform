package headless

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// call is one command the fake browser received.
type call struct {
	Method  string
	Session string
	Params  map[string]any
}

// fakeBrowser speaks the DevTools protocol over a real WebSocket, answering
// each command from reply and recording what it was sent, so the renderer's
// sequence and its interception decisions are tested without a browser.
type fakeBrowser struct {
	t      *testing.T
	server *httptest.Server
	// reply answers a command; the zero answer is an empty result.
	reply func(c call) answer

	mu    sync.Mutex
	calls []call
	ws    *websocket.Conn
}

// answer is what the fake browser sends back for one command: a result, or a
// protocol error in its place.
type answer struct {
	result any
	err    *protocolError
}

// ok answers a command with result.
func ok(result any) answer { return answer{result: result} }

// refused answers a command with a protocol error.
func refused(message string) answer {
	return answer{err: &protocolError{Code: -32000, Message: message}}
}

func newFakeBrowser(t *testing.T, reply func(c call) answer) *fakeBrowser {
	t.Helper()
	fb := &fakeBrowser{t: t, reply: reply}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, _ *http.Request) {
		// The address inside the "container"; the client must replace it.
		_, _ = w.Write([]byte(`{"webSocketDebuggerUrl":"ws://0.0.0.0:9222/devtools/browser/fake"}`))
	})
	mux.HandleFunc("/devtools/browser/fake", fb.serve)
	fb.server = httptest.NewServer(mux)
	t.Cleanup(fb.server.Close)
	return fb
}

func (fb *fakeBrowser) endpoint() string { return fb.server.URL }

func (fb *fakeBrowser) serve(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(maxMessageBytes)
	fb.mu.Lock()
	fb.ws = ws
	fb.mu.Unlock()
	ctx := context.Background()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var m message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		var params map[string]any
		_ = json.Unmarshal(m.Params, &params)
		c := call{Method: m.Method, Session: m.SessionID, Params: params}
		fb.mu.Lock()
		fb.calls = append(fb.calls, c)
		fb.mu.Unlock()
		var result any = map[string]any{}
		var perr *protocolError
		if fb.reply != nil {
			a := fb.reply(c)
			if a.err != nil {
				perr = a.err
			} else if a.result != nil {
				result = a.result
			}
		}
		resp := map[string]any{"id": m.ID}
		if perr != nil {
			resp["error"] = perr
		} else {
			resp["result"] = result
		}
		out, _ := json.Marshal(resp)
		_ = ws.Write(ctx, websocket.MessageText, out)
	}
}

// emit sends an event to the client as if the browser raised it.
func (fb *fakeBrowser) emit(session, method string, params any) {
	fb.t.Helper()
	raw, _ := json.Marshal(params)
	out, _ := json.Marshal(message{Method: method, Params: raw, SessionID: session})
	fb.mu.Lock()
	ws := fb.ws
	fb.mu.Unlock()
	if ws == nil {
		fb.t.Fatal("emit before a client connected")
	}
	if err := ws.Write(context.Background(), websocket.MessageText, out); err != nil {
		fb.t.Fatalf("emit %s: %v", method, err)
	}
}

// dropClient closes the client's socket from the browser's side.
func (fb *fakeBrowser) dropClient() {
	fb.mu.Lock()
	ws := fb.ws
	fb.mu.Unlock()
	if ws != nil {
		_ = ws.Close(websocket.StatusGoingAway, "gone")
	}
}

func (fb *fakeBrowser) recorded() []call {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return append([]call(nil), fb.calls...)
}

// waitFor polls until pred holds over the recorded calls.
func (fb *fakeBrowser) waitFor(pred func([]call) bool) []call {
	fb.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		calls := fb.recorded()
		if pred(calls) {
			return calls
		}
		if time.Now().After(deadline) {
			fb.t.Fatalf("timed out; calls so far: %v", methods(calls))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func methods(calls []call) []string {
	out := make([]string, len(calls))
	for i, c := range calls {
		out[i] = c.Session + ":" + c.Method
	}
	return out
}

func indexOf(calls []call, method, session string) int {
	for i, c := range calls {
		if c.Method == method && c.Session == session {
			return i
		}
	}
	return -1
}

func has(calls []call, method string) bool {
	for _, c := range calls {
		if c.Method == method {
			return true
		}
	}
	return false
}

func count(calls []call, method string) int {
	n := 0
	for _, c := range calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
