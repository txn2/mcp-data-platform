package inproc

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRead(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ok", func(w http.ResponseWriter, r *http.Request) {
		if r.RemoteAddr != "127.0.0.1:0" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write([]byte("a{}"))
	})
	mux.HandleFunc("GET /silent", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET /gone", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
		w.WriteHeader(http.StatusOK)
	})

	res, ok := Read(mux, "/ok", nil, time.Second)
	if !ok || res.Status != http.StatusOK || string(res.Body) != "a{}" || res.Header.Get("Content-Type") != "text/css" {
		t.Fatalf("Read(/ok) = %v %d %q", ok, res.Status, res.Body)
	}
	if _, ok := Read(mux, "/silent", nil, time.Second); !ok {
		t.Error("a route that wrote nothing answered 200 and was refused")
	}
	if _, ok := Read(mux, "/gone", nil, time.Second); ok {
		t.Error("a 410 was taken for an answer; the first status written is the one that counts")
	}
	if _, ok := Read(mux, "/missing", nil, time.Second); ok {
		t.Error("a 404 was taken for an answer")
	}
	if _, ok := Read(mux, "%zz", nil, time.Second); ok {
		t.Error("a path that is not a URL was answered")
	}
}

// TestRead_PassesARangedReadThrough: a map's basemap is read by range, so an
// in-process read forwards the Range and Origin a page sent and answers the
// route's 206 with its headers (#2068).
func TestRead_PassesARangedReadThrough(t *testing.T) {
	routes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("X-Origin-Seen", r.Header.Get("Origin"))
		http.ServeContent(w, r, "a.bin", time.Time{}, strings.NewReader("0123456789"))
	})
	res, ok := Read(routes, "/a.bin", http.Header{"Range": {"bytes=2-4"}, "Origin": {"null"}, "Cookie": {"s=1"}}, time.Second)
	if !ok || res.Status != http.StatusPartialContent || string(res.Body) != "234" {
		t.Fatalf("ranged read = %v %d %q", ok, res.Status, res.Body)
	}
	if res.Header.Get("Content-Range") != "bytes 2-4/10" || res.Header.Get("X-Origin-Seen") != "null" {
		t.Errorf("headers = %v", res.Header)
	}

	if _, ok := Read(http.NotFoundHandler(), "/a.bin", nil, time.Second); ok {
		t.Error("a 404 was answered")
	}
	if _, ok := Read(routes, "://bad", nil, time.Second); ok {
		t.Error("an unparseable path was answered")
	}
}
