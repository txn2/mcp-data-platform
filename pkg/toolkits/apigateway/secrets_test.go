package apigateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/secretref"
	"github.com/txn2/mcp-data-platform/internal/secretstore"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// fakeSecrets answers like the store: portal_password on the grid
// connection for any persona, admin_key on grid for the admin persona only.
type fakeSecrets struct{}

func (fakeSecrets) Lookup(_ context.Context, connection, persona string) secretref.Lookup {
	secrets := map[string]secretstore.Secret{
		"portal_password": {Name: "portal_password", AllowConnections: []string{"grid"}},
		"admin_key":       {Name: "admin_key", AllowConnections: []string{"grid"}, AllowPersonas: []string{"admin"}},
	}
	values := map[string]string{"portal_password": `s3cr"et/pw`, "admin_key": "adm-998877"}
	return func(name string) (string, error) {
		sec, ok := secrets[name]
		if !ok {
			return "", errors.New(`secret "` + name + `" does not exist`)
		}
		if err := secretstore.Allowed(sec, connection, persona); err != nil {
			return "", fmt.Errorf("lookup: %w", err)
		}
		return values[name], nil
	}
}

// echoUpstream records each request and echoes its query, a header and its
// body back, the way the api-test fixture's /v1/echo does.
type echoUpstream struct {
	mu   sync.Mutex
	seen []string
	url  string
}

func (e *echoUpstream) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	e.mu.Lock()
	e.seen = append(e.seen, r.URL.RequestURI()+" "+r.Header.Get("X-Portal")+" "+string(body))
	e.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Echo", r.Header.Get("X-Portal"))
	_ = json.NewEncoder(w).Encode(map[string]any{"path": r.URL.Path, "query": r.URL.RawQuery, "body": string(body), "x_portal": r.Header.Get("X-Portal")})
}

func (e *echoUpstream) requests() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.seen...)
}

func secretsToolkit(t *testing.T, src secretstore.Source) (*Toolkit, *echoUpstream) {
	t.Helper()
	up := &echoUpstream{}
	srv := httptest.NewServer(http.HandlerFunc(up.serve))
	t.Cleanup(srv.Close)
	up.url = srv.URL
	tk := NewMulti(MultiConfig{})
	if src != nil {
		tk.SetSecrets(src)
	}
	for _, name := range []string{"grid", "other"} {
		if err := tk.AddConnection(name, map[string]any{"base_url": srv.URL}); err != nil {
			t.Fatalf("adding %s: %v", name, err)
		}
	}
	return tk, up
}

func invokeOut(ctx context.Context, t *testing.T, tk *Toolkit, in InvokeInput) (out InvokeOutput, refusal string) {
	t.Helper()
	res, payload, err := tk.handleInvoke(ctx, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		return InvokeOutput{}, textOf(res)
	}
	out, _ = payload.(InvokeOutput)
	return out, ""
}

