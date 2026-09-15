package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The reference is read over the origin that served it, and until #1750 the
// bytes it received named `localhost:8080` in every deployment: the generator's
// default, written into the annotations on a developer's machine. These hold
// the route to serving the reader's own origin, and to serving a document.

func TestServeSwaggerSpec_NamesTheOriginItWasServedFrom(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"http://mcp.example.com/api/v1/admin/docs/doc.json", http.NoBody)

	serveSwaggerSpec(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	// The request's own Host is written into the body, so the browser is told
	// not to decide for itself what the body is.
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("the served spec is not JSON: %v", err)
	}
	if doc["host"] != "mcp.example.com" {
		t.Errorf("host = %v, want the request's own host", doc["host"])
	}
	if doc["basePath"] != "/api/v1" {
		t.Errorf("basePath = %v, want /api/v1", doc["basePath"])
	}
}

func TestServeSwaggerSpec_CarriesTheIntroductionAndSchemeDescriptions(t *testing.T) {
	rec := httptest.NewRecorder()
	serveSwaggerSpec(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/api/v1/admin/docs/doc.json", http.NoBody))

	var doc struct {
		Info struct {
			Description string `json:"description"`
		} `json:"info"`
		SecurityDefinitions map[string]struct {
			Description string `json:"description"`
		} `json:"securityDefinitions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("the served spec is not JSON: %v", err)
	}

	// ReDoc splits info.description on its top-level headings and gives each
	// one a navigation entry, so these are sections of the reference rather
	// than a paragraph in front of it. A reader who opens the reference cold
	// makes their first call from what is on this page.
	for _, heading := range []string{
		"# Getting started",
		"# Authentication",
		"# Calling a connection through the gateway",
		"# Conventions",
	} {
		if !strings.Contains(doc.Info.Description, heading) {
			t.Errorf("the introduction has no %q section", heading)
		}
	}

	// "ApiKeyAuth or BearerAuth" expanding to the same two words is what a
	// reader saw before: neither scheme said what the credential is or where
	// one is obtained.
	for _, scheme := range []string{"ApiKeyAuth", "BearerAuth"} {
		if d := doc.SecurityDefinitions[scheme].Description; strings.TrimSpace(d) == "" {
			t.Errorf("%s carries no description", scheme)
		}
	}
}
