package eprun

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

// MaxResponseBytes bounds the body a handler answers with.
const MaxResponseBytes = 10 << 20

// The statuses a handler may answer with.
const (
	minStatus = 200
	maxStatus = 599
)

const (
	headerContentType = "Content-Type"
	mediaJSON         = "application/json"
)

// tokenChars are the characters of an RFC 9110 token besides letters and
// digits, which a header's name is made of.
const tokenChars = "!#$%&'*+-.^_`|~" // #nosec G101 -- the RFC 9110 token punctuation, not a credential

// Caller is who sent a request to a signed_in endpoint.
type Caller struct {
	UserID   string
	Email    string
	AuthType string
}

// Request is one request to an endpoint, as the HTTP surface read it.
type Request struct {
	Method string
	Path   string
	Params map[string]string
	Query  map[string][]string
	Header http.Header
	Body   []byte
	// Caller is nil on a public endpoint.
	Caller *Caller
}

// Response is what the endpoint answers.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// credentialHeaders are the request headers a handler is not shown: they are
// the caller's credentials for the platform, and req.caller is who the
// platform found them to be.
var credentialHeaders = []string{"authorization", "cookie", "x-api-key", "proxy-authorization"}

// reservedHeaders are the response headers a handler may not set: the
// framing the server owns, the cookies of the platform's origin, and the
// cross-origin and content policy the endpoint surface answers with.
var reservedHeaders = []string{
	"content-length", "transfer-encoding", "connection", "set-cookie",
	"content-security-policy", "x-content-type-options", "x-frame-options",
}

// requestValue is req as handle(req) reads it: a frozen dict of method, path,
// params, query, headers, body, json, form and caller. A dict rather than a
// struct, so a test or a draft hands handle a request it built itself.
func requestValue(req Request) (starlark.Value, error) {
	params := starlark.NewDict(len(req.Params))
	for _, k := range sortedKeys(req.Params) {
		_ = params.SetKey(starlark.String(k), starlark.String(req.Params[k]))
	}
	headers := starlark.NewDict(len(req.Header))
	names := make([]string, 0, len(req.Header))
	for name := range req.Header {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		lower := strings.ToLower(name)
		if slices.Contains(credentialHeaders, lower) {
			continue
		}
		_ = headers.SetKey(starlark.String(lower), starlark.String(strings.Join(req.Header.Values(name), ", ")))
	}
	jsonValue, formValue, err := parsedBody(req)
	if err != nil {
		return nil, err
	}
	caller := starlark.Value(starlark.None)
	if req.Caller != nil {
		caller = stringDict(map[string]starlark.Value{
			"user_id": starlark.String(req.Caller.UserID), "email": starlark.String(req.Caller.Email),
			"auth_type": starlark.String(req.Caller.AuthType),
		})
	}
	v := stringDict(map[string]starlark.Value{
		"method": starlark.String(req.Method), "path": starlark.String(req.Path), "params": params,
		"query": firstValues(req.Query), "headers": headers, "body": starlark.Bytes(req.Body),
		"json": jsonValue, "form": formValue, "caller": caller,
	})
	v.Freeze()
	return v, nil
}

// stringDict is a dict of fields, in name order.
func stringDict(fields map[string]starlark.Value) *starlark.Dict {
	d := starlark.NewDict(len(fields))
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		_ = d.SetKey(starlark.String(name), fields[name])
	}
	return d
}

// parsedBody is the body read as JSON when it says it is JSON, and as a form
// when it is urlencoded; the other is None. A body that says it is JSON and
// is not is refused, before the script runs.
func parsedBody(req Request) (jsonValue, formValue starlark.Value, err error) {
	mediaType, _, _ := mime.ParseMediaType(req.Header.Get(headerContentType))
	switch {
	case len(req.Body) == 0:
		return starlark.None, starlark.None, nil
	case mediaType == mediaJSON || strings.HasSuffix(mediaType, "+json"):
		var decoded any
		if err := json.Unmarshal(req.Body, &decoded); err != nil {
			return nil, nil, fmt.Errorf("the request body says it is JSON and is not: %w", err)
		}
		v, err := starlarkconv.ToStarlark(decoded)
		if err != nil {
			return nil, nil, fmt.Errorf("the request body cannot be read as values: %w", err)
		}
		return v, starlark.None, nil
	case mediaType == "application/x-www-form-urlencoded":
		values, err := url.ParseQuery(string(req.Body))
		if err != nil {
			return nil, nil, fmt.Errorf("the request body says it is a form and is not: %w", err)
		}
		return starlark.None, firstValues(values), nil
	}
	return starlark.None, starlark.None, nil
}

