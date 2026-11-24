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
	handler := Middleware()(func(_ context.Context, method string, req mcp.Request) (mcp.Result, error) {
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
	listHandler := Middleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
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
	canonRequest(read)
	assert.Equal(t, "mcp://global/x", read.Params.URI)
	prompt := &mcp.ServerRequest[*mcp.GetPromptParams]{Params: &mcp.GetPromptParams{Arguments: map[string]string{"a": "acme:asset:x"}}}
	canonRequest(prompt)
	assert.Equal(t, "mcp:asset:x", prompt.Params.Arguments["a"])
	typed := &mcp.ServerRequest[*mcp.CallToolParams]{Params: &mcp.CallToolParams{Arguments: map[string]any{"id": "acme:asset:x"}}}
	canonRequest(typed)
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
