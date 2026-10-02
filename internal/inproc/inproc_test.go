package inproc

import (
	"net/http"
	"testing"
	"time"
)

func TestGet(t *testing.T) {
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

	body, ct, ok := Get(mux, "/ok", time.Second)
	if !ok || string(body) != "a{}" || ct != "text/css" {
		t.Fatalf("Get(/ok) = %q %q %v", body, ct, ok)
	}
	if _, _, ok := Get(mux, "/silent", time.Second); !ok {
		t.Error("a route that wrote nothing answered 200 and was refused")
	}
	if _, _, ok := Get(mux, "/gone", time.Second); ok {
		t.Error("a 410 was taken for an answer; the first status written is the one that counts")
	}
	if _, _, ok := Get(mux, "/missing", time.Second); ok {
		t.Error("a 404 was taken for an answer")
	}
	if _, _, ok := Get(mux, "%zz", time.Second); ok {
		t.Error("a path that is not a URL was answered")
	}
}
