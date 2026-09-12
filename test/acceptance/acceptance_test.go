//go:build integration

// Package acceptance is the per-ticket acceptance suite: each file carries one
// issue's acceptance criteria, executed as a real MCP client against a running
// platform, through the tool surface a user calls. It is the step between
// writing code and `make verify`, and the step `make verify-release` requires.
//
// It runs against the local dev stack (`make dev`) by default and fails, rather
// than skipping, when no server answers: a gate that skips is a gate that was
// not run. Point it elsewhere with MCP_BASE_URL and MCP_API_KEY.
//
//	make dev            # in one terminal
//	make acceptance     # in another
//
// A file is named for the issue it proves (issue_<n>_test.go) and its tests are
// named for the criterion, so a failure reads as the sentence that no longer
// holds.
package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultDevPort   = "8080"
	defaultDevAPIKey = "acme-dev-key-2024"
	sessionTimeout   = 120 * time.Second
)

// authRoundTripper injects the API key as a Bearer token on every request.
type authRoundTripper struct {
	key  string
	base http.RoundTripper
}

func (a authRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+a.key)
	return a.base.RoundTrip(r)
}

// client is one authenticated MCP session with the platform's session handle
// already negotiated, so every call carries it.
type client struct {
	t         *testing.T
	ctx       context.Context
	session   *mcp.ClientSession
	sessionID string
	// apiKey is the identity this session authenticated with, kept so a REST
	// route can be reached as the same person the tool calls are made by.
	apiKey string
	// base is the platform process this session is open on, so a REST route
	// reaches the same replica the tool calls do.
	base string
}

// baseURL is where the suite connects: MCP_BASE_URL, or the dev server on
// DEV_API_PORT (dev/start.sh relocates the stack when 8080 is busy).
func baseURL() string {
	if v := os.Getenv("MCP_BASE_URL"); v != "" {
		return v
	}
	port := os.Getenv("DEV_API_PORT")
	if port == "" {
		port = defaultDevPort
	}
	return "http://localhost:" + port
}

// connect opens the session, reads the platform's session handle from
// platform_info, and performs the one discovery call the search-first gate
// requires of a session before a query tool is admitted.
func connect(t *testing.T) *client {
	t.Helper()
	return connectAs(t, devAPIKey())
}

// devAPIKey is the administrator key the suite authenticates with: MCP_API_KEY,
// or the dev stack's.
func devAPIKey() string {
	if apiKey := os.Getenv("MCP_API_KEY"); apiKey != "" {
		return apiKey
	}
	return defaultDevAPIKey
}

// connectAs is connect for a named identity: a criterion about who may reach
// what has to be executed by the person it is about, not by the administrator
// the default key authenticates as. The dev stack carries two ordinary people
// for exactly this (dev/platform.yaml), and a deployment the suite is pointed
// at elsewhere supplies its own keys.
func connectAs(t *testing.T, apiKey string) *client {
	t.Helper()
	return connectAt(t, baseURL(), apiKey)
}

// connectPeer opens a session on a second replica of the same deployment, the
// one MCP_PEER_BASE_URL names. A criterion about what every replica answers
// needs two processes over one database, and nothing stands in for the second
// one: the criterion fails, rather than skips, when no peer is named.
func connectPeer(t *testing.T) *client {
	t.Helper()
	target := os.Getenv("MCP_PEER_BASE_URL")
	if target == "" {
		t.Fatal("MCP_PEER_BASE_URL is not set. This criterion runs against two replicas of one deployment: start a second platform process from the same configuration on another port, against the same database, and set MCP_PEER_BASE_URL to it")
	}
	return connectAt(t, target, devAPIKey())
}

// connectAt opens a session on one platform process as one identity.
func connectAt(t *testing.T, target, apiKey string) *client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), sessionTimeout)
	t.Cleanup(cancel)

	httpClient := &http.Client{Transport: authRoundTripper{key: apiKey, base: http.DefaultTransport}}
	mc := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "1.0.0"}, nil)
	session, err := mc.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: target, HTTPClient: httpClient}, nil)
	if err != nil {
		t.Fatalf("no platform answers at %s (%v). The acceptance suite needs a running server: start one with `make dev`, or set MCP_BASE_URL and MCP_API_KEY", target, err)
	}
	t.Cleanup(func() { _ = session.Close() })

	c := &client{t: t, ctx: ctx, session: session, base: target}
	info := c.call("platform_info", nil)
	c.sessionID, _ = info["session_id"].(string)
	if c.sessionID == "" {
		t.Fatalf("platform_info returned no session_id: %v", info)
	}
	c.call("search", map[string]any{
		"intent": "acceptance suite discovery", "limit": 1,
		"purpose": "The acceptance suite performs the discovery the search-first gate requires.",
	})
	c.apiKey = apiKey
	return c
}

// devOwnerAPIKey and devPeerAPIKey are the two ordinary, non-administrator
// people the dev stack carries: one owns their own work, the other is somebody
// else. A criterion about owner authority is executed by them rather than by
// the administrator the default key authenticates as.
const (
	devOwnerAPIKey   = "acme-owner-key"
	devOwnerEmail    = "asset.owner@example.com"
	devPeerAPIKey    = "acme-peer-key"
	devPeerEmailAddr = "asset.peer@example.com"
)

