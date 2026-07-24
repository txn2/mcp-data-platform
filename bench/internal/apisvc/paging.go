package apisvc

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// Pagination defaults, mirrored by the page_size parameter description in
// the specs.
const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// httpError is the JSON error envelope.
type httpError struct {
	Error string `json:"error"`
}

// writeJSON writes a JSON response with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError writes a JSON error with the given status.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, httpError{Error: msg})
}

// badParam is a 400-with-message sentinel used by parameter parsing.
type badParam struct{ msg string }

func (e badParam) Error() string { return e.msg }

// pageWindow parses cursor and page_size and returns the [from, to)
// window over n items plus the next cursor ("" when the window reaches
// the end).
func pageWindow(r *http.Request, n int) (from, to int, next string, err error) {
	size := defaultPageSize
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		size, err = strconv.Atoi(raw)
		if err != nil || size < 1 {
			return 0, 0, "", badParam{"page_size must be a positive integer"}
		}
		size = min(size, maxPageSize)
	}
	offset := 0
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		offset, err = decodeCursor(raw)
		if err != nil {
			return 0, 0, "", err
		}
	}
	if offset > n {
		offset = n
	}
	from, to = offset, min(offset+size, n)
	if to < n {
		next = encodeCursor(to)
	}
	return from, to, next, nil
}

// encodeCursor renders an offset as an opaque cursor.
func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

// decodeCursor parses an opaque cursor back to an offset.
func decodeCursor(raw string) (int, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, badParam{"invalid cursor"}
	}
	offset, err := strconv.Atoi(string(b))
	if err != nil || offset < 0 {
		return 0, badParam{"invalid cursor"}
	}
	return offset, nil
}

// listEnvelope is the paged list/search response body.
type listEnvelope struct {
	Items      []any  `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// writePage writes one page of items.
func writePage(w http.ResponseWriter, r *http.Request, items []any) {
	from, to, next, err := pageWindow(r, len(items))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, listEnvelope{Items: items[from:to], NextCursor: next})
}

// parseTimeParam parses an optional RFC 3339 (or YYYY-MM-DD) query
// parameter. Returns the zero time when absent.
func parseTimeParam(r *http.Request, name string) (time.Time, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t, nil
	}
	return time.Time{}, badParam{name + " must be an ISO 8601 timestamp"}
}

// parseIntParam parses an optional integer query parameter. Returns
// (0, false, nil) when absent.
func parseIntParam(r *http.Request, name string) (int64, bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, false, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false, badParam{name + " must be an integer"}
	}
	return v, true, nil
}
