package headless

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/egressguard"
)

// pausedAs is paused with the type the browser is loading the URL as.
func pausedAs(id, resourceType, url string, headers map[string]string) message {
	return event("page1", "Fetch.requestPaused", map[string]any{
		"requestId": id, "resourceType": resourceType,
		"request": map[string]any{"url": url, "method": "GET", "headers": headers},
	})
}

// A page that requires its subresources ends with ErrIncomplete when a public
// stylesheet or script did not arrive for a reason that may pass -- the
// network, 429, 5xx -- and draws without one its server says does not exist,
// one the guard refuses, a font or an image (#2122).
func TestRequireSubresources_RecordsWhatMayArriveNextTime(t *testing.T) {
	status := map[string]int{"/busy.css": 503, "/limited.js": 429, "/gone.css": 404, "/ok.css": 200}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status[r.URL.Path])
	}))
	defer upstream.Close()
	for _, tc := range []struct {
		name, resourceType, path string
		client                   *http.Client
		missing                  bool
	}{
		{"a stylesheet whose server is failing", "Stylesheet", "/busy.css", upstream.Client(), true},
		{"a module the server rate limited", "Script", "/limited.js", upstream.Client(), true},
		{"a stylesheet the network could not fetch", "Stylesheet", "/x.css", &http.Client{Transport: failingTransport{errors.New("boom")}}, true},
		{"a stylesheet that does not exist", "Stylesheet", "/gone.css", upstream.Client(), false},
		{"a stylesheet that arrived", "Stylesheet", "/ok.css", upstream.Client(), false},
		{"a font", "Font", "/x.woff2", &http.Client{Transport: failingTransport{errors.New("boom")}}, false},
		{"an image", "Image", "/x.png", &http.Client{Transport: failingTransport{errors.New("boom")}}, false},
		{"a stylesheet the guard refuses", "Stylesheet", "/x.css", &http.Client{Transport: failingTransport{&egressguard.BlockedError{Host: "10.0.0.1", Reason: "private"}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rs, fb := harness(t, nil, tc.client)
			rs.page.RequireSubresources = true
			rs.answer(pausedAs("r", tc.resourceType, upstream.URL+tc.path, nil))
			fb.waitFor(func(c []call) bool { return has(c, "Fetch.failRequest") || has(c, "Fetch.fulfillRequest") })
			err := rs.missingFiles()
			if got := err != nil; got != tc.missing {
				t.Fatalf("missing = %v (%v), want %v", got, err, tc.missing)
			}
			if tc.missing && (!errors.Is(err, ErrIncomplete) || !strings.Contains(err.Error(), tc.path)) {
				t.Errorf("err = %v, want ErrIncomplete naming %s", err, tc.path)
			}
		})
	}
}

// A page that does not ask (the PDF print) is printed with what arrived.
func TestRequireSubresources_OffByDefault(t *testing.T) {
	rs, fb := harness(t, nil, &http.Client{Transport: failingTransport{errors.New("boom")}})
	rs.answer(pausedAs("r", "Stylesheet", "https://cdn.example.com/x.css", nil))
	fb.waitFor(func(c []call) bool { return has(c, "Fetch.failRequest") })
	if err := rs.missingFiles(); err != nil {
		t.Fatalf("a page that did not require its subresources failed: %v", err)
	}
}

// The second render naming a public file is answered from the first's fetch,
// with the CORS origin it reflected answered for the new render's origin.
func TestPublicCache_ReusesAFetchAcrossRenders(t *testing.T) {
	var fetched atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched.Add(1)
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		_, _ = w.Write([]byte("body{color:red}"))
	}))
	defer upstream.Close()
	rs, fb := harness(t, nil, upstream.Client())

	rs.answer(pausedAs("a", "Stylesheet", upstream.URL+"/s.css", map[string]string{"Origin": "http://t-a.render.invalid"}))
	rs.answer(pausedAs("b", "Stylesheet", upstream.URL+"/s.css", map[string]string{"Origin": "http://t-b.render.invalid"}))
	calls := fb.waitFor(func(c []call) bool { return count(c, "Fetch.fulfillRequest") == 2 })
	if n := fetched.Load(); n != 1 {
		t.Fatalf("the file was fetched %d times, want once", n)
	}
	for _, c := range calls {
		if c.Method != "Fetch.fulfillRequest" {
			continue
		}
		if decodedBody(t, c) != "body{color:red}" {
			t.Errorf("a cached answer lost its body: %+v", c)
		}
		want := "http://t-a.render.invalid"
		if c.Params["requestId"] == "b" {
			want = "http://t-b.render.invalid"
		}
		if got := headerOf(c, "Access-Control-Allow-Origin"); got != want {
			t.Errorf("request %v: Access-Control-Allow-Origin = %q, want %q", c.Params["requestId"], got, want)
		}
	}
}

func TestPublicCache_KeepsOnlyWhatMayBeReused(t *testing.T) {
	get := func(url string, h map[string]string) pausedRequest {
		var p pausedRequest
		p.Request.URL, p.Request.Method, p.Request.Headers = url, http.MethodGet, h
		return p
	}
	c := newPublicCache()
	now := time.Unix(0, 0)
	c.now = func() time.Time { return now }
	ok := reply{http.StatusOK, map[string]string{}, []byte("x")}

	c.put(get("https://a/no-store", nil), ok, "no-store")
	c.put(get("https://a/private", nil), ok, "private, max-age=60")
	c.put(get("https://a/missing", nil), reply{http.StatusNotFound, nil, nil}, "")
	c.put(get("https://a/range", map[string]string{"Range": "bytes=0-1"}), ok, "")
	for _, u := range []string{"https://a/no-store", "https://a/private", "https://a/missing"} {
		if _, hit := c.get(get(u, nil)); hit {
			t.Errorf("%s was kept", u)
		}
	}
	if _, hit := c.get(get("https://a/range", map[string]string{"Range": "bytes=0-1"})); hit {
		t.Error("a ranged read was kept")
	}

	c.put(get("https://a/kept", nil), ok, "public, max-age=60")
	if _, hit := c.get(get("https://a/kept", nil)); !hit {
		t.Fatal("a reusable answer was not kept")
	}
	now = now.Add(publicCacheTTL + time.Second)
	if _, hit := c.get(get("https://a/kept", nil)); hit {
		t.Error("an answer older than the TTL was reused")
	}

	c.maxBytes = 2
	c.put(get("https://a/1", nil), ok, "")
	c.put(get("https://a/2", nil), ok, "")
	c.put(get("https://a/3", nil), ok, "")
	if _, hit := c.get(get("https://a/1", nil)); hit {
		t.Error("the oldest answer was not dropped past the size bound")
	}
	if _, hit := c.get(get("https://a/3", nil)); !hit {
		t.Error("the newest answer was dropped")
	}
}
