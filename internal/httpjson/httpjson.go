// Package httpjson holds the JSON and RFC 9457 Problem Details responders
// shared by the admin and portal REST decomposition seams, and the request-side
// JSON field types (see OptionalInt) that the REST routes and the MCP tools
// accepting the same field decode through.
//
// pkg/admin and pkg/portal each carry their own unexported copy of this
// responder, and the first seam extracted out of pkg/admin
// (internal/admin/settingsapi) copied it a third time. Every additional seam
// would have added another byte-identical copy, and — because swag resolves a
// schema per package — another duplicate `problemDetail` definition in the
// published OpenAPI document. The seams point here instead, so the seam
// family contributes exactly one responder and one schema.
//
// The parents' own copies are deliberately left in place: their ~500
// `@Failure ... {object} problemDetail` annotations resolve against the
// package-local type, and rewriting those is a documentation change rather
// than a decomposition one.
package httpjson

import (
	"encoding/json"
	"net/http"
)

// ProblemDetail is an RFC 9457 Problem Details response body. It mirrors the
// admin and portal packages' unexported equivalents field for field, so a
// seam's errors are indistinguishable on the wire from the routes that stayed
// behind in the parent.
type ProblemDetail struct {
	Type   string `json:"type" example:"about:blank"`
	Title  string `json:"title" example:"Not Found"`
	Status int    `json:"status" example:"404"`
	Detail string `json:"detail,omitempty" example:"resource not found"`
}

// StatusResponse is the generic acknowledgement body ("status": "ok") that
// admin and portal write routes return when there is nothing else to say. It
// mirrors the parents' unexported equivalents.
type StatusResponse struct {
	Status string `json:"status" example:"ok"`
}

// WriteJSON writes v as a JSON response with the given status. A Content-Type
// the caller already set is preserved, which is what lets WriteError layer its
// problem+json type on top of this.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes an RFC 9457 Problem Details error response. Title is
// derived from the status text; msg becomes the detail and must already be
// safe to return to the caller.
func WriteError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/problem+json")
	WriteJSON(w, status, ProblemDetail{
		Type:   "about:blank",
		Title:  http.StatusText(status),
		Status: status,
		Detail: msg,
	})
}
