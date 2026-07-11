package oidcdiscovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetch_ParsesDocument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != WellKnownPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"issuer": "https://idp.example.com",
			"authorization_endpoint": "https://idp.example.com/authorize",
			"token_endpoint": "https://idp.example.com/token",
			"jwks_uri": "https://idp.example.com/jwks"
		}`))
	}))
	defer srv.Close()

	doc, err := Fetch(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if doc.AuthorizationEndpoint != "https://idp.example.com/authorize" {
		t.Errorf("authorization_endpoint = %q", doc.AuthorizationEndpoint)
	}
	if doc.TokenEndpoint != "https://idp.example.com/token" {
		t.Errorf("token_endpoint = %q", doc.TokenEndpoint)
	}
	if doc.JWKSURI != "https://idp.example.com/jwks" {
		t.Errorf("jwks_uri = %q", doc.JWKSURI)
	}
}

func TestFetch_TrimsTrailingSlashAndDefaultsClient(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"x"}`))
	}))
	defer srv.Close()

	// Trailing slash on the issuer must not double up the path; nil client falls
	// back to http.DefaultClient.
	if _, err := Fetch(context.Background(), nil, srv.URL+"/"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotPath != WellKnownPath {
		t.Errorf("requested path = %q, want %q", gotPath, WellKnownPath)
	}
}

func TestFetch_TrimsWhitespaceIssuer(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"x"}`))
	}))
	defer srv.Close()

	// A trailing newline (common from YAML block scalars) must not corrupt the
	// discovery URL.
	if _, err := Fetch(context.Background(), srv.Client(), srv.URL+"\n"); err != nil {
		t.Fatalf("Fetch with trailing newline: %v", err)
	}
	if gotPath != WellKnownPath {
		t.Errorf("requested path = %q, want %q", gotPath, WellKnownPath)
	}
}

func TestFetch_BoundsResponseBody(t *testing.T) {
	// Serve more bytes than the cap as an incomplete JSON object; the limited
	// reader truncates it, so decoding fails rather than reading it all into
	// memory.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"authorization_endpoint":"`))
		blob := make([]byte, maxDocumentBytes+1024)
		for i := range blob {
			blob[i] = 'a'
		}
		_, _ = w.Write(blob) // never closes the JSON string
	}))
	defer srv.Close()

	if _, err := Fetch(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected decode error for oversized body truncated at the cap")
	}
}

func TestFetch_Errors(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", http.StatusInternalServerError)
		}))
		defer srv.Close()
		if _, err := Fetch(context.Background(), srv.Client(), srv.URL); err == nil {
			t.Fatal("expected error on non-200")
		}
	})

	t.Run("bad JSON", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{not json`))
		}))
		defer srv.Close()
		if _, err := Fetch(context.Background(), srv.Client(), srv.URL); err == nil {
			t.Fatal("expected error on malformed body")
		}
	})

	t.Run("invalid issuer URL", func(t *testing.T) {
		if _, err := Fetch(context.Background(), http.DefaultClient, "http://ex\x7fample.com"); err == nil {
			t.Fatal("expected error building request from invalid URL")
		}
	})

	t.Run("unreachable host", func(t *testing.T) {
		_, err := Fetch(context.Background(), http.DefaultClient, "http://127.0.0.1:1")
		if err == nil || !strings.Contains(err.Error(), "fetching discovery document") {
			t.Fatalf("expected transport error, got %v", err)
		}
	})
}
