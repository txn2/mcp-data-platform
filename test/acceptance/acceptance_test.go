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
	"context"
	"encoding/json"
	"fmt"
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
	apiKey := os.Getenv("MCP_API_KEY")
	if apiKey == "" {
		apiKey = defaultDevAPIKey
	}
	target := baseURL()

	ctx, cancel := context.WithTimeout(context.Background(), sessionTimeout)
	t.Cleanup(cancel)

	httpClient := &http.Client{Transport: authRoundTripper{key: apiKey, base: http.DefaultTransport}}
	mc := mcp.NewClient(&mcp.Implementation{Name: "acceptance", Version: "1.0.0"}, nil)
	session, err := mc.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: target, HTTPClient: httpClient}, nil)
	if err != nil {
		t.Fatalf("no platform answers at %s (%v). The acceptance suite needs a running server: start one with `make dev`, or set MCP_BASE_URL and MCP_API_KEY", target, err)
	}
	t.Cleanup(func() { _ = session.Close() })

	c := &client{t: t, ctx: ctx, session: session}
	info := c.call("platform_info", nil)
	c.sessionID, _ = info["session_id"].(string)
	if c.sessionID == "" {
		t.Fatalf("platform_info returned no session_id: %v", info)
	}
	c.call("search", map[string]any{
		"intent": "acceptance suite discovery", "limit": 1,
		"purpose": "The acceptance suite performs the discovery the search-first gate requires.",
	})
	return c
}

// rateLimitRetries bounds how many times a call refused by the tool-call
// rate limiter is issued again. The limiter is a per-identity backstop, and
// the suite shares the dev API key with the start script's gateway trickle,
// so a refusal is answered the way the refusal itself says to: wait the
// interval it names and retry.
const rateLimitRetries = 8

// call invokes a tool with the session handle attached and returns its JSON
// result, failing the test on a transport error or a tool error. A
// rate-limit refusal is waited out and retried, as the refusal instructs.
func (c *client) call(name string, args map[string]any) map[string]any {
	c.t.Helper()
	var (
		res  *mcp.CallToolResult
		text string
		err  error
	)
	for attempt := 0; attempt <= rateLimitRetries; attempt++ {
		res, text, err = c.callRaw(name, args)
		if err != nil {
			c.t.Fatalf("%s: transport error: %v", name, err)
		}
		if !res.IsError || !strings.Contains(text, "RATE_LIMITED") {
			break
		}
		time.Sleep(retryAfter(text))
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
// leaving the verdict to the caller.
func (c *client) callRaw(name string, args map[string]any) (*mcp.CallToolResult, string, error) {
	if args == nil {
		args = map[string]any{}
	}
	if c.sessionID != "" {
		args["session_id"] = c.sessionID
	}
	res, err := c.session.CallTool(c.ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, "", err
	}
	return res, firstText(res), nil
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
