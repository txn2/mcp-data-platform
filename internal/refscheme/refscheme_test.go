package refscheme

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useScheme(t *testing.T, scheme, uri string) {
	t.Helper()
	require.NoError(t, Configure(scheme, uri))
	t.Cleanup(func() { _ = Configure("", "") })
}

func TestValidate(t *testing.T) {
	for _, bad := range []string{"http", "Acme", "a:b", "schema", "1abc", "acme_corp", strings.Repeat("a", 33)} {
		err := Validate(bad)
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), "server.reference_scheme")
	}
	for _, good := range []string{"", "mcp", "acme", "acme-corp2"} {
		assert.NoError(t, Validate(good), good)
	}
	assert.Error(t, Configure("http", ""))
}

func TestUnsetChangesNothing(t *testing.T) {
	useScheme(t, "", "")
	s := "see mcp:asset:a1 and acme:asset:a1 and mcp://global/x"
	assert.Equal(t, s, Canon(s))
	assert.Equal(t, s, Display(s))
	assert.False(t, Rewrites())
	assert.Equal(t, "mcp", Scheme())
	assert.Equal(t, "mcp", StoredResourceScheme())
}

func TestCanonAndDisplay(t *testing.T) {
	useScheme(t, "acme", "")
	assert.True(t, Rewrites())
	in := `cites acme:asset:a1, (acme:dataset:(trino,w,h.s.t)) "acme:resource:r1" acme://global/x.png`
	canon := Canon(in)
	assert.Equal(t, `cites mcp:asset:a1, (mcp:dataset:(trino,w,h.s.t)) "mcp:resource:r1" mcp://global/x.png`, canon)
	assert.Equal(t, in, Display(canon))
	for _, untouched := range []string{"xacme:asset:a", "https://x/acme:asset:a", "acme:unknown:a", "acme:asset", "my-acme:asset:a"} {
		assert.Equal(t, untouched, Canon(untouched), untouched)
	}
	assert.Equal(t, []byte("mcp:call:c1"), CanonBytes([]byte("acme:call:c1")))
	assert.Equal(t, []byte("acme:call:c1"), DisplayBytes([]byte("mcp:call:c1")))
	assert.Equal(t, "mcp", StoredResourceScheme())

	useScheme(t, "acme", "corp")
	assert.Equal(t, "mcp://global/x", Display("mcp://global/x"), "a resource scheme of its own is left alone")
	assert.Equal(t, "corp", StoredResourceScheme())
	assert.Equal(t, "acme:asset:a", Display("mcp:asset:a"))
	assert.Equal(t, Asset, Of("asset", ""))
}