// jsonBody encodes a request body for the admin REST routes. The suite's
// restJSON returns only the status, and a criterion that reads the refusal
// text needs the decoded body too, so it builds the request body here and
// hands it to rest directly.
func jsonBody(t *testing.T, v any) io.Reader {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return bytes.NewReader(raw)
}

// rest issues an authenticated REST request as this client's identity and
// returns the status and the decoded body. The portal's own pages read these
// routes, so a criterion about what the Assets page shows is checked here
// rather than only through the tool surface.
func (c *client) rest(method, path string, body io.Reader) (int, map[string]any) {
	c.t.Helper()
	req, err := http.NewRequestWithContext(c.ctx, method, c.base+path, body)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close() //nolint:errcheck // best-effort close after read
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		c.t.Fatalf("%s %s: reading the body: %v", method, path, err)
	}
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out) //nolint:errcheck // a non-object body leaves out nil
	}
	return res.StatusCode, out
}

// tools returns what tools/list carries for this session, which is the
// inventory a caller actually has. A tool a toolkit names and the server never
// registered is absent here while every listing built from the toolkits shows
// it (#1675).
func (c *client) tools() []*mcp.Tool {
	c.t.Helper()
	res, err := c.session.ListTools(c.ctx, &mcp.ListToolsParams{})
	if err != nil {
		c.t.Fatalf("tools/list: %v", err)
	}
	return res.Tools
}

// rateLimitRetries bounds how many times a call refused by the tool-call
// rate limiter is issued again. The limiter is a per-identity backstop, and
// the suite shares the dev API key with the start script's gateway trickle,
// so a refusal is answered the way the refusal itself says to: wait the
// interval it names and retry.
const rateLimitRetries = 8

// call invokes a tool with the session handle attached and returns its JSON
// result, failing the test on a transport error or a tool error.
func (c *client) call(name string, args map[string]any) map[string]any {
	c.t.Helper()
	res, text, err := c.callRaw(name, args)
	if err != nil {
		c.t.Fatalf("%s: transport error: %v", name, err)
	}
	if res.IsError {
		c.t.Fatalf("%s: tool error: %s", name, text)
	}
	var out map[string]any
	if text != "" {
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			c.t.Fatalf("%s: result is not a JSON object: %v\n%s", name, err, text)
		}
	}
	return out
}

// callRaw invokes a tool and returns the result and its first text block,
// leaving the verdict on the tool's own answer to the caller.
//
// A rate-limit refusal is not one of those answers, and waiting it out belongs
// here rather than in call (#1632). RATE_LIMITED is itself a refusal, so a
// criterion about a refusal -- which reads the text off this method rather
// than off call -- was asserting against the limiter whenever the backstop
// happened to fire, and failing over a contract it never reached.
func (c *client) callRaw(name string, args map[string]any) (*mcp.CallToolResult, string, error) {
	if args == nil {
		args = map[string]any{}
	}
	if c.sessionID != "" {
		args["session_id"] = c.sessionID
	}
	statePurpose(name, args)
	for attempt := 0; ; attempt++ {
		res, err := c.session.CallTool(c.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			return nil, "", err
		}
		text := firstText(res)
		if attempt >= rateLimitRetries || !res.IsError || !strings.Contains(text, "RATE_LIMITED") {
			return res, text, nil
		}
		time.Sleep(retryAfter(text))
	}
}

// assetWriteTools are the two tools #1695 added to the purpose gate. They are
// not data-access tools, which is why they were outside it: an asset is the
// OUTPUT of the work, and the sentence recorded beside it is what lets someone
// opening it weeks later learn what it was for.
//
// The suite states one the way the platform's own instructions tell an agent
// to, for the same reason it threads the session handle on every call: both are
// arguments the platform adds to the tool and requires of a real agent, so a
// suite that omitted them would be a client no deployment accepts. A criterion
// ABOUT the gate sends its own literal params through session.CallTool rather
// than through this, so the gate is still exercised as an agent meets it.
var assetWriteTools = map[string]bool{"save_asset": true, "manage_asset": true}

// statePurpose states why a gated call is being made, when the caller has not.
// A caller that passes its own purpose keeps it.
func statePurpose(name string, args map[string]any) {
	if !assetWriteTools[name] {
		return
	}
	if _, stated := args["purpose"]; stated {
		return
	}
	args["purpose"] = "The acceptance suite is exercising an acceptance criterion against this asset."
}

// retryAfter reads the interval a rate-limit refusal names ("Wait about N
// second(s)"), falling back to one second.
func retryAfter(text string) time.Duration {
	var seconds int
	if _, err := fmt.Sscanf(text[strings.Index(text, "Wait about")+len("Wait about"):], " %d", &seconds); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Second
}

func firstText(res *mcp.CallToolResult) string {
	for _, content := range res.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// number reads a JSON number out of a result.
func number(t *testing.T, out map[string]any, key string) float64 {
	t.Helper()
	v, ok := out[key].(float64)
	if !ok {
		t.Fatalf("%s: not a number in %v", key, out)
	}
	return v
}
