package apigateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const ratesXML = `<GetRatesResponse xmlns="urn:acme:rates"><Rate currency="EUR">0.92</Rate></GetRatesResponse>`

func TestResponseDecoder_Modes(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		declaredXML bool
		contentType string
		body        string
		wantTag     string
		wantText    string
		wantJSON    bool
	}{
		{
			name: "auto keeps text for an XML response on a catalog-less connection",
			mode: "", contentType: "text/xml", body: ratesXML, wantText: ratesXML,
		},
		{
			name: "auto decodes XML the catalog declares",
			mode: "", declaredXML: true, contentType: "text/xml", body: ratesXML, wantTag: "GetRatesResponse",
		},
		{
			name: "auto decodes XML the catalog declares when the response says nothing",
			mode: "", declaredXML: true, contentType: "", body: ratesXML, wantTag: "GetRatesResponse",
		},
		{
			name: "auto prefers JSON when the response says JSON",
			mode: "", declaredXML: true, contentType: "application/json", body: `{"a":1}`, wantJSON: true,
		},
		{
			name: "auto keeps text when the catalog declares XML and the response is HTML",
			mode: "", declaredXML: true, contentType: "text/html", body: "<p>down for maintenance</p>",
			wantText: "<p>down for maintenance</p>",
		},
		{
			name: "explicit xml overrules a text/plain response",
			mode: DecodeXML, contentType: "text/plain", body: ratesXML, wantTag: "GetRatesResponse",
		},
		{
			name: "explicit xml overrules a JSON response type",
			mode: DecodeXML, contentType: "application/json", body: ratesXML, wantTag: "GetRatesResponse",
		},
		{
			name: "explicit text overrules the catalog",
			mode: DecodeText, declaredXML: true, contentType: "text/xml", body: ratesXML, wantText: ratesXML,
		},
		{
			name: "explicit json parses an undeclared body",
			mode: DecodeJSON, contentType: "text/plain", body: `{"a":1}`, wantJSON: true,
		},
		{
			name: "auto is the same as the empty mode",
			mode: DecodeAuto, declaredXML: true, contentType: "application/soap+xml", body: ratesXML,
			wantTag: "GetRatesResponse",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := responseDecoder{mode: tt.mode, declaredXML: tt.declaredXML}.
				decode(tt.contentType, []byte(tt.body))
			assert.Empty(t, got.note)
			assert.Equal(t, tt.wantJSON, got.json)
			switch {
			case tt.wantTag != "":
				tree, ok := got.body.(map[string]any)
				require.True(t, ok, "got %#v; want an XML tree", got.body)
				assert.Equal(t, tt.wantTag, tree["tag"])
			case tt.wantText != "":
				assert.Equal(t, tt.wantText, got.body)
			}
		})
	}
}