// textOf is a result's first text content.
func textOf(res *mcp.CallToolResult) string {
	if tc, ok := res.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

// TestSecretsAreFilledAtSendAndRedactedFromTheResponse is #2051's criteria
// at the gateway: every place a placeholder may sit is filled with the
// value as that place encodes it, the upstream receives the value, and the
// response that echoes it carries the redaction instead.
func TestSecretsAreFilledAtSendAndRedactedFromTheResponse(t *testing.T) {
	tk, up := secretsToolkit(t, fakeSecrets{})
	ctx := mcpcontext.WithPersona(context.Background(), "analyst")

	cases := map[string]struct {
		in   InvokeInput
		sent string
	}{
		"body object": {
			InvokeInput{Connection: "grid", Method: "POST", Path: "/login", Body: map[string]any{"text": "{{secret:portal_password}}"}},
			`/login  {"text":"s3cr\"et/pw"}`,
		},
		"body string of JSON": {
			InvokeInput{Connection: "grid", Method: "POST", Path: "/login", Body: `{"text": "{{secret:portal_password}}"}`},
			`/login  {"text": "s3cr\"et/pw"}`,
		},
		"form body": {
			InvokeInput{Connection: "grid", Method: "POST", Path: "/login", Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, Body: "user=a&pw={{secret:portal_password}}"},
			`/login  user=a&pw=s3cr%22et%2Fpw`,
		},
		"query and header": {
			InvokeInput{Connection: "grid", Method: "GET", Path: "/q", Query: map[string]any{"pw": "{{secret:portal_password}}"}, Headers: map[string]string{"X-Portal": "{{secret:portal_password}}"}},
			`/q?pw=s3cr%22et%2Fpw s3cr"et/pw `,
		},
		"path": {
			InvokeInput{Connection: "grid", Method: "GET", Path: "/u/{{secret:portal_password}}"},
			`/u/s3cr%22et%2Fpw  `,
		},
	}
	for name, c := range cases {
		before := len(up.requests())
		out, refusal := invokeOut(ctx, t, tk, c.in)
		if refusal != "" {
			t.Fatalf("%s: refused: %s", name, refusal)
		}
		reqs := up.requests()
		if len(reqs) != before+1 || reqs[before] != c.sent {
			t.Errorf("%s: upstream received %q, want %q", name, reqs[len(reqs)-1], c.sent)
		}
		text, _ := json.Marshal(out)
		if strings.Contains(string(text), `s3cr`) {
			t.Errorf("%s: the response carries the value: %s", name, text)
		}
		if !strings.Contains(string(text), "[REDACTED:portal_password]") {
			t.Errorf("%s: the echoed value was not redacted: %s", name, text)
		}
	}
	if body, _ := cases["body object"].in.Body.(map[string]any); body["text"] != "{{secret:portal_password}}" {
		t.Error("the caller's input was changed: what is recorded must keep the placeholder")
	}
}

// TestSecretRefusalsSendNothing covers the refusals: an unknown name, a
// connection outside the secret's scope, a persona outside it, a malformed
// placeholder, and a toolkit with no secrets wired. None reaches the
// upstream, and each names why.
func TestSecretRefusalsSendNothing(t *testing.T) {
	tk, up := secretsToolkit(t, fakeSecrets{})
	bare, bareUp := secretsToolkit(t, nil)
	analyst := mcpcontext.WithPersona(context.Background(), "analyst")
	cases := []struct {
		tk   *Toolkit
		ctx  context.Context
		in   InvokeInput
		want string
	}{
		{tk, analyst, InvokeInput{Connection: "grid", Method: "POST", Path: "/x", Body: map[string]any{"p": "{{secret:nope}}"}}, `secret "nope" does not exist`},
		{tk, analyst, InvokeInput{Connection: "other", Method: "POST", Path: "/x", Body: map[string]any{"p": "{{secret:portal_password}}"}}, `may not be sent through connection "other"`},
		{tk, analyst, InvokeInput{Connection: "grid", Method: "GET", Path: "/x", Headers: map[string]string{"X-K": "{{secret:admin_key}}"}}, "may not be used by persona analyst"},
		{tk, analyst, InvokeInput{Connection: "grid", Method: "GET", Path: "/x", Query: map[string]any{"k": "{{secret:Bad}}"}}, "malformed secret placeholder"},
		{tk, analyst, InvokeInput{Connection: "grid", Method: "GET", Path: "/x/{{secret:nope}}"}, `secret "nope" does not exist`},
		{bare, analyst, InvokeInput{Connection: "grid", Method: "POST", Path: "/x", Body: "{{secret:portal_password}}"}, "stored secrets are not available here"},
	}
	for _, c := range cases {
		res, _, err := c.tk.handleInvoke(c.ctx, nil, c.in)
		if err != nil || !res.IsError {
			t.Fatalf("%+v was not refused", c.in)
		}
		if text := strings.ReplaceAll(textOf(res), `\"`, `"`); !strings.Contains(text, c.want) {
			t.Errorf("refusal %q does not contain %q", text, c.want)
		}
	}
	if n := len(up.requests()) + len(bareUp.requests()); n != 0 {
		t.Errorf("%d requests reached the upstream; a refused placeholder sends nothing", n)
	}

	admin := mcpcontext.WithPersona(context.Background(), "admin")
	if _, refusal := invokeOut(admin, t, tk, InvokeInput{Connection: "grid", Method: "GET", Path: "/x", Headers: map[string]string{"X-Portal": "{{secret:admin_key}}"}}); refusal != "" {
		t.Errorf("the admin persona was refused its own secret: %s", refusal)
	}
}

// TestSecretsInATransportError are redacted from it: a filled path is in
// the URL the client reports.
func TestSecretsInATransportError(t *testing.T) {
	tk := NewMulti(MultiConfig{})
	tk.SetSecrets(fakeSecrets{})
	if err := tk.AddConnection("grid", map[string]any{"base_url": "http://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	res, _, err := tk.handleInvoke(context.Background(), nil, InvokeInput{Connection: "grid", Method: "GET", Path: "/u/{{secret:portal_password}}"})
	if err != nil {
		t.Fatal(err)
	}
	if text := textOf(res); strings.Contains(text, "s3cr") || !strings.Contains(text, "[REDACTED:portal_password]") {
		t.Errorf("transport error = %q", text)
	}
}

// TestSecretsInAnExportAreFilledAndRedactedFromTheAsset: api_export fills a
// placeholder the same way, and the stored asset, streamed from the
// upstream, carries the redaction where the upstream echoed the value.
func TestSecretsInAnExportAreFilledAndRedactedFromTheAsset(t *testing.T) {
	up := &echoUpstream{}
	srv := httptest.NewServer(http.HandlerFunc(up.serve))
	t.Cleanup(srv.Close)
	s3 := &fakeExportS3Client{}
	deps := defaultExportDeps(&fakeExportAssetStore{}, &fakeExportVersionStore{}, s3)
	tk := buildExportTestToolkit(t, srv.URL, &deps)
	tk.SetSecrets(fakeExportSecrets{})

	r, _, _ := tk.handleExport(context.Background(), &mcp.CallToolRequest{}, exportInput{
		Connection: "crm", Method: "POST", Path: "/v1/login", Name: "login echo",
		Body: map[string]any{"text": "{{secret:portal_password}}"},
	})
	if r == nil || r.IsError {
		t.Fatalf("handleExport refused: %+v", r)
	}
	if reqs := up.requests(); len(reqs) != 1 || !strings.Contains(reqs[0], `s3cr\"et/pw`) {
		t.Fatalf("the upstream received %v", reqs)
	}
	if len(s3.puts) != 1 || strings.Contains(string(s3.puts[0].Data), "s3cr") || !strings.Contains(string(s3.puts[0].Data), "[REDACTED:portal_password]") {
		t.Errorf("the stored asset is %q", s3.puts)
	}
}

// fakeExportSecrets allows portal_password on the export test connection.
type fakeExportSecrets struct{}

func (fakeExportSecrets) Lookup(_ context.Context, connection, _ string) secretref.Lookup {
	return func(name string) (string, error) {
		if name != "portal_password" || connection != "crm" {
			return "", errors.New("not allowed")
		}
		return `s3cr"et/pw`, nil
	}
}

// A connection with fill_secrets false sends a call's placeholders as
// written, in every place a call may put one (#2066): the built-in
// platform-admin connection saves a connection or secret that names a
// stored secret, and the admin API stores it as written.
func TestFillSecretsFalseSendsPlaceholdersAsWritten(t *testing.T) {
	tk, up := secretsToolkit(t, fakeSecrets{})
	if err := tk.AddConnection("admin-self", map[string]any{"base_url": up.url, "fill_secrets": false}); err != nil {
		t.Fatal(err)
	}
	ctx := mcpcontext.WithPersona(context.Background(), "admin")
	for name, in := range map[string]InvokeInput{
		"body object":         {Connection: "admin-self", Method: "PUT", Path: "/c", Body: map[string]any{"credential": "{{secret:tableau-rest}}"}},
		"body string of JSON": {Connection: "admin-self", Method: "PUT", Path: "/c", Body: `{"credential":"{{secret:tableau-rest}}"}`},
		"query and header":    {Connection: "admin-self", Method: "GET", Path: "/q", Query: map[string]any{"v": "{{secret:tableau-rest}}"}, Headers: map[string]string{"X-Portal": "{{secret:tableau-rest}}"}},
	} {
		before := len(up.requests())
		if _, refusal := invokeOut(ctx, t, tk, in); refusal != "" {
			t.Fatalf("%s: refused: %s", name, refusal)
		}
		reqs := up.requests()
		if len(reqs) != before+1 {
			t.Fatalf("%s: nothing was sent", name)
		}
		sent, _ := url.QueryUnescape(reqs[before])
		if !strings.Contains(sent, "{{secret:tableau-rest}}") {
			t.Errorf("%s: the upstream received %q, not the placeholder as written", name, reqs[before])
		}
	}
}

// A connection whose own configuration names a stored secret sends its
// value, and a response that echoes it reaches the caller redacted (#2066).
func TestConnectionConfigSecretIsSentAndRedacted(t *testing.T) {
	prev := secretref.SetConnectionSource(func(_ context.Context, name, connection string) (string, error) {
		if name == "grid-key" && connection == "keyed" {
			return "grid-key-value-77", nil
		}
		return "", fmt.Errorf("secret %q may not be used by connection %q", name, connection)
	})
	t.Cleanup(func() { secretref.SetConnectionSource(prev) })
	tk, up := secretsToolkit(t, fakeSecrets{})
	if err := tk.AddConnection("keyed", map[string]any{"base_url": up.url, "static_headers": map[string]any{"X-Portal": "{{secret:grid-key}}"}}); err != nil {
		t.Fatal(err)
	}
	out, refusal := invokeOut(mcpcontext.WithPersona(context.Background(), "analyst"), t, tk, InvokeInput{Connection: "keyed", Method: "GET", Path: "/h"})
	if refusal != "" {
		t.Fatal(refusal)
	}
	reqs := up.requests()
	if got := reqs[len(reqs)-1]; got != "/h grid-key-value-77 " {
		t.Errorf("upstream received %q", got)
	}
	text, _ := json.Marshal(out)
	if strings.Contains(string(text), "grid-key-value-77") || !strings.Contains(string(text), "[REDACTED:grid-key]") {
		t.Errorf("the echoed header was not redacted: %s", text)
	}
}

// codeSecrets answers {{totp:mfa}} with a fixed code, as the store answers it
// with the code for the moment (#2065), and records the seed for redaction.
type codeSecrets struct{}

func (codeSecrets) Lookup(ctx context.Context, _, _ string) secretref.Lookup {
	return func(name string) (string, error) {
		if name != secretref.TOTPName("mfa") {
			return "", fmt.Errorf("secret %q is an authenticator seed, which is never sent", name)
		}
		secretref.FromContext(ctx).Add("mfa", "GEZDGNBVGY3TQOJQ")
		return "287082", nil
	}
}

// TestOneTimeCodesAreFilledEverywhereAPlaceholderSits sends {{totp:<name>}}
// in each place a request carries one; the code is sent, and it is not
// redacted from the answer, while the seed would be.
func TestOneTimeCodesAreFilledEverywhereAPlaceholderSits(t *testing.T) {
	tk, up := secretsToolkit(t, codeSecrets{})
	ctx := mcpcontext.WithPersona(context.Background(), "analyst")
	for name, c := range map[string]struct {
		in   InvokeInput
		sent string
	}{
		"body object":         {InvokeInput{Connection: "grid", Method: "POST", Path: "/v", Body: map[string]any{"text": "{{totp:mfa}}"}}, `/v  {"text":"287082"}`},
		"body string of JSON": {InvokeInput{Connection: "grid", Method: "POST", Path: "/v", Body: `{"text":"{{totp:mfa}}"}`}, `/v  {"text":"287082"}`},
		"query and header":    {InvokeInput{Connection: "grid", Method: "GET", Path: "/q", Query: map[string]any{"code": "{{totp:mfa}}"}, Headers: map[string]string{"X-Portal": "{{totp:mfa}}"}}, `/q?code=287082 287082 `},
	} {
		out, refusal := invokeOut(ctx, t, tk, c.in)
		if refusal != "" {
			t.Fatalf("%s: refused: %s", name, refusal)
		}
		reqs := up.requests()
		if got := reqs[len(reqs)-1]; got != c.sent {
			t.Errorf("%s: upstream received %q, want %q", name, got, c.sent)
		}
		text, _ := json.Marshal(out)
		if !strings.Contains(string(text), "287082") {
			t.Errorf("%s: the code was redacted from the answer: %s", name, text)
		}
	}
	_, refusal := invokeOut(ctx, t, tk, InvokeInput{Connection: "grid", Method: "GET", Path: "/q", Query: map[string]any{"pw": "{{secret:mfa}}"}})
	if !strings.Contains(refusal, "never sent") {
		t.Errorf("a seed named as a value was not refused: %q", refusal)
	}
}
