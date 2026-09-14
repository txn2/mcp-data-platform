//go:build integration

package acceptance

import (
	"encoding/json"
	"strings"
	"testing"
)

// Issue #1741: the API reference the platform serves documented the 123
// /api/v1/admin/* routes and nothing else. The REST gateway routes a non-MCP
// client is told to use -- POST /api/v1/gateway/{connection}/invoke and
// /invoke-raw -- carried no swag annotations, so an integration developer
// wiring NiFi, Airflow or curl opened the only reference the deployment
// exposes, found no gateway route in it, and concluded there was no REST
// surface for them.
//
// What these criteria hold: both routes are in the served spec, under their
// own Gateway tag, declaring both auth schemes and the {connection} path
// parameter; the tag is navigable rather than an untitled bucket; and the
// `paginate` block the reference now documents on the invoke route actually
// walks pages there, because documenting a field the route dropped would have
// been the same defect in the other direction.
//
// Wire forms: the parameters this ticket touches on the REST route are
// `paginate` and the `body` it is documented beside. `paginate` is a typed
// object (items, cursor_param, page_param, page_step, max_pages), so the forms
// its schema admits are the object, absent, and explicit null -- all three are
// sent below as literal request bodies. `body` is untyped and admits both an
// object and a string of JSON (#1548); both are sent to the same upstream
// operation and asserted to produce the same result. The spec itself is read
// over plain HTTP, which admits one form.

// gatewaySpecPath is where the platform serves the generated OpenAPI document
// the reference renders. It is the same bytes internal/apidocs embeds.
const gatewaySpecPath = "/api/v1/admin/docs/doc.json"

// invokeRoute and invokeRawRoute are the two gateway data-plane paths as the
// spec addresses them, with the /api/v1 base path stripped the way @BasePath
// leaves them.
const (
	invokeRoute    = "/gateway/{connection}/invoke"
	invokeRawRoute = "/gateway/{connection}/invoke-raw"
)

// specPaths reads the served spec and returns its paths object.
func specPaths(t *testing.T, c *client) map[string]any {
	t.Helper()
	status, doc := c.rest("GET", gatewaySpecPath, nil)
	if status != 200 {
		t.Fatalf("GET %s = %d; want 200", gatewaySpecPath, status)
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		t.Fatalf("the served spec carries no paths object")
	}
	return paths
}

// operation returns one method's operation object from the served spec,
// failing when the route or the method is absent.
func operation(t *testing.T, paths map[string]any, route, method string) map[string]any {
	t.Helper()
	item, ok := paths[route].(map[string]any)
	if !ok {
		t.Fatalf("the served spec has no %s; the handler's annotations did not reach it", route)
	}
	op, ok := item[method].(map[string]any)
	if !ok {
		t.Fatalf("the served spec has %s but no %s on it", route, method)
	}
	return op
}

// TestIssue1741_TheServedSpecCarriesBothGatewayRoutes is the ticket's first
// sentence: a person reading the deployment's own reference finds the REST
// gateway in it.
func TestIssue1741_TheServedSpecCarriesBothGatewayRoutes(t *testing.T) {
	c := connect(t)
	paths := specPaths(t, c)

	for _, route := range []string{invokeRoute, invokeRawRoute} {
		op := operation(t, paths, route, "post")

		tags, _ := op["tags"].([]any)
		if len(tags) != 1 || tags[0] != "Gateway" {
			t.Errorf("%s tags = %v; want exactly [Gateway]", route, op["tags"])
		}

		// Both auth schemes the rest of the spec declares, so a reader learns
		// the route takes the credential they already hold.
		schemes := map[string]bool{}
		security, _ := op["security"].([]any)
		for _, entry := range security {
			m, _ := entry.(map[string]any)
			for name := range m {
				schemes[name] = true
			}
		}
		if !schemes["ApiKeyAuth"] || !schemes["BearerAuth"] {
			t.Errorf("%s security = %v; want both ApiKeyAuth and BearerAuth", route, op["security"])
		}

		// The {connection} path parameter, without which the route reads as
		// though the connection were optional.
		var hasConnection bool
		params, _ := op["parameters"].([]any)
		for _, p := range params {
			m, _ := p.(map[string]any)
			if m["in"] == "path" && m["name"] == "connection" && m["required"] == true {
				hasConnection = true
			}
		}
		if !hasConnection {
			t.Errorf("%s declares no required {connection} path parameter", route)
		}

		if summary, _ := op["summary"].(string); strings.TrimSpace(summary) == "" {
			t.Errorf("%s carries no summary", route)
		}
	}
}

