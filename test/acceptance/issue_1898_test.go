//go:build integration

package acceptance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Issue #1898: authentication failures, configuration, connection state and
// the platform's domain operations are signals rather than log lines.
//
// Wire forms: apply_knowledge's `insight_ids` is an array of strings and
// `action` a string, the one form each admits; memory_capture's `type`,
// `content`, `category` and `confidence` are strings. The expired-token
// criterion sends no tool parameters: the credential is refused at the HTTP
// gate before any is read. The connection is written over the admin REST
// route as an object, the one form that route takes.

const (
	// issue1898Client is the dev realm client whose access tokens expire one
	// second after issue and carry the portal audience the platform checks
	// (dev/keycloak-realm.json).
	issue1898Client = "mcp-acceptance-short-lived"
	issue1898Secret = "acceptance-short-lived-secret"
	issue1898Probe  = 30 * time.Second
)

// issue1898ExpiredToken signs the dev analyst in through the short-lived
// client and returns the access token once its exp has passed: a real token
// the identity provider signed, expired by its own clock.
func issue1898ExpiredToken(t *testing.T) string {
	t.Helper()
	form := url.Values{
		"grant_type": {"password"}, "client_id": {issue1898Client}, "client_secret": {issue1898Secret},
		"username": {issue1759AnalystEmail}, "password": {issue1759AnalystPass}, "scope": {"openid"},
	}
	endpoint := issue1759KeycloakBase() + "/realms/" + issue1759Realm + "/protocol/openid-connect/token"
	res, err := http.Post(endpoint, "application/x-www-form-urlencoded", strings.NewReader(form.Encode())) // #nosec G704 -- the dev stack's identity provider
	if err != nil {
		t.Fatalf("signing in through %s: %v (is the dev stack up? make dev)", issue1898Client, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("token endpoint answered %d: %s (the realm predates #1898's client? `make dev-down` re-imports it)", res.StatusCode, body)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		t.Fatalf("token response: %s", body)
	}
	parts := strings.Split(tok.AccessToken, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("token payload: %v", err)
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	_ = json.Unmarshal(payload, &claims)
	// The token's own lifetime, one second; nothing here is a guessed delay.
	if wait := time.Until(time.Unix(claims.Exp, 0).Add(time.Second)); wait > 0 {
		time.Sleep(wait)
	}
	return tok.AccessToken
}

// issue1898Count sums one series over every replica's scrape.
func issue1898Count(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	return metricSeries(scrapeRaw(t), name, labels)
}

// TestIssue1898_AnExpiredTokenIsCountedAsExpired: a tool call made with an
// expired token through the real client increments
// auth_attempts_total{method="oidc",result="failure",reason="expired"}.
func TestIssue1898_AnExpiredTokenIsCountedAsExpired(t *testing.T) {
	labels := map[string]string{"method": "oidc", "result": "failure", "reason": "expired"}
	before := issue1898Count(t, "auth_attempts_total", labels)

	token := issue1898ExpiredToken(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	httpClient := &http.Client{Transport: authRoundTripper{key: token, base: http.DefaultTransport}}
	mc := mcp.NewClient(&mcp.Implementation{Name: "acceptance-1898", Version: "1.0.0"}, nil)
	session, err := mc.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: baseURL(), HTTPClient: httpClient}, nil)
	if err == nil {
		_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "platform_info", Arguments: map[string]any{}})
		_ = session.Close()
	}
	if err == nil {
		t.Fatal("a tool call with an expired token was answered")
	}

	if after := issue1898Count(t, "auth_attempts_total", labels); after <= before {
		t.Errorf("auth_attempts_total{method=oidc,result=failure,reason=expired} = %v, was %v", after, before)
	}
}

// TestIssue1898_APersonaNamingAnUnregisteredToolIsWarnedAtBoot: the dev
// config's acceptance-stale-grant persona names trino_retired_tool, which no
// toolkit registers, and every replica starts with
// config_validation_warnings_total{code="persona_tool_unregistered"} at 1.
func TestIssue1898_APersonaNamingAnUnregisteredToolIsWarnedAtBoot(t *testing.T) {
	for _, target := range metricsURLs() {
		got := metricSeries(scrapeOne(t, target), "config_validation_warnings_total", map[string]string{"code": "persona_tool_unregistered"})
		if got != 1 {
			t.Errorf("%s: config_validation_warnings_total{code=persona_tool_unregistered} = %v, want 1", target, got)
		}
	}
}

