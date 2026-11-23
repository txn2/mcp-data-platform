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

	res, ok := Read(mux, "/ok", nil, time.Second, 1<<20)
	if !ok || res.Status != http.StatusOK || string(res.Body) != "a{}" || res.Header.Get("Content-Type") != "text/css" {
		t.Fatalf("Read(/ok) = %v %d %q", ok, res.Status, res.Body)
	}
	if _, ok := Read(mux, "/silent", nil, time.Second, 1<<20); !ok {
		t.Error("a route that wrote nothing answered 200 and was refused")
	}
	if _, ok := Read(mux, "/gone", nil, time.Second, 1<<20); ok {
		t.Error("a 410 was taken for an answer; the first status written is the one that counts")
	}
	if _, ok := Read(mux, "/missing", nil, time.Second, 1<<20); ok {
		t.Error("a 404 was taken for an answer")
	}
	if _, ok := Read(mux, "%zz", nil, time.Second, 1<<20); ok {
		t.Error("a path that is not a URL was answered")
	}
}

// A refused read keeps the status the route answered, so a caller tells a
// file that is not there from one the platform could not serve; a route still
// working when the time is up reads as a 503 (#2218).
func TestRead_KeepsTheStatusOfARefusal(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /busy", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusInternalServerError)
	})
	for path, want := range map[string]int{"/busy": http.StatusServiceUnavailable, "/missing": http.StatusNotFound, "/slow": http.StatusServiceUnavailable} {
		res, ok := Read(mux, path, nil, 20*time.Millisecond, 1<<20)
		if ok || res.Status != want {
			t.Errorf("Read(%s) = %v %d, want refused with %d", path, ok, res.Status, want)
		}
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
	res, ok := Read(routes, "/a.bin", http.Header{"Range": {"bytes=2-4"}, "Origin": {"null"}, "Cookie": {"s=1"}}, time.Second, 1<<20)
	if !ok || res.Status != http.StatusPartialContent || string(res.Body) != "234" {
		t.Fatalf("ranged read = %v %d %q", ok, res.Status, res.Body)
	}
	if res.Header.Get("Content-Range") != "bytes 2-4/10" || res.Header.Get("X-Origin-Seen") != "null" {
		t.Errorf("headers = %v", res.Header)
	}

	if _, ok := Read(http.NotFoundHandler(), "/a.bin", nil, time.Second, 1<<20); ok {
		t.Error("a 404 was answered")
	}
	if _, ok := Read(routes, "://bad", nil, time.Second, 1<<20); ok {
		t.Error("an unparseable path was answered")
	}
}

// A route answering more than the reader takes is stopped where it passes the
// limit, or before a byte when it declares its length, and Read reports 413
// rather than the part it wrote (#2721).
func TestRead_ABodyPastTheLimitIsNotHeld(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/streamed", func(w http.ResponseWriter, _ *http.Request) {
		for range 4 {
			if _, err := w.Write(make([]byte, 400)); err != nil {
				return
			}
		}
		t.Error("the route kept writing after the limit refused it")
	})
	mux.HandleFunc("/declared", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "5000")
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("x")); err == nil {
			t.Error("a body declared past the limit was taken")
		}
	})
	mux.HandleFunc("/fits", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		w.WriteHeader(http.StatusTeapot) // a second status is ignored
		_, _ = w.Write(make([]byte, 1000))
	})
	for _, p := range []string{"/streamed", "/declared"} {
		res, ok := Read(mux, p, nil, time.Second, 1000)
		if ok || res.Status != http.StatusRequestEntityTooLarge || len(res.Body) != 0 {
			t.Errorf("Read(%s) = %v %d %d bytes, want 413", p, ok, res.Status, len(res.Body))
		}
	}
	res, ok := Read(mux, "/fits", nil, time.Second, 1000)
	if !ok || res.Status != http.StatusOK || len(res.Body) != 1000 {
		t.Errorf("Read(/fits) = %v %d %d bytes", ok, res.Status, len(res.Body))
	}
}
