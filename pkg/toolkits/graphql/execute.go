package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/apigwmetrics"
	"github.com/txn2/mcp-data-platform/internal/membudget"
	"github.com/txn2/mcp-data-platform/internal/upstreamauth"
	"github.com/txn2/mcp-data-platform/internal/useragent"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// contentTypeJSON is what a GraphQL request body is and what a
// conforming endpoint answers with.
const contentTypeJSON = "application/json"

// Error is one entry of a GraphQL response's errors array. A GraphQL
// endpoint reports a failure this way inside an HTTP 200, which is why
// the platform classifies the outcome from the body rather than from
// the status line.
type Error struct {
	// Message is the upstream's own text. It is passed through
	// unchanged: for most GraphQL upstreams it is the only diagnosis
	// the caller will get.
	Message string `json:"message"`
	// Path is the response path the error occurred at, when the
	// upstream reports one.
	Path []any `json:"path,omitempty"`
	// Locations are the document positions the upstream blames.
	Locations []ErrorLocation `json:"locations,omitempty"`
	// Extensions is the upstream's own error metadata (an error code,
	// a classification), passed through as sent.
	Extensions map[string]any `json:"extensions,omitempty"`
}

// ErrorLocation is a line and column in the submitted document.
type ErrorLocation struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// graphQLRequest is the JSON body a GraphQL request is sent as.
type graphQLRequest struct {
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables,omitempty"`
	OperationName string         `json:"operationName,omitempty"`
}

// graphQLResponse is the JSON body a GraphQL response arrives as. Data
// is kept raw so a partial result is preserved exactly as sent
// alongside the errors that cut it short.
type graphQLResponse struct {
	Data       json.RawMessage `json:"data,omitempty"`
	Errors     []Error         `json:"errors,omitempty"`
	Extensions map[string]any  `json:"extensions,omitempty"`
}

// execution is one round trip's outcome.
type execution struct {
	// status is the HTTP status line.
	status int
	// body is the raw response body, as read.
	body []byte
	// truncated reports the body being cut at the connection's read
	// cap.
	truncated bool
	// parsed is the decoded GraphQL response, nil when the body was not
	// JSON (an upstream answering an HTML error page, a proxy in the
	// way).
	parsed *graphQLResponse
	// userAgent is the User-Agent the request was sent with: the
	// connection's static header when it pins one, else the platform's
	// product string. A refusal that names it tells the operator which
	// knob to turn (#1679).
	userAgent string
}

// upstreamFailed reports an outcome that is a failure whatever the
// status line said: a non-2xx, or a 200 carrying a non-empty errors
// array. It is what the audit pipeline, the call catalog and the
// outbound metrics classify on, because a GraphQL endpoint reports
// almost every failure as an HTTP 200.
func (e *execution) upstreamFailed() bool {
	if e.status < http.StatusOK || e.status >= http.StatusMultipleChoices {
		return true
	}
	return e.parsed != nil && len(e.parsed.Errors) > 0
}

// execute posts one document to a connection's endpoint and reads the
// answer. The caller has already validated the document; this is the
// transport step alone.
//
// The outbound metric is recorded here rather than by a transport
// wrapper because its verdict is not on the status line: a GraphQL
// endpoint reports failure as a 200 carrying errors, and only a read
// body says which (#1678). Every send this kind makes, including the
// schema introspection, comes through here, so nothing is uncounted.
func (t *Toolkit) execute(ctx context.Context, c *conn, body graphQLRequest) (*execution, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("graphql: encoding request: %w", err)
	}
	req, err := t.newRequest(ctx, c, payload)
	if err != nil {
		return nil, err
	}
	t.mu.RLock()
	budget, metrics := t.memBudget, t.metrics
	t.mu.RUnlock()

	start := time.Now()
	resp, err := c.client.Do(req)
	if err != nil {
		apigwmetrics.Record(ctx, metrics, c.cfg.ConnectionName, apigwmetrics.Observation{Failed: true, Duration: time.Since(start)})
		return nil, fmt.Errorf("graphql: calling %s: %w", c.cfg.EndpointURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	out, err := t.readExecution(resp, req.Header, budget, c)
	if err != nil {
		apigwmetrics.Record(ctx, metrics, c.cfg.ConnectionName, apigwmetrics.Observation{Status: resp.StatusCode, Failed: true, Duration: time.Since(start)})
		return nil, err
	}
	apigwmetrics.Record(ctx, metrics, c.cfg.ConnectionName, apigwmetrics.Observation{Status: out.status, Failed: out.upstreamFailed(), Duration: time.Since(start)})
	return out, nil
}

// readExecution reads one response into an execution, within the
// connection's read cap and the platform's in-flight budget.
func (*Toolkit) readExecution(resp *http.Response, sent http.Header, budget *membudget.Budget, c *conn) (*execution, error) {
	readCap := readLimit(c.cfg.MaxResponseBytes)
	reserved, ok := reserveBodyBudget(budget, resp.ContentLength, readCap)
	if !ok {
		return nil, errors.New("graphql: the platform's in-flight response budget is exhausted; retry when concurrent calls have completed")
	}
	defer budget.Release(reserved)

	raw, truncated, err := readBody(resp.Body, readCap)
	if err != nil {
		return nil, err
	}
	out := &execution{status: resp.StatusCode, body: raw, truncated: truncated, userAgent: useragent.Effective(sent)}
	// A body cut at the read cap is not parseable JSON, and reporting a
	// decode failure for it would blame the payload for the cap.
	if !truncated {
		var parsed graphQLResponse
		if json.Unmarshal(raw, &parsed) == nil {
			out.parsed = &parsed
		}
	}
	return out, nil
}

// newRequest builds the outbound POST with the connection's static
// headers and its credential applied. Static headers go on first so the
// authenticator has the last word on the header it owns; the header
// names a model may claim are already refused by the shared seam's
// validation, and the model never reaches this function's inputs at all.
func (*Toolkit) newRequest(ctx context.Context, c *conn, payload []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.EndpointURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("graphql: building request: %w", err)
	}
	req.Header.Set("Content-Type", contentTypeJSON)
	req.Header.Set("Accept", contentTypeJSON)
	for name, value := range c.cfg.StaticHeaders {
		req.Header.Set(name, value)
	}
	if c.cfg.IdentityPassthrough {
		return req, applyIdentityPassthrough(ctx, req)
	}
	if err := c.auth.Apply(req); err != nil {
		return nil, fmt.Errorf("graphql: applying auth: %w", err)
	}
	return req, nil
}

// applyIdentityPassthrough forwards the acting caller's inbound bearer
// token as the outbound Authorization header, in place of this
// connection's shared credential. The token is the one that
// authenticated the MCP session, bridged onto the request context by
// the auth middleware and read here via mcpcontext (importing
// pkg/middleware would form a cycle). An absent token is a hard error:
// a passthrough connection must act as the calling user, so an
// anonymous call would be wrong rather than merely unauthenticated.
func applyIdentityPassthrough(ctx context.Context, req *http.Request) error {
	token := mcpcontext.GetAuthToken(ctx)
	if token == "" {
		return errors.New("graphql: identity passthrough requires an authenticated caller token, but none was present on the request")
	}
	req.Header.Set(upstreamauth.AuthorizationHeader, "Bearer "+token)
	return nil
}