func TestResponseDecoder_XMLTreeShape(t *testing.T) {
	got := responseDecoder{mode: DecodeXML}.decode("text/xml", []byte(ratesXML))
	tree, ok := got.body.(map[string]any)
	require.True(t, ok)

	assert.Equal(t, "GetRatesResponse", tree["tag"])
	assert.Equal(t, "urn:acme:rates", tree["ns"])
	assert.Equal(t, "", tree["text"])
	assert.Equal(t, map[string]any{}, tree["attrs"])

	children, ok := tree["children"].([]any)
	require.True(t, ok)
	require.Len(t, children, 1)
	rate, ok := children[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Rate", rate["tag"])
	assert.Equal(t, "0.92", rate["text"])
	assert.Equal(t, map[string]any{"currency": "EUR"}, rate["attrs"])
	assert.Equal(t, []any{}, rate["children"], "a leaf has an empty child list, not a missing one")
}

func TestResponseDecoder_ParseFailureKeepsTheBodyAndSaysWhy(t *testing.T) {
	got := responseDecoder{mode: DecodeXML}.decode("text/xml", []byte("<p>not a document"))
	assert.Equal(t, "<p>not a document", got.body, "an unparseable body is still handed back")
	assert.Contains(t, got.note, "Could not read the response as XML")
	assert.Contains(t, got.note, "returned as text instead")
}

// TestResponseDecoder_ForcedJSONSaysWhyItIsText is #1763: decode=json on a
// body that is not JSON answered the raw text and no hint at all, so a caller
// that asked for a JSON reading was handed a string and told nothing. The
// forced XML read has always said so; both forced modes now do.
func TestResponseDecoder_ForcedJSONSaysWhyItIsText(t *testing.T) {
	got := responseDecoder{mode: DecodeJSON}.decode("text/xml", []byte(ratesXML))
	assert.Equal(t, ratesXML, got.body, "an unparseable body is still handed back")
	assert.False(t, got.json)
	assert.Contains(t, got.note, "Could not read the response as JSON")
	assert.Contains(t, got.note, "returned as text instead")
	assert.Contains(t, got.note, "decode=xml", "the hint names the mode that reads this document")
}

// TestResponseDecoder_AutoJSONFallbackStaysSilent is the other half: a
// response that declares JSON and is not JSON has come back as a string since
// the gateway shipped, on a path nobody asked to parse, and a note on every
// one of those would be noise.
func TestResponseDecoder_AutoJSONFallbackStaysSilent(t *testing.T) {
	for _, mode := range []string{"", DecodeAuto} {
		got := responseDecoder{mode: mode}.decode("application/json", []byte("<html>error</html>"))
		assert.Equal(t, "<html>error</html>", got.body)
		assert.Empty(t, got.note, "mode %q", mode)
	}
}

// TestResponseDecoder_ForcedJSONThatParsesCarriesNoNote holds the note to the
// failure: a body the caller asked to read as JSON and that is JSON is the
// ordinary path, and says nothing.
func TestResponseDecoder_ForcedJSONThatParsesCarriesNoNote(t *testing.T) {
	got := responseDecoder{mode: DecodeJSON}.decode("text/plain", []byte(`{"a":1}`))
	assert.True(t, got.json)
	assert.Empty(t, got.note)
}

func TestResponseDecoder_OversizeDocumentSteersToExport(t *testing.T) {
	huge := "<a>" + string(make([]byte, xmlOversizeProbeBytes)) + "</a>"
	got := responseDecoder{mode: DecodeXML}.decode("text/xml", []byte(huge))
	assert.Contains(t, got.note, "api_export")
}

// xmlOversizeProbeBytes is one byte past the decoder's document limit.
const xmlOversizeProbeBytes = (8 << 20) + 1

func TestResponseDecoder_EmptyBody(t *testing.T) {
	got := responseDecoder{mode: DecodeXML}.decode("text/xml", nil)
	assert.Nil(t, got.body)
	assert.Empty(t, got.note)
}

// TestDecodeSchemaEnumMatchesTheConstants is the gate that replaced a check
// inside the handler. The schema's enum is the only thing refusing an unknown
// mode -- the SDK validates tools/call arguments against it before the handler
// runs -- so a constant added here and not there would be a mode the toolkit
// implements and no caller can send, and one there and not here would reach
// effectiveMode as an unhandled value and be read as text.
func TestDecodeSchemaEnumMatchesTheConstants(t *testing.T) {
	var schema struct {
		Properties struct {
			Decode struct {
				Enum []string `json:"enum"`
			} `json:"decode"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(invokeEndpointSchema, &schema))
	assert.Equal(t, []string{DecodeAuto, DecodeJSON, DecodeXML, DecodeText}, schema.Properties.Decode.Enum)
}

func TestIsXMLMediaType(t *testing.T) {
	for _, mt := range []string{"text/xml", "application/xml", "application/soap+xml", "application/atom+xml"} {
		assert.True(t, isXMLMediaType(mt), mt)
	}
	for _, mt := range []string{"", "application/json", "text/plain", "text/html", "application/xml-dtd"} {
		assert.False(t, isXMLMediaType(mt), mt)
	}
}

func TestParseMediaType(t *testing.T) {
	assert.Equal(t, "text/xml", parseMediaType("Text/XML; charset=utf-8"))
	assert.Equal(t, "", parseMediaType(""))
	assert.Equal(t, "", parseMediaType("not a media type at all ;;;"))
}

// specWithResponse builds a one-operation catalog whose success response
// declares the given media types.
func specWithResponse(t *testing.T, path, method, status string, mediaTypes ...string) map[string]*specState {
	t.Helper()
	content := openapi3.Content{}
	for _, mt := range mediaTypes {
		content[mt] = openapi3.NewMediaType()
	}
	resp := openapi3.NewResponse().WithDescription("ok")
	resp.Content = content
	responses := openapi3.NewResponses()
	responses.Set(status, &openapi3.ResponseRef{Value: resp})
	op := &openapi3.Operation{Responses: responses}

	item := &openapi3.PathItem{}
	switch method {
	case "POST":
		item.Post = op
	default:
		item.Get = op
	}
	doc := &openapi3.T{Paths: openapi3.NewPaths()}
	doc.Paths.Set(path, item)
	return map[string]*specState{"main": {doc: doc}}
}

func TestResolveDeclaresXMLResponse(t *testing.T) {
	tests := []struct {
		name   string
		specs  map[string]*specState
		method string
		path   string
		want   bool
	}{
		{"no catalog", nil, "POST", "/soap", false},
		{
			"soap 1.1 response", specWithResponse(t, "/soap", "POST", "200", "text/xml"),
			"POST", "/soap", true,
		},
		{
			"soap 1.2 response", specWithResponse(t, "/soap", "POST", "200", "application/soap+xml"),
			"POST", "/soap", true,
		},
		{
			"status range", specWithResponse(t, "/soap", "POST", "2XX", "application/xml"),
			"POST", "/soap", true,
		},
		{
			"default response when no success status is declared",
			specWithResponse(t, "/soap", "POST", "default", "text/xml"), "POST", "/soap", true,
		},
		{
			"json response", specWithResponse(t, "/v1/items", "GET", "200", "application/json"),
			"GET", "/v1/items", false,
		},
		{
			"query string does not defeat the match",
			specWithResponse(t, "/v1/items", "GET", "200", "application/xml"), "GET", "/v1/items?page=2", true,
		},
		{
			"lower-case method", specWithResponse(t, "/soap", "POST", "200", "text/xml"),
			"post", "/soap", true,
		},
		{
			"another path", specWithResponse(t, "/soap", "POST", "200", "text/xml"),
			"POST", "/other", false,
		},
		{
			"another method", specWithResponse(t, "/soap", "POST", "200", "text/xml"),
			"GET", "/soap", false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolveDeclaresXMLResponse(tt.specs, tt.method, tt.path))
		})
	}
}

func TestResolveDeclaresXMLResponse_ErrorStatusDoesNotCount(t *testing.T) {
	// A JSON API that answers a fault in XML declares it on 500; decoding
	// every 200 as XML because of that would be wrong.
	specs := specWithResponse(t, "/v1/items", "GET", "200", "application/json")
	specs["main"].doc.Paths.Value("/v1/items").Get.Responses.Set("500",
		&openapi3.ResponseRef{Value: openapi3.NewResponse().WithDescription("fault").WithContent(
			openapi3.Content{"text/xml": openapi3.NewMediaType()})})
	assert.False(t, resolveDeclaresXMLResponse(specs, "GET", "/v1/items"))
}

// soapOpSpec is a catalog whose one operation answers in XML, which is what
// auto mode keys on.
const soapOpSpec = `
openapi: 3.0.3
info:
  title: Rates
  version: "1.0"
paths:
  /soap/rates:
    post:
      operationId: getRates
      responses:
        "200":
          description: ok
          content:
            text/xml:
              schema:
                type: string
`

// runInvokeAgainstXML drives the real invoke path against an upstream that
// answers with an XML document, and returns the tool output. It is the
// end-to-end half: the unit tests above prove the decoder decides correctly,
// this proves the decision reaches the response the caller is handed.
func runInvokeAgainstXML(t *testing.T, specYAML, respContentType, respBody string, in InvokeInput) InvokeOutput {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", respContentType)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)

	cfg := Config{
		BaseURL:          srv.URL,
		AuthMode:         AuthModeNone,
		ConnectTimeout:   2 * time.Second,
		CallTimeout:      5 * time.Second,
		MaxResponseBytes: DefaultMaxResponseBytes,
	}
	auth, err := NewAuthenticator(cfg)
	require.NoError(t, err)

	inv := invocation{cfg: cfg, auth: auth, client: newHTTPClient(cfg)}
	if specYAML != "" {
		inv.specs = map[string]*specState{"main": mustParseSpec(t, specYAML)}
	}
	out, err := invoke(context.Background(), inv, in)
	require.NoError(t, err)
	return out
}

func TestInvoke_EndToEnd_CatalogDeclaredXMLDecodes(t *testing.T) {
	out := runInvokeAgainstXML(t, soapOpSpec, "text/xml", ratesXML,
		InvokeInput{Connection: "x", Method: "POST", Path: "/soap/rates"})

	tree, ok := out.Body.(map[string]any)
	require.True(t, ok, "got %#v; want an XML tree", out.Body)
	assert.Equal(t, "GetRatesResponse", tree["tag"])
	assert.Equal(t, int64(len(ratesXML)), out.BodyBytes, "body_bytes still reports what came off the wire")
	assert.Empty(t, out.Hint)
}

func TestInvoke_EndToEnd_NoCatalogKeepsTheStringBody(t *testing.T) {
	out := runInvokeAgainstXML(t, "", "text/xml", ratesXML,
		InvokeInput{Connection: "x", Method: "POST", Path: "/soap/rates"})
	assert.Equal(t, ratesXML, out.Body, "a connection with no catalog reads as it always has")
}

func TestInvoke_EndToEnd_DecodeXMLOnAnUncatalogedCall(t *testing.T) {
	out := runInvokeAgainstXML(t, "", "text/xml", ratesXML,
		InvokeInput{Connection: "x", Method: "POST", Path: "/soap/rates", Decode: DecodeXML})
	tree, ok := out.Body.(map[string]any)
	require.True(t, ok, "got %#v; want an XML tree", out.Body)
	assert.Equal(t, "GetRatesResponse", tree["tag"])
}

func TestInvoke_EndToEnd_DecodeFailureCarriesTheReason(t *testing.T) {
	out := runInvokeAgainstXML(t, "", "text/html", "<p>gateway timeout",
		InvokeInput{Connection: "x", Method: "GET", Path: "/status", Decode: DecodeXML})
	assert.Equal(t, "<p>gateway timeout", out.Body)
	assert.Contains(t, out.Hint, "Could not read the response as XML")
}

// TestInvoke_EndToEnd_DecodeJSONFailureCarriesTheReason is #1763 through the
// tool's own output: the reason reaches the caller in `hint`, beside the text
// body, exactly as the XML one does.
func TestInvoke_EndToEnd_DecodeJSONFailureCarriesTheReason(t *testing.T) {
	out := runInvokeAgainstXML(t, "", "text/xml", ratesXML,
		InvokeInput{Connection: "x", Method: "POST", Path: "/soap/rates", Decode: DecodeJSON})
	assert.Equal(t, ratesXML, out.Body)
	assert.Contains(t, out.Hint, "Could not read the response as JSON")
}

// TestInvoke_EndToEnd_AutoJSONFallbackCarriesNoHint keeps auto's silence on
// the path it has always taken.
func TestInvoke_EndToEnd_AutoJSONFallbackCarriesNoHint(t *testing.T) {
	out := runInvokeAgainstXML(t, "", "application/json", "<p>gateway timeout",
		InvokeInput{Connection: "x", Method: "GET", Path: "/status"})
	assert.Equal(t, "<p>gateway timeout", out.Body)
	assert.Empty(t, out.Hint)
}

func TestInvoke_EndToEnd_XMLTreeIsNotProbedForPageCursors(t *testing.T) {
	// A document whose root element is named `next` would land on the
	// pagination probe's `next` key if an XML tree were handed to it.
	out := runInvokeAgainstXML(t, "", "text/xml", `<next>https://example.com/p2</next>`,
		InvokeInput{Connection: "x", Method: "GET", Path: "/feed", Decode: DecodeXML})
	assert.Nil(t, out.Pagination, "a decoded XML tree is not a JSON page envelope")
}

func TestInvoke_EndToEnd_JSONStillDetectsPagination(t *testing.T) {
	out := runInvokeAgainstXML(t, "", "application/json", `{"next":"https://example.com/p2"}`,
		InvokeInput{Connection: "x", Method: "GET", Path: "/feed"})
	require.NotNil(t, out.Pagination, "the JSON probe is unchanged")
	assert.True(t, out.Pagination.HasMore)
}

// TestHandleInvoke_DecodeWithPaginateIsRefused states the one combination that
// has no meaning. A walk returns a collection assembled from every page's own
// body, so a decode mode has no single response to apply to; accepting the
// argument and ignoring it would be the worse answer.
func TestHandleInvoke_DecodeWithPaginateIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("the upstream must not be contacted for a refused call")
	}))
	t.Cleanup(srv.Close)

	tk := New("test")
	require.NoError(t, tk.AddConnection("self", map[string]any{
		"base_url": srv.URL, "auth_mode": AuthModeNone,
	}))

	for _, mode := range []string{DecodeXML, DecodeJSON, DecodeText} {
		t.Run(mode, func(t *testing.T) {
			res, _, err := tk.handleInvoke(context.Background(), nil, InvokeInput{
				Connection: "self", Method: "GET", Path: "/things", Decode: mode,
				Paginate: &PaginateInput{Items: "data"},
			})
			require.NoError(t, err)
			require.True(t, res.IsError, "decode=%s with paginate was accepted", mode)
			assert.Contains(t, textContent(res), "decode is not available with paginate")
		})
	}
}
