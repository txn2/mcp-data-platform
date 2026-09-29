package portal

import (
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/wirejson"
)

// problemDetail represents an RFC 9457 Problem Details response.
type problemDetail struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = wirejson.Encode(w, v)
}

// writeError writes a JSON error response using RFC 9457 Problem Details.
func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = wirejson.Encode(w, problemDetail{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: msg,
	})
}
