package secretref

import (
	"encoding/json"
	"net/url"
	"strings"
)

// Request is the parts of an outbound request a placeholder may sit in.
// ContentType is the type the body will be sent as, which says how a value
// written into a string body is escaped.
type Request struct {
	Path        string
	Headers     map[string]string
	Query       map[string]any
	Body        any
	ContentType string
}

// FillRequest returns req with every placeholder in its path, headers,
// query and body filled through lookup. req itself is not changed, so what
// a caller recorded keeps its placeholders.
func FillRequest(req Request, lookup Lookup) (Request, error) {
	out := req
	var err error
	if out.Path, err = FillPath(req.Path, lookup); err != nil {
		return req, err
	}
	if out.Headers, err = FillStrings(req.Headers, lookup); err != nil {
		return req, err
	}
	if req.Query != nil {
		q, err := FillValue(req.Query, lookup)
		if err != nil {
			return req, err
		}
		out.Query, _ = q.(map[string]any)
	}
	if out.Body, err = fillBody(req.Body, req.ContentType, lookup); err != nil {
		return req, err
	}
	return out, nil
}

// fillBody fills a body. An object is filled value by value and encoded
// afterwards; a body sent as a string is filled in place, with the value
// escaped for what the string is: JSON text, a form, or anything else as
// written.
func fillBody(body any, contentType string, lookup Lookup) (any, error) {
	s, ok := body.(string)
	if !ok {
		return FillValue(body, lookup)
	}
	escape := Raw
	switch {
	case json.Valid([]byte(s)):
		escape = JSONString
	case strings.Contains(strings.ToLower(contentType), "x-www-form-urlencoded"):
		escape = url.QueryEscape
	}
	return Fill(s, lookup, escape)
}

// Recording returns lookup with every value it answers recorded on r, so
// the response to the request it fills is redacted of them. A one-time code
// is not recorded (#2065): six to eight digits would rewrite ordinary numbers
// in a response, and the code is useless once its period passes. The seed it
// was computed from is recorded by the lookup that read it.
func (r *Redactor) Recording(lookup Lookup) Lookup {
	return func(name string) (string, error) {
		v, err := lookup(name)
		if _, code := IsTOTPName(name); err == nil && !code {
			r.Add(name, v)
		}
		return v, err
	}
}
