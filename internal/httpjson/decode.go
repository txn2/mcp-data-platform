package httpjson

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Request body bounds for DecodeBody. A JSON body that names a record is a
// few kilobytes; MaxContentBody is for a route whose body carries a document
// (a knowledge page's markdown, a prompt), escaped for JSON.
const (
	MaxBody        int64 = 1 << 20
	MaxContentBody int64 = 4 << 20
)

// DecodeBody reads r's JSON body into dst, reading at most maxBytes of it, so
// a request cannot make the server buffer more than its route accepts. It is
// the one way a handler reads a JSON body; .semgrep/go-unbounded-json-body.yml
// refuses json.NewDecoder over a request body that is not bounded first.
func DecodeBody(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		return fmt.Errorf("decoding request body: %w", err)
	}
	return nil
}
