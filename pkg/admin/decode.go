package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxAdminBodyBytes caps the size of a decoded admin write request body. Admin
// payloads (persona definitions, connection configs, tool arguments) are small;
// this bound protects the decoder from an oversized or hostile body. Callers
// that stream large uploads (e.g. catalog spec files) manage their own limits.
const maxAdminBodyBytes = 1 << 20 // 1 MiB

// errInvalidBody is the generic RFC 9457 detail for a malformed JSON body that
// is not an unknown-field or over-size error. It matches the wording other
// admin handlers already return for decode failures.
const errInvalidBody = "invalid request body"

// decodeStrict decodes a required JSON request body into dst, rejecting unknown
// fields so a mis-named or schema-mismatched field surfaces as a 400 instead of
// being silently dropped. For authorization-defining resources (personas, tool
// access) a dropped field can turn a typo into a policy change, so strict
// decoding is the safe default for every admin write endpoint. The body is
// capped at maxAdminBodyBytes. The returned error's message is safe to hand to
// writeError as the problem+json detail (it names the offending field for the
// unknown-field case).
func decodeStrict(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeStrictLimit(w, r, dst, maxAdminBodyBytes)
}

// decodeStrictLimit is decodeStrict with a caller-supplied body cap. Endpoints
// that legitimately carry large inline payloads (e.g. an OpenAPI spec's
// `content` field, which the sibling multipart path allows up to 10 MiB) pass
// their own bound instead of the small maxAdminBodyBytes default.
func decodeStrictLimit(w http.ResponseWriter, r *http.Request, dst any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	// Reject trailing data after the first JSON value so a body like
	// `{...}{...}` is not silently accepted with only the first object read.
	if dec.More() {
		return errors.New(errInvalidBody)
	}
	return nil
}

// decodeStrictOptional behaves like decodeStrict but treats an empty body as a
// success (leaving dst at its zero value). Use it for endpoints whose body is
// optional (e.g. an OAuth start with an optional return_url) but which should
// still reject unknown fields when a body is present.
func decodeStrictOptional(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return nil // empty body: leave dst zero-valued
		}
		return decodeError(err)
	}
	if dec.More() {
		return errors.New(errInvalidBody)
	}
	return nil
}

// decodeError maps a json.Decoder error to a safe RFC 9457 detail string. The
// stdlib unknown-field error text ("json: unknown field \"tools\"") already
// quotes the field name, so it is surfaced verbatim (minus the "json: " prefix)
// to tell the caller exactly which field was rejected. Over-size and all other
// decode errors collapse to stable, non-leaky messages.
func decodeError(err error) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return errors.New("request body too large")
	}
	const unknownPrefix = "json: unknown field "
	if msg := err.Error(); strings.HasPrefix(msg, unknownPrefix) {
		return fmt.Errorf("unknown field %s", strings.TrimPrefix(msg, unknownPrefix))
	}
	return errors.New(errInvalidBody)
}
