//go:build integration

package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// issue1647JSONReader encodes a request body for the admin REST routes. The
// suite's restJSON returns only the status, and these criteria read the
// refusal text, so the body is built here and handed to rest directly.
func issue1647JSONReader(t *testing.T, v any) io.Reader {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return bytes.NewReader(raw)
}

// Issue #1647: the API gateway's upstream authentication and transport policy
// moved into internal/upstreamauth so a second HTTP-based connection kind
// reuses them instead of copying them. Nothing an operator or a model sees may
// change, which is what this suite executes: for every auth mode, what the
// platform actually puts on the wire, read back off the api-test fixture's own
// echo of the request it received.
//
// The fixture (dev/docker-compose.yml, api-test) is the upstream rather than a
// stand-in: /v1/echo returns the method, path, query and headers of the
// request as it arrived, so a credential the platform attached is observed
// where it lands rather than where it is built. Credential VALUES on
// Authorization and X-API-Key come back "[redacted]" — the fixture's own
// policy — so those modes assert the header's presence and the upstream's
// verdict, and the custom-header mode, whose name the fixture does not treat
// as a credential, asserts the value too.
//
// Wire forms: api_invoke_endpoint's `body` is untyped and admits an object and
// a string of JSON. Both are sent as literal tools/call params and asserted to
// reach the upstream identically.

const (
	issue1647Tool        = "api_invoke_endpoint"
	issue1647FixtureURL  = "http://localhost:9282"
	issue1647FixtureKey  = "apitest-dev-key-2024"
	issue1647CustomKey   = "X-Acc-1647-Key"
	issue1647StaticName  = "X-Acc-1647-Static"
	issue1647StaticValue = "pinned-by-the-operator"
	issue1647Purpose     = "Acceptance for #1647: the shared upstream auth and transport seam puts the same bytes on the wire."
)

// issue1647Connect registers one catalog-less connection to the api-test
// fixture with the given auth settings and returns its name. Every connection
// is removed when the test ends.
func issue1647Connect(t *testing.T, c *client, label string, auth map[string]any) string {
	t.Helper()
	name := fmt.Sprintf("acc-1647-%s-%d", label, time.Now().UnixNano())
	cfg := map[string]any{
		"base_url":        issue1647FixtureURL,
		"connection_name": name,
		"connect_timeout": "5s",
		"call_timeout":    "10s",
		"trust_level":     "untrusted",
	}
	for k, v := range auth {
		cfg[k] = v
	}
	status := c.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config":      cfg,
		"description": "Acceptance 1647: " + label,
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register %s connection: HTTP %d", label, status)
	}
	t.Cleanup(func() {
		c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
	})
	return name
}

// issue1647Echo calls the fixture's echo endpoint through the connection and
// returns the request as the fixture saw it.
func issue1647Echo(t *testing.T, c *client, connection string, extra map[string]any) map[string]any {
	t.Helper()
	args := map[string]any{
		"connection": connection,
		"method":     http.MethodGet,
		"path":       "/v1/echo",
		"purpose":    issue1647Purpose,
	}
	for k, v := range extra {
		args[k] = v
	}
	out := c.call(issue1647Tool, args)
	if got := number(t, out, "status"); got != http.StatusOK {
		t.Fatalf("%s through %s: upstream status %v, body %v", issue1647Tool, connection, got, out["body"])
	}
	body, ok := out["body"].(map[string]any)
	if !ok {
		t.Fatalf("echo body is not an object: %v", out["body"])
	}
	return body
}