func TestMiddleware(t *testing.T) {
	useScheme(t, "acme", "")
	var seen string
	handler := Middleware(Options{})(func(_ context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch method {
		case "tools/call":
			p, _ := req.GetParams().(*mcp.CallToolParamsRaw)
			seen = string(p.Arguments)
			return &mcp.CallToolResult{
				Content:           []mcp.Content{&mcp.TextContent{Text: `{"reference":"mcp:asset:a1"}`}, &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "mcp://global/x", Text: "mcp:resource:r"}}},
				StructuredContent: map[string]any{"reference": "mcp:asset:a1", "n": 1},
			}, nil
		case "tools/list":
			return &mcp.ListToolsResult{Tools: []*mcp.Tool{{Name: "fetch", Description: "takes mcp:asset:<id>"}}}, nil
		}
		return &mcp.InitializeResult{Instructions: "no references"}, nil
	})
	req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: "fetch", Arguments: json.RawMessage(`{"id":"acme:asset:a1","ids":["acme:asset:a2","mcp:asset:a3"]}`)}}
	res, err := handler(context.Background(), "tools/call", req)
	require.NoError(t, err)
	assert.Equal(t, `{"id":"mcp:asset:a1","ids":["mcp:asset:a2","mcp:asset:a3"]}`, seen, "every form reaches the handler canonical")
	r, _ := res.(*mcp.CallToolResult)
	assert.Equal(t, `{"reference":"acme:asset:a1"}`, as[*mcp.TextContent](t, r.Content[0]).Text)
	er, _ := r.Content[1].(*mcp.EmbeddedResource)
	assert.Equal(t, "acme://global/x", er.Resource.URI)
	assert.Equal(t, "acme:asset:a1", as[map[string]any](t, r.StructuredContent)["reference"])

	tool := &mcp.Tool{Name: "fetch", Description: "takes mcp:asset:<id>"}
	listHandler := Middleware(Options{})(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.ListToolsResult{Tools: []*mcp.Tool{tool}}, nil
	})
	listed, err := listHandler(context.Background(), "tools/list", &mcp.ServerRequest[*mcp.ListToolsParams]{Params: &mcp.ListToolsParams{}})
	require.NoError(t, err)
	assert.Equal(t, "takes acme:asset:<id>", as[*mcp.ListToolsResult](t, listed).Tools[0].Description)
	assert.Equal(t, "takes mcp:asset:<id>", tool.Description, "the registered tool is not changed")

	other, err := handler(context.Background(), "initialize", &mcp.ServerRequest[*mcp.InitializeParams]{Params: &mcp.InitializeParams{}})
	require.NoError(t, err)
	assert.Equal(t, "no references", as[*mcp.InitializeResult](t, other).Instructions)

	read := &mcp.ServerRequest[*mcp.ReadResourceParams]{Params: &mcp.ReadResourceParams{URI: "acme://global/x"}}
	canonRequest(read, nil)
	assert.Equal(t, "mcp://global/x", read.Params.URI)
	prompt := &mcp.ServerRequest[*mcp.GetPromptParams]{Params: &mcp.GetPromptParams{Arguments: map[string]string{"a": "acme:asset:x"}}}
	canonRequest(prompt, nil)
	assert.Equal(t, "mcp:asset:x", prompt.Params.Arguments["a"])
	typed := &mcp.ServerRequest[*mcp.CallToolParams]{Params: &mcp.CallToolParams{Arguments: map[string]any{"id": "acme:asset:x"}}}
	canonRequest(typed, nil)
	assert.Equal(t, "mcp:asset:x", as[map[string]any](t, typed.Params.Arguments)["id"])

	useScheme(t, "", "")
	passthrough, _ := handler(context.Background(), "tools/call", &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Arguments: json.RawMessage(`{"id":"acme:asset:a1"}`)}})
	assert.Equal(t, `{"id":"acme:asset:a1"}`, seen)
	assert.Equal(t, `{"reference":"mcp:asset:a1"}`, as[*mcp.TextContent](t, as[*mcp.CallToolResult](t, passthrough).Content[0]).Text)
}