// TestIssue1898_AConnectionWhoseUpstreamIsDownIsUnreachable: an api
// connection whose base URL nothing answers reads as configured until a call
// uses it, and is reported under
// mcp_platform_connections{kind="api",state="unreachable"} once one has. The
// state is the outcome of real calls: the platform sends nothing to an
// upstream to find it out.
func TestIssue1898_AConnectionWhoseUpstreamIsDownIsUnreachable(t *testing.T) {
	admin := connect(t)
	configured := map[string]string{"kind": "api", "state": "configured"}
	unreachable := map[string]string{"kind": "api", "state": "unreachable"}
	configuredBefore := issue1898Count(t, "mcp_platform_connections", configured)
	unreachableBefore := issue1898Count(t, "mcp_platform_connections", unreachable)

	name := fmt.Sprintf("acc-1898-down-%d", time.Now().UnixNano())
	status := admin.restJSON(http.MethodPut, "/api/v1/admin/connection-instances/api/"+name, map[string]any{
		"config": map[string]any{
			"base_url": "http://127.0.0.1:1", "connection_name": name, "auth_mode": "none",
			"connect_timeout": "1s", "call_timeout": "2s", "trust_level": "untrusted",
		},
		"description": "Acceptance 1898: an upstream nothing answers",
	})
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("register connection: HTTP %d", status)
	}
	t.Cleanup(func() { admin.rest(http.MethodDelete, "/api/v1/admin/connection-instances/api/"+name, http.NoBody) })

	// Every replica learns of the connection over the reload bus; until a
	// call uses it, it is configured and nothing else.
	deadline := time.Now().Add(issue1898Probe)
	for issue1898Count(t, "mcp_platform_connections", configured) <= configuredBefore {
		if time.Now().After(deadline) {
			t.Fatalf("the new connection was not reported as configured within %s", issue1898Probe)
		}
		time.Sleep(250 * time.Millisecond)
	}
	if got := issue1898Count(t, "mcp_platform_connections", unreachable); got != unreachableBefore {
		t.Fatalf("the connection read as unreachable before any call used it (%v -> %v)", unreachableBefore, got)
	}

	res, text, err := admin.callRaw("api_invoke_endpoint", map[string]any{
		"connection": name,
		"method":     "GET",
		"path":       "/",
		"purpose":    "Acceptance 1898: a call through a connection whose upstream is down.",
	})
	if err != nil {
		t.Fatalf("api_invoke_endpoint: %v", err)
	}
	if !res.IsError {
		t.Fatalf("a call to an upstream nothing answers succeeded: %s", text)
	}
	if got := issue1898Count(t, "mcp_platform_connections", unreachable); got <= unreachableBefore {
		t.Errorf("mcp_platform_connections{kind=api,state=unreachable} stayed at %v after the call", got)
	}
}

// TestIssue1898_ConfigInfoCarriesNoAddress: /metrics shows
// mcp_platform_config_info, and no label value on it contains "://" or "@".
func TestIssue1898_ConfigInfoCarriesNoAddress(t *testing.T) {
	for _, target := range metricsURLs() {
		var line string
		for _, l := range strings.Split(scrapeOne(t, target), "\n") {
			if strings.HasPrefix(l, "mcp_platform_config_info{") {
				line = l
			}
		}
		if line == "" {
			t.Fatalf("%s: no mcp_platform_config_info series", target)
		}
		labels := line[strings.Index(line, "{")+1 : strings.LastIndex(line, "}")]
		for _, pair := range strings.Split(labels, `",`) {
			_, value, _ := strings.Cut(pair, `="`)
			if strings.Contains(value, "://") || strings.Contains(value, "@") {
				t.Errorf("%s: label %q reads as an address", target, pair)
			}
		}
		for _, want := range []string{`auth_methods="`, `toolkit_kinds="`, `tracing="`} {
			if !strings.Contains(line, want) {
				t.Errorf("%s: config info %q has no %s", target, line, want)
			}
		}
	}
}

// TestIssue1898_ApplyKnowledgeIsCountedAndTraced: apply_knowledge through the
// real client increments domain_operations_total{operation="knowledge.apply"},
// records the review under knowledge_changes_total, and produces a
// knowledge.apply span under the call's tools/call span.
func TestIssue1898_ApplyKnowledgeIsCountedAndTraced(t *testing.T) {
	c := connect(t)
	opLabels := map[string]string{"operation": "knowledge.apply", "result": "ok"}
	rejectLabels := map[string]string{"sink": "insight", "result": "rejected"}
	beforeOps := issue1898Count(t, "domain_operations_total", opLabels)
	beforeRejects := issue1898Count(t, "knowledge_changes_total", rejectLabels)

	captured := c.call("memory_capture", map[string]any{
		"type": "business_knowledge", "category": "business_context", "confidence": "high",
		"content": fmt.Sprintf("Acceptance 1898: an insight written to be rejected (%d).", time.Now().UnixNano()),
	})
	id, _ := captured["id"].(string)
	if id == "" {
		t.Fatalf("memory_capture returned no id: %v", captured)
	}
	t.Cleanup(func() { _, _, _ = c.callRaw("memory_manage", map[string]any{"command": "forget", "id": id}) })

	out := c.call("apply_knowledge", map[string]any{
		"action": "reject", "insight_ids": []any{id}, "review_notes": "Acceptance 1898 rejects its own insight.",
	})
	if number(t, out, "updated") != 1 {
		t.Fatalf("reject updated %v insights: %v", out["updated"], out)
	}

	if after := issue1898Count(t, "domain_operations_total", opLabels); after <= beforeOps {
		t.Errorf("domain_operations_total{operation=knowledge.apply,result=ok} = %v, was %v", after, beforeOps)
	}
	if after := issue1898Count(t, "knowledge_changes_total", rejectLabels); after <= beforeRejects {
		t.Errorf("knowledge_changes_total{sink=insight,result=rejected} = %v, was %v", after, beforeRejects)
	}

	call := awaitSpan(t, c.sessionID, "apply_knowledge")
	deadline := time.Now().Add(spanWait)
	for {
		for _, sp := range readSpans(t) {
			if sp.Name == "knowledge.apply" && sp.TraceID == call.TraceID && sp.ParentSpanID == call.SpanID {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no knowledge.apply span under tools/call apply_knowledge (trace %s) within %s", call.TraceID, spanWait)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