// TestIssue1741_TheGatewayRoutesDocumentTheirPlatformStatusCodes holds the
// part a client actually routes on. The invoke route answers 200 with the
// upstream's status inside the body, so its platform-level codes are the ones
// that mean the gateway itself refused or failed.
func TestIssue1741_TheGatewayRoutesDocumentTheirPlatformStatusCodes(t *testing.T) {
	c := connect(t)
	paths := specPaths(t, c)
	op := operation(t, paths, invokeRoute, "post")

	responses, ok := op["responses"].(map[string]any)
	if !ok {
		t.Fatalf("%s declares no responses", invokeRoute)
	}
	// Every code the handler can actually emit on this route.
	for _, code := range []string{"200", "400", "401", "403", "404", "413", "415", "429", "500", "502", "504"} {
		if _, found := responses[code]; !found {
			t.Errorf("%s does not document a %s response, which the handler emits", invokeRoute, code)
		}
	}
}

// TestIssue1741_TheGatewayTagIsNavigable checks that the new tag is declared
// with a description and placed in a group. A tag that is used but not
// declared renders in the served reference as an untitled bucket outside both
// headings, which is exactly as unfindable as not being documented at all.
func TestIssue1741_TheGatewayTagIsNavigable(t *testing.T) {
	c := connect(t)
	status, doc := c.rest("GET", gatewaySpecPath, nil)
	if status != 200 {
		t.Fatalf("GET %s = %d; want 200", gatewaySpecPath, status)
	}

	var described bool
	tags, _ := doc["tags"].([]any)
	for _, entry := range tags {
		m, _ := entry.(map[string]any)
		if m["name"] != "Gateway" {
			continue
		}
		if desc, _ := m["description"].(string); strings.TrimSpace(desc) != "" {
			described = true
		}
	}
	if !described {
		t.Error("the Gateway tag is not declared with a description in the served spec")
	}

	var grouped bool
	groups, _ := doc["x-tagGroups"].([]any)
	for _, entry := range groups {
		m, _ := entry.(map[string]any)
		names, _ := m["tags"].([]any)
		for _, n := range names {
			if n == "Gateway" {
				grouped = true
			}
		}
	}
	if !grouped {
		t.Error("the Gateway tag is in no x-tagGroups group, so the reference renders it outside every heading")
	}
}

// TestIssue1741_PaginateWalksPagesOverREST is the criterion that keeps the
// documentation honest. The reference now shows `paginate` in the invoke
// route's request body; before this ticket the REST binding had no such field
// and buildInvokeArgs never forwarded one, so a REST caller's paginate block
// was accepted and silently dropped, returning one page.
//
// The upstream is the api-test fixture `make dev` registers, whose
// /v1/pagination/link collection is 100 items over 10 pages.
func TestIssue1741_PaginateWalksPagesOverREST(t *testing.T) {
	c := connect(t)
	status, out := c.rest("POST", "/api/v1/gateway/"+apiTestConnection+"/invoke", jsonBody(t, map[string]any{
		"method":   "GET",
		"path":     "/v1/pagination/link",
		"paginate": map[string]any{"items": "items"},
	}))
	if status != 200 {
		t.Fatalf("invoke with paginate = %d; want 200 (%v)", status, out)
	}
	if got := number(t, out, "pages_fetched"); got != 10 {
		t.Errorf("pages_fetched = %v; want 10 -- paginate did not reach the walk", got)
	}
	if got := number(t, out, "items_merged"); got != 100 {
		t.Errorf("items_merged = %v; want 100", got)
	}
	if got := out["stopped_by"]; got != "end" {
		t.Errorf("stopped_by = %v; want end", got)
	}
}

