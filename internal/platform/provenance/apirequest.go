package provenance

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// maxRequestBytes bounds the rendered request one captured call holds. A
// request body is arbitrary size and the capture is stored on the asset row,
// so the bound is what keeps an asset's provenance a record of its calls
// rather than a second copy of what they carried. It is generous: a URL, a
// query string and a small JSON body are all far below it.
const maxRequestBytes = 4 << 10

// truncationMarker ends a request too large to record in full.
const truncationMarker = "\n[TRUNCATED]"

// APIOperationResolver rebuilds the request behind an api call that was
// addressed by operation id. The audit row holds the operation and the values
// the caller passed; the path template they were substituted into lives in the
// connection's API catalog, which only the toolkit serving that connection
// holds. The api gateway toolkit implements this (issue #1423).
type APIOperationResolver interface {
	ResolveOperationRequest(
		ctx context.Context, connection, operationID, spec string, pathParams map[string]string,
	) (method, path string, ok bool)
}

// ToolkitLister is the live toolkit registry. A capture asks it for the
// resolver above at the moment it renders a call, rather than holding a
// toolkit resolved at assembly: connections are added, reloaded and removed
// while the platform runs, and a capture must read the catalog as it stands.
type ToolkitLister interface {
	All() []registry.Toolkit
}

// Option configures a Capturer.
type Option func(*Capturer)

// WithToolkits gives the capturer the toolkit registry it resolves an api
// call's operation id against. Without it a call addressed by operation id
// records the operation and the values it passed, but not the path they
// resolve to.
func WithToolkits(toolkits ToolkitLister) Option {
	return func(c *Capturer) { c.toolkits = toolkits }
}

// describeAPI fills in what an api call requested, from the arguments the
// audit row kept: the operation it addressed, the path those arguments resolve
// to, the query string it sent, and its request body. Two calls to the same
// operation differ in exactly those arguments, so without them the rows are
// indistinguishable from each other (issue #1423).
func (c *Capturer) describeAPI(ctx context.Context, call *portal.ProvenanceCall, params map[string]any) {
	call.Method = stringParam(params, "method")
	call.Path = stringParam(params, "path")
	call.OperationID = stringParam(params, "operation_id")
	if call.Path == "" && call.OperationID != "" {
		if method, path := c.resolveOperation(ctx, call, params); path != "" {
			call.Method, call.Path = method, path
		}
	}
	call.Request = APIRequestText(call, params)
}

// resolveOperation asks the toolkit serving the call's connection what request
// an operation id and its path values addressed. It returns empty strings when
// no toolkit resolves it — the connection is gone, the catalog no longer
// carries the operation, or the values no longer fit its template — and the
// call then reads as the operation it named.
func (c *Capturer) resolveOperation(
	ctx context.Context, call *portal.ProvenanceCall, params map[string]any,
) (method, path string) {
	if c.toolkits == nil {
		return "", ""
	}
	spec := stringParam(params, "spec")
	values := stringMapParam(params, "path_params")
	for _, tk := range c.toolkits.All() {
		resolver, ok := tk.(APIOperationResolver)
		if !ok {
			continue
		}
		if m, p, resolved := resolver.ResolveOperationRequest(
			ctx, call.Connection, call.OperationID, spec, values,
		); resolved {
			return m, p
		}
	}
	return "", ""
}

// APIRequestText renders what an api call asked for, from the addressing it
// resolved to and the arguments it carried. It is exported for an export's
// record of the call it made itself, which is built outside a capture (there
// is no audit row for it yet) and has to read the same way as the calls
// captured around it (#1423).
func APIRequestText(call *portal.ProvenanceCall, params map[string]any) string {
	return boundRequest(requestText(call, params))
}

// requestText renders the request line and body of an api call.
//
// A call whose path is known reads as the request it made. One addressed by an
// operation the catalog no longer resolves reads as that operation and the
// values it passed, which still tells two calls to it apart. A call with
// neither reads as nothing here, and the card names it by tool and connection
// as it always has.
func requestText(call *portal.ProvenanceCall, params map[string]any) string {
	head := strings.TrimSpace(call.Method + " " + call.Path)
	if call.Path == "" {
		head = strings.TrimSpace(call.OperationID + " " + pairsText(params, "path_params"))
	}
	if head == "" {
		return ""
	}
	if q := queryText(params); q != "" {
		head += "?" + q
	}
	if body := bodyText(params["body"]); body != "" {
		return head + "\n" + body
	}
	return head
}

// queryText renders the query string a call sent, sorted by key so two
// captures of the same call read identically. A redacted value is rendered as
// the redaction it was stored as.
func queryText(params map[string]any) string {
	raw, ok := params["query_params"]
	if !ok {
		return ""
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return scalarText(raw)
	}
	q := url.Values{}
	for key, v := range values {
		if list, isList := v.([]any); isList {
			for _, item := range list {
				q.Add(key, scalarText(item))
			}
			continue
		}
		q.Set(key, scalarText(v))
	}
	return q.Encode()
}

// pairsText renders a map argument as "name=value" pairs in name order. It is
// how the values a call passed are shown when the request they addressed
// cannot be rebuilt.
func pairsText(params map[string]any, key string) string {
	raw, ok := params[key]
	if !ok {
		return ""
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return scalarText(raw)
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([]string, 0, len(names))
	for _, name := range names {
		pairs = append(pairs, name+"="+scalarText(values[name]))
	}
	return strings.Join(pairs, " ")
}

// bodyText renders a request body. A body the caller sent as text is kept as
// text — a redacted value arrives that way too, and reads as the redaction —
// and anything else is rendered as the JSON it was sent as.
func bodyText(body any) string {
	if body == nil {
		return ""
	}
	if s, ok := body.(string); ok {
		return s
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// scalarText renders one argument value as the text a reader sees. Strings are
// kept verbatim so a redaction and a URL both read as themselves; every other
// value takes the JSON form it arrived in, which for the numbers and booleans
// an audit row holds is the value as it was written. A value that will not
// encode renders as nothing rather than as a Go dump of its internals.
func scalarText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// stringMapParam reads an argument that holds a map of strings, which is how
// path_params arrives. A value of any other shape yields no values, so a
// redacted or malformed argument leaves the operation unresolved rather than
// resolving it against half its template.
func stringMapParam(params map[string]any, key string) map[string]string {
	values, ok := params[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(values))
	for name, v := range values {
		out[name] = scalarText(v)
	}
	return out
}

// boundRequest cuts a rendered request to maxRequestBytes on a rune boundary
// and marks that it was cut, so a large body costs the asset a bounded amount
// of storage and a reader is told the text is not the whole request.
func boundRequest(request string) string {
	if len(request) <= maxRequestBytes {
		return request
	}
	// Back off any bytes of a character the bound cut in half, so what is
	// stored is text rather than text plus a broken fragment.
	end := maxRequestBytes
	for end > 0 && !utf8.RuneStart(request[end]) {
		end--
	}
	return request[:end] + truncationMarker
}
