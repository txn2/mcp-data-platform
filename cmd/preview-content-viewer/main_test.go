package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewHandler(t *testing.T) {
	handler := newHandler()

	tests := []struct {
		name        string
		query       string
		wantStatus  int
		wantStrings []string
	}{
		{
			name:       "default type is markdown",
			query:      "",
			wantStatus: http.StatusOK,
			wantStrings: []string{
				"Preview: markdown",
				"text/markdown",
				`id="content-data"`,
				`id="content-root"`,
			},
		},
		{
			name:       "explicit markdown",
			query:      "?type=markdown",
			wantStatus: http.StatusOK,
			wantStrings: []string{
				"Preview: markdown",
				"text/markdown",
			},
		},
		{
			name:       "svg type",
			query:      "?type=svg",
			wantStatus: http.StatusOK,
			wantStrings: []string{
				"Preview: svg",
				"image/svg+xml",
			},
		},
		{
			name:       "jsx type",
			query:      "?type=jsx",
			wantStatus: http.StatusOK,
			wantStrings: []string{
				"Preview: jsx",
				"text/jsx",
			},
		},
		{
			name:       "html type",
			query:      "?type=html",
			wantStatus: http.StatusOK,
			wantStrings: []string{
				"Preview: html",
				"text/html",
			},
		},
		{
			name:       "plain type",
			query:      "?type=plain",
			wantStatus: http.StatusOK,
			wantStrings: []string{
				"Preview: plain",
				"text/plain",
			},
		},
		{
			name:       "unknown type falls back to markdown",
			query:      "?type=unknown",
			wantStatus: http.StatusOK,
			wantStrings: []string{
				"Preview: unknown",
				"text/markdown",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/"+tt.query, http.NoBody)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			ct := w.Header().Get("Content-Type")
			if ct != "text/html; charset=utf-8" {
				t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
			}

			body := w.Body.String()
			for _, s := range tt.wantStrings {
				if !contains(body, s) {
					t.Errorf("body missing %q", s)
				}
			}
		})
	}
}

func TestSamplesComplete(t *testing.T) {
	expected := []string{"markdown", "svg", "jsx", "html", "plain"}
	for _, key := range expected {
		sample, ok := samples[key]
		if !ok {
			t.Errorf("samples missing key %q", key)
			continue
		}
		if sample[0] == "" {
			t.Errorf("samples[%q] content type is empty", key)
		}
		if sample[1] == "" {
			t.Errorf("samples[%q] content is empty", key)
		}
	}
}

func TestViewerTemplateValid(t *testing.T) {
	// viewerTpl is parsed at init time; this test verifies it didn't panic.
	if viewerTpl == nil {
		t.Fatal("viewerTpl is nil — template parsing failed")
	}
}

// contains is a simple helper to avoid importing strings in test.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
