package modfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPFetcherReadsOnceAndRefusesWhatItCannotUse(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/ok@1.0.0":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write([]byte("export const ok = 1;"))
		case "/big@1.0.0":
			_, _ = w.Write([]byte(strings.Repeat("x", int(maxModuleBytes)+1)))
		case "/moved@1.0.0":
			http.Redirect(w, r, "/ok@1.0.0", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	f := NewHTTPFetcher(srv.Client())
	for range 2 {
		body, ct, err := f.Fetch(context.Background(), srv.URL+"/ok@1.0.0")
		if err != nil || string(body) != "export const ok = 1;" || ct != "text/javascript" {
			t.Fatalf("fetch: %q %q %v", body, ct, err)
		}
	}
	if hits.Load() != 1 {
		t.Errorf("a cached module was fetched %d times", hits.Load())
	}
	for path, want := range map[string]string{"/nope@1.0.0": "404", "/big@1.0.0": "more than"} {
		if _, _, err := f.Fetch(context.Background(), srv.URL+path); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", path, err)
		}
	}
	if _, _, err := f.Fetch(context.Background(), "://bad"); err == nil {
		t.Error("a malformed URL was fetched")
	}
}

func TestTheGuardedFetcherRefusesRedirectsAndPrivateAddresses(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	f, err := NewGuardedFetcher()
	if err != nil {
		t.Fatal(err)
	}
	// The test server is on loopback, which the guard refuses before any
	// redirect could be followed.
	if _, _, err := f.Fetch(context.Background(), srv.URL+"/x@1.0.0"); err == nil {
		t.Fatal("a loopback module was fetched")
	}
	f.client.Transport = srv.Client().Transport
	if _, _, err := f.Fetch(context.Background(), srv.URL+"/x@1.0.0"); err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("a redirect was followed: %v", err)
	}
}

func TestTheCacheKeepsItsBound(t *testing.T) {
	f := NewHTTPFetcher(http.DefaultClient)
	big := make([]byte, maxCachedBytes/2+1)
	f.store(cached{url: "a", body: big})
	f.store(cached{url: "a", body: big})
	f.store(cached{url: "b", body: big})
	if _, ok := f.lookup("a"); ok {
		t.Error("the oldest module outlived the bound")
	}
	if _, ok := f.lookup("b"); !ok || f.held != int64(len(big)) {
		t.Errorf("held %d", f.held)
	}
}