// TestIssue1741_TheOtherPaginateWireFormsBehaveAsDocumented sends the two
// remaining forms the schema admits. Absent and explicit null must both mean
// "do not walk": the signal is reported and not followed, which is the
// contract the walk was added beside rather than instead of.
func TestIssue1741_TheOtherPaginateWireFormsBehaveAsDocumented(t *testing.T) {
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"absent", map[string]any{"method": "GET", "path": "/v1/pagination/link"}},
		{"explicit null", map[string]any{"method": "GET", "path": "/v1/pagination/link", "paginate": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := connect(t)
			status, out := c.rest("POST", "/api/v1/gateway/"+apiTestConnection+"/invoke", jsonBody(t, tc.body))
			if status != 200 {
				t.Fatalf("invoke = %d; want 200 (%v)", status, out)
			}
			if _, walked := out["pages_fetched"]; walked {
				t.Fatalf("paginate %s must not walk: %v", tc.name, out)
			}
			pagination, _ := out["pagination"].(map[string]any)
			if pagination["has_more"] != true {
				t.Errorf("pagination = %v; want the signal reported", out["pagination"])
			}
		})
	}
}

// TestIssue1741_PaginateIsRefusedOnTheRawRoute holds the sentence the
// invoke-raw annotation makes: a walk merges JSON pages and a byte stream has
// nothing to merge, so the combination is refused rather than accepted and
// half-honored.
func TestIssue1741_PaginateIsRefusedOnTheRawRoute(t *testing.T) {
	c := connect(t)
	status, out := c.rest("POST", "/api/v1/gateway/"+apiTestConnection+"/invoke-raw", jsonBody(t, map[string]any{
		"method":   "GET",
		"path":     "/v1/pagination/link",
		"paginate": map[string]any{"items": "items"},
	}))
	if status != 400 {
		t.Fatalf("invoke-raw with paginate = %d; want 400 (%v)", status, out)
	}
	if msg, _ := out["error"].(string); !strings.Contains(strings.ToLower(msg), "paginate") {
		t.Errorf("refusal = %q; want it to name paginate as the reason", msg)
	}
}

// TestIssue1741_BothBodyWireFormsReachTheUpstream sends the `body` parameter
// in each form its schema admits -- an object, and a string of JSON -- to the
// same upstream operation, and asserts the same outcome for both. The REST
// route's body is untyped, and #1548 shipped green because every check sent
// one form while the client sent the other.
func TestIssue1741_BothBodyWireFormsReachTheUpstream(t *testing.T) {
	// The fixture's /v1/echo returns the request as it saw it, so what the
	// upstream received is readable rather than inferred. Comparing the two
	// forms against each other, rather than against a shape written here,
	// means the criterion cannot pass by asserting something both forms fail.
	echoed := make(map[string]string, 2)
	for _, tc := range []struct {
		name string
		body any
	}{
		{"object", map[string]any{"name": "acceptance"}},
		{"string of JSON", `{"name":"acceptance"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := connect(t)
			status, out := c.rest("POST", "/api/v1/gateway/"+apiTestConnection+"/invoke", jsonBody(t, map[string]any{
				"method":  "POST",
				"path":    "/v1/echo",
				"headers": map[string]string{"Content-Type": "application/json"},
				"body":    tc.body,
			}))
			if status != 200 {
				t.Fatalf("invoke = %d; want 200 (%v)", status, out)
			}
			if got := number(t, out, "status"); got != 200 {
				t.Fatalf("upstream status = %v; want 200 (%v)", got, out["body"])
			}
			raw, err := json.Marshal(out["body"])
			if err != nil {
				t.Fatalf("re-encoding the echoed body: %v", err)
			}
			rendered := strings.ToLower(string(raw))
			if !strings.Contains(rendered, "acceptance") {
				t.Fatalf("the echoed request does not carry the body that was sent: %s", rendered)
			}
			echoed[tc.name] = rendered
		})
	}
	if len(echoed) == 2 && echoed["object"] != echoed["string of JSON"] {
		t.Errorf("the two body wire forms reached the upstream differently:\n object: %s\n string: %s",
			echoed["object"], echoed["string of JSON"])
	}
}