func TestHTTP(t *testing.T) {
	useScheme(t, "acme", "")
	var gotPath, gotQuery, gotBody string
	h := HTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		switch r.URL.Path {
		case "/api/v1/file":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "mcp:asset:raw")
		default:
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", "26")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"ref":"mcp:asset:a1"}`)
			_, _ = io.WriteString(w, "    ")
		}
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/portal/refs/acme:asset:a1?ref=acme:call:c1", strings.NewReader(`{"ref":"acme:resource:r1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, "/api/v1/portal/refs/mcp:asset:a1", gotPath)
	assert.Equal(t, "ref=mcp:call:c1", gotQuery)
	assert.Equal(t, `{"ref":"mcp:resource:r1"}`, gotBody)
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, `{"ref":"acme:asset:a1"}    `, rec.Body.String())
	assert.Equal(t, "27", rec.Header().Get("Content-Length"))

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/file", http.NoBody))
	assert.Equal(t, "mcp:asset:raw", rec.Body.String(), "a file is sent as it is")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/portal/acme:asset:a1", http.NoBody))
	assert.Equal(t, "/portal/acme:asset:a1", gotPath, "only the API is rewritten")

	flushing := HTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"a":"mcp:asset:1"}`)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = io.WriteString(w, `{"b":"mcp:asset:2"}`)
	}))
	rec = httptest.NewRecorder()
	flushing.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/stream", http.NoBody))
	assert.Equal(t, `{"a":"acme:asset:1"}{"b":"mcp:asset:2"}`, rec.Body.String())
	assert.NotNil(t, (&displayWriter{ResponseWriter: rec}).Unwrap())
	_, _, err := (&displayWriter{ResponseWriter: rec}).Hijack()
	assert.Error(t, err)
}

// as is v as a T, failing the test when it is not one.
func as[T any](t *testing.T, v any) T {
	t.Helper()
	got, ok := v.(T)
	require.True(t, ok, "%T is not the type expected", v)
	return got
}

// TestProse is #2818: the platform's wording about the reference form is
// written under the deployment's scheme, generic forms included, while a word
// that merely follows "mcp:" with a colon of its own (a URL, an id) is left to
// Display's rule.
func TestProse(t *testing.T) {
	if err := Configure("acme", ""); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = Configure("", "") }()
	for in, want := range map[string]string{
		"A reference is an mcp:<type>:<key> string": "A reference is an acme:<type>:<key> string",
		"fetch the table's mcp:dataset reference":   "fetch the table's acme:dataset reference",
		"an mcp: reference":                         "an acme: reference",
		"`mcp:call:` references":                    "`acme:call:` references",
		"mcp:asset:abc and mcp:dataset:(trino,w,t)": "acme:asset:abc and acme:dataset:(trino,w,t)",
		"ends with mcp:":                            "ends with acme:",
		`{"d":"an mcp:<type> and \"mcp:\""}`:        `{"d":"an acme:<type> and \"acme:\""}`,
		"see https://x/mcp:asset":                   "see https://x/mcp:asset",
	} {
		if got := Prose(in); got != want {
			t.Errorf("Prose(%q) = %q, want %q", in, got, want)
		}
		if got := string(ProseBytes([]byte(in))); got != want {
			t.Errorf("ProseBytes(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Display("an mcp:dataset reference"); got != "an mcp:dataset reference" {
		t.Errorf("Display rewrote prose: %q", got)
	}
	_ = Configure("", "")
	if got := Prose("an mcp: reference"); got != "an mcp: reference" {
		t.Errorf("with no scheme Prose rewrote: %q", got)
	}
}

// TestMiddleware_PlatformText is #2818: a tool named as the platform's own
// text has its generic mcp: wording rewritten as well as its references; any
// other tool's text keeps generic wording, which may be data.
func TestMiddleware_PlatformText(t *testing.T) {
	useScheme(t, "acme", "")
	answer := func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: "cite an mcp: reference"}, &mcp.ResourceLink{URI: "mcp://global/x", Description: "mcp:asset:a"}},
			StructuredContent: map[string]any{"text": "an mcp:<type>:<key> string"},
		}, nil
	}
	call := func(name string) *mcp.CallToolResult {
		t.Helper()
		req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: name, Arguments: json.RawMessage(`{}`)}}
		res, err := Middleware(Options{PlatformText: []string{"platform_info"}})(answer)(context.Background(), "tools/call", req)
		require.NoError(t, err)
		return as[*mcp.CallToolResult](t, res)
	}
	own := call("platform_info")
	assert.Equal(t, "cite an acme: reference", as[*mcp.TextContent](t, own.Content[0]).Text)
	link := as[*mcp.ResourceLink](t, own.Content[1])
	assert.Equal(t, "acme://global/x", link.URI)
	assert.Equal(t, "acme:asset:a", link.Description)
	assert.Equal(t, "an acme:<type>:<key> string", as[map[string]any](t, own.StructuredContent)["text"])

	other := call("trino_query")
	assert.Equal(t, "cite an mcp: reference", as[*mcp.TextContent](t, other.Content[0]).Text)

	assert.False(t, platformTextTool(nil, []string{"platform_info"}))
	assert.False(t, platformTextTool(&mcp.ServerRequest[*mcp.ListToolsParams]{Params: &mcp.ListToolsParams{}}, []string{"platform_info"}))
}

type scriptKey struct{}

// A managed script's session reads results canonical, and a tool's verbatim
// fields pass both ways untouched while its other fields are rewritten
// (#2788).
func TestMiddlewareScriptsAndVerbatimFields(t *testing.T) {
	useScheme(t, "acme", "")
	var seen string
	opts := Options{
		Canonical: func(ctx context.Context) bool { return ctx.Value(scriptKey{}) != nil },
		Verbatim:  map[string][]string{"manage_script": {"source", "edits"}},
	}
	handler := Middleware(opts)(func(_ context.Context, _ string, req mcp.Request) (mcp.Result, error) {
		p, _ := req.GetParams().(*mcp.CallToolParamsRaw)
		seen = string(p.Arguments)
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: `{"reference":"mcp:script:s1","source":"x.startswith(\"mcp:resource:\")"}`}},
			StructuredContent: map[string]any{"reference": "mcp:script:s1", "source": `x.startswith("mcp:resource:")`},
		}, nil
	})
	call := func(ctx context.Context, name, args string) *mcp.CallToolResult {
		res, err := handler(ctx, "tools/call", &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: &mcp.CallToolParamsRaw{Name: name, Arguments: json.RawMessage(args)}})
		require.NoError(t, err)
		return as[*mcp.CallToolResult](t, res)
	}

	// A script's session: the result keeps the canonical form.
	script := call(context.WithValue(context.Background(), scriptKey{}, true), "manage_resource", `{"reference":"acme:resource:r1"}`)
	assert.JSONEq(t, `{"reference":"mcp:resource:r1"}`, seen, "a script's request is still made canonical")
	assert.Equal(t, "mcp:script:s1", as[map[string]any](t, script.StructuredContent)["reference"])
	assert.Contains(t, as[*mcp.TextContent](t, script.Content[0]).Text, `"reference":"mcp:script:s1"`)

	// An agent writing a script: its source keeps the deployment's scheme as
	// written, the reference beside it is canonical.
	agent := call(context.Background(), "manage_script", `{"reference":"acme:script:s1","source":"s.replace(\"acme://\", \"mcp://\")","edits":[{"find":"acme:resource:","replace":"x"}]}`)
	assert.JSONEq(t, `{"reference":"mcp:script:s1","source":"s.replace(\"acme://\", \"mcp://\")","edits":[{"find":"acme:resource:","replace":"x"}]}`, seen)
	sc := as[map[string]any](t, agent.StructuredContent)
	assert.Equal(t, "acme:script:s1", sc["reference"], "a reference outside the code is displayed")
	assert.Equal(t, `x.startswith("mcp:resource:")`, sc["source"], "the source reads back as it was saved")
	text := as[*mcp.TextContent](t, agent.Content[0]).Text
	assert.Contains(t, text, `"reference": "acme:script:s1"`)
	assert.Contains(t, text, `x.startswith(\"mcp:resource:\")`)

	// Another tool is rewritten whole, as before.
	call(context.Background(), "save_asset", `{"content":"see acme:asset:a1"}`)
	assert.JSONEq(t, `{"content":"see mcp:asset:a1"}`, seen)
}

