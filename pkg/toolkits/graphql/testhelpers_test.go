package graphql

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// upstream is a GraphQL endpoint under the test's control: it records
// what was sent and answers what the test tells it to. It stands in for
// an endpoint on the wire, never for the platform's own behavior — the
// acceptance suite is what runs this kind against a real GraphQL server.
type upstream struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []graphQLRequest
	headers  []http.Header

	// introspection is the body returned to the introspection query.
	// Empty answers a GraphQL error, which is what an endpoint with
	// introspection disabled does.
	introspection string
	// onIntrospection runs while the introspection query is in flight,
	// before it is answered: what another instance does during a read.
	onIntrospection func()
	// respond answers every other document. Nil answers an empty data
	// object.
	respond func(req graphQLRequest, callNo int) (status int, body string)
}

// newUpstream starts a fake endpoint and stops it when the test ends.
func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{}
	u.server = httptest.NewServer(http.HandlerFunc(u.serve))
	t.Cleanup(u.server.Close)
	return u
}

func (u *upstream) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req graphQLRequest
	_ = json.Unmarshal(raw, &req)

	u.mu.Lock()
	u.requests = append(u.requests, req)
	u.headers = append(u.headers, r.Header.Clone())
	callNo := len(u.requests)
	respond := u.respond
	introspection := u.introspection
	onIntrospection := u.onIntrospection
	u.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if strings.Contains(req.Query, "__schema") {
		if onIntrospection != nil {
			onIntrospection()
		}
		if introspection == "" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"errors":[{"message":"GraphQL introspection is not allowed"}]}`))
			return
		}
		_, _ = w.Write([]byte(introspection))
		return
	}
	if respond == nil {
		_, _ = w.Write([]byte(`{"data":{}}`))
		return
	}
	status, body := respond(req, callNo)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// calls returns the documents the endpoint was sent.
func (u *upstream) calls() []graphQLRequest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]graphQLRequest(nil), u.requests...)
}

// lastHeaders returns the headers of the most recent request.
func (u *upstream) lastHeaders() http.Header {
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.headers) == 0 {
		return nil
	}
	return u.headers[len(u.headers)-1]
}

// answer builds a respond function that always returns one body.
func answer(body string) func(graphQLRequest, int) (int, string) {
	return func(graphQLRequest, int) (int, string) { return http.StatusOK, body }
}

// newToolkit builds a single-connection toolkit against an endpoint,
// applying any config overrides, and installs the named fixture schema
// through the upload path so a test that is not about introspection does
// not have to stand up an introspection result.
func newToolkit(t *testing.T, u *upstream, fixture string, overrides map[string]any) *Toolkit {
	t.Helper()
	cfg := map[string]any{"endpoint_url": u.server.URL}
	maps.Copy(cfg, overrides)
	parsed, err := ParseConfig(cfg)
	if err != nil {
		t.Fatalf("parsing config: %v", err)
	}
	parsed.ConnectionName = "gql"
	tk := NewMulti(MultiConfig{DefaultName: "gql", Instances: map[string]Config{"gql": parsed}})
	if fixture != "" {
		if err := tk.SetSchema(context.Background(), "gql", fixtureSDL(t, fixture)); err != nil {
			t.Fatalf("installing the fixture schema: %v", err)
		}
	}
	return tk
}

// fixtureSDL reads a schema fixture shared with the schema seam's own
// tests, so both suites reason about the same two schemas.
func fixtureSDL(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../../internal/gqlschema/testdata/" + name + ".graphql") //nolint:gosec // a fixture path this test builds
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return raw
}

// callQuery runs graphql_query and returns the parsed output, failing
// the test when the tool answered with an error envelope instead.
func callQuery(t *testing.T, tk *Toolkit, in QueryInput) *QueryOutput {
	t.Helper()
	res, _, err := tk.handleQuery(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	if msg := errorMessage(res); msg != "" {
		t.Fatalf("graphql_query refused the call: %s", msg)
	}
	var out QueryOutput
	decodeResult(t, res, &out)
	return &out
}

// refuseQuery runs graphql_query expecting a refusal, and returns its
// message.
func refuseQuery(t *testing.T, tk *Toolkit, in QueryInput) string {
	t.Helper()
	res, _, err := tk.handleQuery(context.Background(), nil, in)
	if err != nil {
		t.Fatalf("handleQuery: %v", err)
	}
	msg := errorMessage(res)
	if msg == "" {
		t.Fatalf("the call was not refused")
	}
	return msg
}

// errorMessage returns the message of a tool-error envelope, or "" when
// the result is not one.
func errorMessage(res *mcp.CallToolResult) string {
	var envelope struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(resultText(res)), &envelope); err != nil {
		return ""
	}
	return envelope.Error
}

// decodeResult parses a tool result's JSON text into dst.
func decodeResult(t *testing.T, res *mcp.CallToolResult, dst any) {
	t.Helper()
	if err := json.Unmarshal([]byte(resultText(res)), dst); err != nil {
		t.Fatalf("decoding the tool result: %v\n%s", err, resultText(res))
	}
}

// resultText returns the text content of a tool result.
func resultText(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}
	return text.Text
}

// jsonRaw marshals a value into the raw form a tool argument arrives as.
func jsonRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling: %v", err)
	}
	return raw
}