// issue1647Header reads one header out of the fixture's echo. The fixture
// reports every header as a list of values.
func issue1647Header(body map[string]any, name string) []string {
	headers, _ := body["headers"].(map[string]any)
	raw, ok := headers[name].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

// TestIssue1647_AuthModesPutTheSameBytesOnTheWire is the criterion: for each
// auth mode the fixture can observe, the credential arrives where that mode
// says it should and nowhere else.
func TestIssue1647_AuthModesPutTheSameBytesOnTheWire(t *testing.T) {
	c := connect(t)

	t.Run("none attaches no credential", func(t *testing.T) {
		conn := issue1647Connect(t, c, "none", map[string]any{"auth_mode": "none"})
		body := issue1647Echo(t, c, conn, nil)
		if got := issue1647Header(body, "Authorization"); got != nil {
			t.Errorf("auth_mode=none sent an Authorization header: %v", got)
		}
		if got := issue1647Header(body, "X-Api-Key"); got != nil {
			t.Errorf("auth_mode=none sent an X-API-Key header: %v", got)
		}
	})

	t.Run("api_key in a custom header carries name and value", func(t *testing.T) {
		conn := issue1647Connect(t, c, "apikey-header", map[string]any{
			"auth_mode":         "api_key",
			"credential":        issue1647FixtureKey,
			"api_key_placement": "header",
			"api_key_header":    issue1647CustomKey,
		})
		body := issue1647Echo(t, c, conn, nil)
		got := issue1647Header(body, issue1647CustomKey)
		if len(got) != 1 || got[0] != issue1647FixtureKey {
			t.Errorf("%s = %v; want the configured credential", issue1647CustomKey, got)
		}
	})

	t.Run("api_key in the query carries name and value", func(t *testing.T) {
		conn := issue1647Connect(t, c, "apikey-query", map[string]any{
			"auth_mode":         "api_key",
			"credential":        issue1647FixtureKey,
			"api_key_placement": "query",
			"api_key_param":     "acc_1647_key",
		})
		body := issue1647Echo(t, c, conn, nil)
		query, _ := body["query"].(map[string]any)
		values, _ := query["acc_1647_key"].([]any)
		if len(values) != 1 || values[0] != issue1647FixtureKey {
			t.Errorf("query acc_1647_key = %v; want the configured credential", values)
		}
		if got := issue1647Header(body, issue1647CustomKey); got != nil {
			t.Errorf("query placement also sent a header: %v", got)
		}
	})

	t.Run("api_key in the default header authenticates the caller", func(t *testing.T) {
		conn := issue1647Connect(t, c, "apikey-default", map[string]any{
			"auth_mode":  "api_key",
			"credential": issue1647FixtureKey,
		})
		out := c.call(issue1647Tool, map[string]any{
			"connection": conn, "method": http.MethodGet, "path": "/v1/whoami",
			"purpose": issue1647Purpose,
		})
		body, _ := out["body"].(map[string]any)
		if body["auth_type"] != "apikey" || body["key_name"] != "dev-fixture" {
			t.Errorf("whoami = %v; want the fixture to recognize the api key", body)
		}
	})

	t.Run("basic attaches an Authorization header", func(t *testing.T) {
		conn := issue1647Connect(t, c, "basic", map[string]any{
			"auth_mode": "basic",
			"username":  "alice",
			"password":  "s3cret",
		})
		body := issue1647Echo(t, c, conn, nil)
		if got := issue1647Header(body, "Authorization"); len(got) != 1 {
			t.Errorf("auth_mode=basic sent Authorization %v; want exactly one value", got)
		}
	})

	// The fixture refuses a bearer token it does not know, and answers the
	// same path 200 with no credential at all. The contrast is the proof the
	// header was attached: its value comes back redacted, so presence is
	// asserted by the upstream's verdict rather than by reading it.
	t.Run("bearer attaches the token the upstream then judges", func(t *testing.T) {
		conn := issue1647Connect(t, c, "bearer", map[string]any{
			"auth_mode":  "bearer",
			"credential": "acc-1647-token-the-fixture-does-not-know",
		})
		out := c.call(issue1647Tool, map[string]any{
			"connection": conn, "method": http.MethodGet, "path": "/v1/echo",
			"purpose": issue1647Purpose,
		})
		if got := number(t, out, "status"); got != http.StatusUnauthorized {
			t.Fatalf("bearer call status = %v; want 401 from the fixture, which means the header arrived", got)
		}
		body, _ := out["body"].(map[string]any)
		if !strings.Contains(fmt.Sprint(body), "invalid credential") {
			t.Errorf("fixture refusal = %v; want its invalid-credential answer", body)
		}
	})
}

// TestIssue1647_OperatorHeadersAndTheirReservations covers the other half of
// the policy the seam owns: headers the operator pins, and the model's
// inability to reach any header a credential or the operator already claims.
func TestIssue1647_OperatorHeadersAndTheirReservations(t *testing.T) {
	c := connect(t)
	conn := issue1647Connect(t, c, "static", map[string]any{
		"auth_mode":         "api_key",
		"credential":        issue1647FixtureKey,
		"api_key_placement": "header",
		"api_key_header":    issue1647CustomKey,
		"static_headers":    map[string]any{issue1647StaticName: issue1647StaticValue},
	})

	t.Run("a pinned header reaches the upstream", func(t *testing.T) {
		body := issue1647Echo(t, c, conn, nil)
		got := issue1647Header(body, issue1647StaticName)
		if len(got) != 1 || got[0] != issue1647StaticValue {
			t.Errorf("%s = %v; want the operator's value", issue1647StaticName, got)
		}
	})

	t.Run("a model header the connection does not claim is forwarded", func(t *testing.T) {
		body := issue1647Echo(t, c, conn, map[string]any{
			"headers": map[string]any{"X-Acc-1647-Free": "from-the-model"},
		})
		got := issue1647Header(body, "X-Acc-1647-Free")
		if len(got) != 1 || got[0] != "from-the-model" {
			t.Errorf("X-Acc-1647-Free = %v; want the model's value", got)
		}
	})

	reserved := []struct {
		name    string
		header  string
		wantMsg string
	}{
		{"Authorization", "Authorization", "Authorization header is reserved"},
		{"the auth mode's own header", strings.ToLower(issue1647CustomKey), "reserved by this connection's auth_mode"},
		{"a pinned header", strings.ToLower(issue1647StaticName), "reserved by this connection's static_headers"},
	}
	for _, tc := range reserved {
		t.Run("the model may not set "+tc.name, func(t *testing.T) {
			res, text, err := c.callRaw(issue1647Tool, map[string]any{
				"connection": conn, "method": http.MethodGet, "path": "/v1/echo",
				"headers": map[string]any{tc.header: "spoofed"},
				"purpose": issue1647Purpose,
			})
			if err != nil {
				t.Fatalf("transport error: %v", err)
			}
			if !res.IsError {
				t.Fatalf("setting %s was allowed: %s", tc.header, text)
			}
			if !strings.Contains(text, tc.wantMsg) {
				t.Errorf("refusal = %q; want it to name %q", text, tc.wantMsg)
			}
		})
	}
}

// TestIssue1647_BodyReachesTheUpstreamInEveryWireForm sends the one untyped
// parameter this tool takes in both forms its schema admits, as literal
// tools/call params, and asserts the upstream received the same document.
func TestIssue1647_BodyReachesTheUpstreamInEveryWireForm(t *testing.T) {
	c := connect(t)
	conn := issue1647Connect(t, c, "body", map[string]any{
		"auth_mode":  "api_key",
		"credential": issue1647FixtureKey,
	})

	forms := []struct {
		name string
		body any
	}{
		{"object", map[string]any{"issue": float64(1647), "shape": "object"}},
		{"string of JSON", `{"issue":1647,"shape":"object"}`},
	}
	for _, form := range forms {
		t.Run(form.name, func(t *testing.T) {
			out := c.call(issue1647Tool, map[string]any{
				"connection": conn, "method": http.MethodPost, "path": "/v1/echo",
				"body":    form.body,
				"purpose": issue1647Purpose,
			})
			if got := number(t, out, "status"); got != http.StatusOK {
				t.Fatalf("status = %v, body %v", got, out["body"])
			}
			echoed, _ := out["body"].(map[string]any)
			received, _ := echoed["body"].(map[string]any)
			if received["issue"] != float64(1647) || received["shape"] != "object" {
				t.Errorf("the upstream received %v; want the document the call carried", received)
			}
		})
	}
}

// TestIssue1647_ConfigRefusalsKeepTheToolkitsVoice is the operator-facing half
// of the move: the auth and transport rules now live behind a shared seam, and
// a refused connection save must still name the surface the operator
// configured rather than the seam behind it.
func TestIssue1647_ConfigRefusalsKeepTheToolkitsVoice(t *testing.T) {
	c := connect(t)

	cases := []struct {
		name    string
		cfg     map[string]any
		wantMsg string
	}{
		{
			name:    "bearer without a credential",
			cfg:     map[string]any{"auth_mode": "bearer"},
			wantMsg: `apigateway: credential is required when auth_mode is "bearer"`,
		},
		{
			name:    "api_key in the query without a parameter name",
			cfg:     map[string]any{"auth_mode": "api_key", "credential": "k", "api_key_placement": "query"},
			wantMsg: `apigateway: api_key_param is required when api_key_placement is "query"`,
		},
		{
			name:    "basic with a colon in the userid",
			cfg:     map[string]any{"auth_mode": "basic", "username": "a:b"},
			wantMsg: "apigateway: username must not contain",
		},
		{
			name:    "a pinned header that would fight the auth layer",
			cfg:     map[string]any{"auth_mode": "none", "static_headers": map[string]any{"Authorization": "Bearer x"}},
			wantMsg: "apigateway: static_headers must not set Authorization",
		},
		{
			name:    "an unusable timeout",
			cfg:     map[string]any{"auth_mode": "none", "call_timeout": "0s"},
			wantMsg: "apigateway: call_timeout must be positive",
		},
		{
			name:    "mtls without the keypair that is its credential",
			cfg:     map[string]any{"auth_mode": "mtls"},
			wantMsg: "apigateway: mtls_client_cert_pem and mtls_client_key_pem are required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name := fmt.Sprintf("acc-1647-bad-%d", time.Now().UnixNano())
			cfg := map[string]any{"base_url": issue1647FixtureURL, "connection_name": name}
			for k, v := range tc.cfg {
				cfg[k] = v
			}
			status, body := c.rest(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name,
				issue1647JSONReader(t, map[string]any{"config": cfg, "description": "Acceptance 1647: refused"}))
			if status == http.StatusCreated || status == http.StatusOK {
				c.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody)
				t.Fatalf("an invalid connection was accepted: HTTP %d", status)
			}
			if !strings.Contains(fmt.Sprint(body), tc.wantMsg) {
				t.Errorf("refusal body = %v; want it to carry %q", body, tc.wantMsg)
			}
		})
	}
}
