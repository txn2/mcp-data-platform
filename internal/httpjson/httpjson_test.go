package httpjson

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestWriteJSONSetsContentTypeAndStatus(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	WriteJSON(w, http.StatusCreated, map[string]string{"id": "x"})

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if got["id"] != "x" {
		t.Errorf("body = %v, want id=x", got)
	}
}

// TestWriteJSONKeepsCallerContentType is the property WriteError depends on:
// if WriteJSON overwrote an already-set type, every error response would go
// out as application/json instead of application/problem+json.
func TestWriteJSONKeepsCallerContentType(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "application/problem+json")
	WriteJSON(w, http.StatusOK, map[string]string{})

	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want the caller's problem+json", ct)
	}
}

func TestWriteErrorEmitsProblemDetail(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	WriteError(w, http.StatusNotFound, "spec not found")

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	var pd ProblemDetail
	if err := json.Unmarshal(w.Body.Bytes(), &pd); err != nil {
		t.Fatalf("body is not a problem detail: %v", err)
	}
	if pd.Type != "about:blank" || pd.Status != http.StatusNotFound ||
		pd.Title != "Not Found" || pd.Detail != "spec not found" {
		t.Errorf("problem detail = %+v", pd)
	}
}

// TestWriteErrorOmitsEmptyDetail pins the omitempty tag: an error with no
// detail must not emit a `"detail": ""` field, which is what the parent
// packages' equivalents do.
func TestWriteErrorOmitsEmptyDetail(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	WriteError(w, http.StatusInternalServerError, "")

	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if _, present := raw["detail"]; present {
		t.Errorf("empty detail should be omitted, got %v", raw)
	}
}

func TestParseTimeParam(t *testing.T) {
	t.Parallel()
	want := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		value string
		want  *time.Time
	}{
		{"absent", "", nil},
		{"valid RFC3339", "2026-07-30T12:00:00Z", &want},
		{"unparseable widens rather than fails", "not-a-time", nil},
		{"wrong layout widens rather than fails", "2026-07-30", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := url.Values{}
			if tc.value != "" {
				q.Set("since", tc.value)
			}
			got := ParseTimeParam(q, "since")
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("got %v, want nil", got)
			case tc.want != nil && got == nil:
				t.Errorf("got nil, want %v", tc.want)
			case tc.want != nil && !got.Equal(*tc.want):
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		want  int
	}{
		{"absent means no preference", "", 0},
		{"numeric", "25", 25},
		{"non-numeric falls back", "abc", 0},
		{"negative is passed through for the caller to clamp", "-5", -5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := url.Values{}
			if tc.value != "" {
				q.Set("per_page", tc.value)
			}
			if got := ParseLimit(q); got != tc.want {
				t.Errorf("ParseLimit(%q) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
}

func TestParsePageOffset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		limit int
		want  int
	}{
		{"absent is the first page", "", 20, 0},
		{"page 1 is offset 0", "1", 20, 0},
		{"page 3 at 20 per page", "3", 20, 40},
		{"page 0 is treated as the first page", "0", 20, 0},
		{"negative page is treated as the first page", "-2", 20, 0},
		{"non-numeric is treated as the first page", "abc", 20, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := url.Values{}
			if tc.value != "" {
				q.Set("page", tc.value)
			}
			if got := ParsePageOffset(q, tc.limit); got != tc.want {
				t.Errorf("ParsePageOffset(page=%q, limit=%d) = %d, want %d",
					tc.value, tc.limit, got, tc.want)
			}
		})
	}
}