// firstValues is each name's first value.
func firstValues(values map[string][]string) *starlark.Dict {
	d := starlark.NewDict(len(values))
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if len(values[name]) > 0 {
			_ = d.SetKey(starlark.String(name), starlark.String(values[name][0]))
		}
	}
	return d
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// responseFrom reads what handle returned: a dict of status, headers and one
// of json or body.
func responseFrom(v starlark.Value) (Response, error) {
	d, ok := v.(*starlark.Dict)
	if !ok {
		return Response{}, fmt.Errorf("handle(req) returned %s; it returns a dict of status, headers and json or body", typeName(v))
	}
	resp := Response{Status: http.StatusOK, Header: http.Header{}}
	var parts returned
	for _, item := range d.Items() {
		if err := parts.read(item, &resp); err != nil {
			return Response{}, err
		}
	}
	body, contentType, err := bodyFrom(parts.json, parts.body)
	if err != nil {
		return Response{}, err
	}
	if len(body) > MaxResponseBytes {
		return Response{}, fmt.Errorf("handle(req) returned a body of %d bytes, past the %d an endpoint answers with", len(body), MaxResponseBytes)
	}
	resp.Body = body
	if contentType != "" && resp.Header.Get(headerContentType) == "" {
		resp.Header.Set(headerContentType, contentType)
	}
	return resp, nil
}

// returned is the json and body a handler's dict carried.
type returned struct {
	json, body starlark.Value
}

// read takes one key of the dict a handler returned onto resp, or keeps its
// json or body for bodyFrom.
func (p *returned) read(item starlark.Tuple, resp *Response) error {
	key, ok := item[0].(starlark.String)
	if !ok {
		return fmt.Errorf("handle(req) returned a dict with the key %s; its keys are status, headers, json and body", item[0])
	}
	var err error
	switch string(key) {
	case "status":
		resp.Status, err = statusFrom(item[1])
	case "headers":
		err = headersFrom(item[1], resp.Header)
	case "json":
		p.json = item[1]
	case "body":
		p.body = item[1]
	default:
		err = fmt.Errorf("handle(req) returned the key %q; its keys are status, headers, json and body", key)
	}
	return err
}

func statusFrom(v starlark.Value) (int, error) {
	n, ok := v.(starlark.Int)
	if !ok {
		return 0, fmt.Errorf("status is %s; it is an int from 200 to 599", typeName(v))
	}
	status, exact := n.Int64()
	if !exact || status < minStatus || status > maxStatus {
		return 0, fmt.Errorf("status %s is not from 200 to 599", n)
	}
	return int(status), nil
}

func headersFrom(v starlark.Value, into http.Header) error {
	d, ok := v.(*starlark.Dict)
	if !ok {
		return fmt.Errorf("headers is %s; it is a dict of names to strings", typeName(v))
	}
	for _, item := range d.Items() {
		name, okName := item[0].(starlark.String)
		value, okValue := item[1].(starlark.String)
		if !okName || !okValue {
			return errors.New("headers maps names to strings")
		}
		lower := strings.ToLower(string(name))
		switch {
		case !validHeaderName(lower) || strings.ContainsAny(string(value), "\r\n"):
			return fmt.Errorf("header %q is not a valid header", name)
		case slices.Contains(reservedHeaders, lower) || strings.HasPrefix(lower, "access-control-"):
			return fmt.Errorf("header %q is the platform's to set", name)
		}
		into.Set(string(name), string(value))
	}
	return nil
}

// validHeaderName reports whether name, lower-cased, is an RFC 9110 token.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r < '0' || r > '9') && (r < 'a' || r > 'z') && !strings.ContainsRune(tokenChars, r) {
			return false
		}
	}
	return true
}

// bodyFrom renders json or body, and the content type it takes when the
// handler sets none.
func bodyFrom(jsonValue, bodyValue starlark.Value) (body []byte, contentType string, err error) {
	switch {
	case jsonValue != nil && bodyValue != nil:
		return nil, "", errors.New("handle(req) returned both json and body; it returns one")
	case jsonValue != nil:
		decoded, err := starlarkconv.FromStarlark(jsonValue)
		if err != nil {
			return nil, "", fmt.Errorf("json cannot be answered: %w", err)
		}
		out, err := json.Marshal(decoded)
		if err != nil {
			return nil, "", fmt.Errorf("json cannot be answered: %w", err)
		}
		return out, mediaJSON, nil
	case bodyValue != nil:
		switch b := bodyValue.(type) {
		case starlark.String:
			return []byte(b), "text/plain; charset=utf-8", nil
		case starlark.Bytes:
			return []byte(b), "application/octet-stream", nil
		}
		return nil, "", fmt.Errorf("body is %s; it is a string or bytes", typeName(bodyValue))
	}
	return nil, "", nil
}

func typeName(v starlark.Value) string {
	if v == nil {
		return "nothing"
	}
	return v.Type()
}

// errorResponse is a JSON {"error": msg} at status.
func errorResponse(status int, msg string) Response {
	body, _ := json.Marshal(map[string]string{"error": msg}) // a map of strings always marshals
	return Response{Status: status, Header: http.Header{headerContentType: {mediaJSON}}, Body: body}
}