// A request to an endpoint carries back a reference a response gave out: its
// path, query and JSON body reach the script canonical, and its JSON answer
// leaves under the scheme; a webhook post is not rewritten (#2788).
func TestHTTPEndpoints(t *testing.T) {
	useScheme(t, "acme", "")
	var gotPath, gotQuery, gotBody string
	h := HTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"reference":"mcp:resource:r1"}`)
	}))
	for _, path := range []string{"/endpoints/upload/acme:resource:r1", "/environments/dev/endpoints/upload/acme:resource:r1"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path+"?ref=acme:resource:r1", strings.NewReader(`{"file":"acme:resource:r1"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.True(t, strings.HasSuffix(gotPath, "/mcp:resource:r1"), gotPath)
		assert.Equal(t, "ref=mcp:resource:r1", gotQuery)
		assert.JSONEq(t, `{"file":"acme:resource:r1"}`, gotBody, "an endpoint makes its body canonical itself, after its limits")
		assert.JSONEq(t, `{"reference":"acme:resource:r1"}`, rec.Body.String())
	}
	assert.Equal(t, `{"file":"mcp:resource:r1"}`, string(CanonJSONBody("application/json; charset=utf-8", []byte(`{"file":"acme:resource:r1"}`))))
	assert.Equal(t, `file=acme:resource:r1`, string(CanonJSONBody("application/x-www-form-urlencoded", []byte(`file=acme:resource:r1`))))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/hooks/src", strings.NewReader(`{"file":"acme:resource:r1"}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.JSONEq(t, `{"file":"acme:resource:r1"}`, gotBody, "a webhook's body is the sender's")
}

// A chunked JSON body past the rewrite bound reaches the handler whole and as
// sent, never cut at the bound.
func TestHTTPOversizedBodyPassesWhole(t *testing.T) {
	useScheme(t, "acme", "")
	var got int
	h := HTTP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = len(b)
	}))
	body := strings.Repeat("a", maxRewrittenBody+10)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/x", io.NopCloser(strings.NewReader(body)))
	req.ContentLength = -1
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, len(body), got)
}

// A verbatim tool's JSON keeps a large integer exact when its other fields are
// rewritten.
func TestVerbatimRewriteKeepsLargeNumbers(t *testing.T) {
	useScheme(t, "acme", "")
	out := rewriteJSONBytes([]byte(`{"n":9007199254740993,"ref":"acme:asset:a","source":"acme:asset:a"}`), CanonBytes, Canon, map[string]bool{"source": true})
	assert.JSONEq(t, `{"n":9007199254740993,"ref":"mcp:asset:a","source":"acme:asset:a"}`, string(out))
	assert.Contains(t, string(out), "9007199254740993")
}
