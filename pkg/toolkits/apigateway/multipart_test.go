package apigateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// multipartOpSpec declares an operation whose only requestBody media
// type is multipart/form-data — the shape of the US Census batch
// geocoder and of the platform's own catalog-spec upload route, the two
// operations issue #1296 reports as unreachable.
const multipartOpSpec = `
openapi: 3.0.3
info:
  title: Test API
  version: "1.0"
paths:
  /geocoder/addressbatch:
    post:
      operationId: geocodeBatch
      requestBody:
        required: true
        content:
          multipart/form-data:
            schema:
              type: object
              properties:
                addressFile:
                  type: string
                  format: binary
                benchmark:
                  type: string
      responses:
        "200":
          description: ok
`

// maxTestUploadBytes bounds the body the end-to-end handler will read
// and buffer. The fixture is a few hundred bytes; the cap exists so the
// test handler models what a real one must do rather than parsing an
// unbounded body.
const maxTestUploadBytes = 1 << 20

// decodedPart is one part read back off an assembled multipart body.
type decodedPart struct {
	name        string
	filename    string
	contentType string
	body        string
}

// readParts parses a multipart body the way an upstream server does,
// which is the only assertion that proves the bytes are well-formed:
// a hand-rolled byte comparison would pass on a body no parser accepts.
func readParts(t *testing.T, contentType string, data []byte) []decodedPart {
	t.Helper()
	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatalf("parsing Content-Type %q: %v", contentType, err)
	}
	if mt != multipartFormData {
		t.Fatalf("media type = %q; want %s", mt, multipartFormData)
	}
	if params["boundary"] == "" {
		t.Fatal("Content-Type carries no boundary")
	}
	r := multipart.NewReader(bytes.NewReader(data), params["boundary"])
	var parts []decodedPart
	for {
		p, perr := r.NextPart()
		if perr == io.EOF {
			return parts
		}
		if perr != nil {
			t.Fatalf("reading part: %v", perr)
		}
		body, rerr := io.ReadAll(p)
		if rerr != nil {
			t.Fatalf("reading part body: %v", rerr)
		}
		parts = append(parts, decodedPart{
			name:        p.FormName(),
			filename:    p.FileName(),
			contentType: p.Header.Get("Content-Type"),
			body:        string(body),
		})
	}
}

// findPart returns the first part with the given field name.
func findPart(t *testing.T, parts []decodedPart, name string) decodedPart {
	t.Helper()
	for _, p := range parts {
		if p.name == name {
			return p
		}
	}
	t.Fatalf("no part named %q in %+v", name, parts)
	return decodedPart{}
}

func TestIsMultipartFormData(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"multipart/form-data", true},
		{"multipart/form-data; boundary=abc123", true},
		{"MULTIPART/FORM-DATA", true},
		{"  multipart/form-data ; boundary=x ", true},
		{"multipart/form-data; boundary", true}, // unparseable params, media type still recognized
		{"multipart/mixed", false},
		{"application/json", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isMultipartFormData(c.in); got != c.want {
			t.Errorf("isMultipartFormData(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}

// TestEncodeMultipartBody_FieldsAndFile covers the shape the issue
// asks for: scalars become text fields, an object naming a filename
// becomes a file part, and the gateway supplies the boundary.
func TestEncodeMultipartBody_FieldsAndFile(t *testing.T) {
	enc, err := encodeMultipartBody(map[string]any{
		"addressFile": map[string]any{
			"filename":     "batch.csv",
			"content_type": "text/csv",
			"content":      "1,123 Main St,Springfield,IL,62701\n",
		},
		"benchmark": "Public_AR_Current",
		"vintage":   float64(4),
		"strict":    true,
	})
	if err != nil {
		t.Fatalf("encodeMultipartBody: %v", err)
	}
	if !enc.authoritative {
		t.Error("multipart encoding must be authoritative over a caller Content-Type")
	}
	parts := readParts(t, enc.contentType, enc.data)
	if len(parts) != 4 {
		t.Fatalf("parts = %d; want 4: %+v", len(parts), parts)
	}
	file := findPart(t, parts, "addressFile")
	if file.filename != "batch.csv" {
		t.Errorf("filename = %q; want batch.csv", file.filename)
	}
	if file.contentType != "text/csv" {
		t.Errorf("part content-type = %q; want text/csv", file.contentType)
	}
	if file.body != "1,123 Main St,Springfield,IL,62701\n" {
		t.Errorf("file part body = %q", file.body)
	}
	if got := findPart(t, parts, "benchmark").body; got != "Public_AR_Current" {
		t.Errorf("benchmark = %q", got)
	}
	// float64 is what the JSON decoder produces for every number, so a
	// whole number must not reach the upstream as "4e+00".
	if got := findPart(t, parts, "vintage").body; got != "4" {
		t.Errorf("vintage = %q; want 4", got)
	}
	if got := findPart(t, parts, "strict").body; got != "true" {
		t.Errorf("strict = %q; want true", got)
	}
}

// TestEncodeMultipartBody_DeterministicOrder proves field order does
// not vary with Go's randomized map iteration, so the same body always
// produces the same sequence of parts.
func TestEncodeMultipartBody_DeterministicOrder(t *testing.T) {
	body := map[string]any{"c": "3", "a": "1", "b": "2"}
	for range 8 {
		enc, err := encodeMultipartBody(body)
		if err != nil {
			t.Fatalf("encodeMultipartBody: %v", err)
		}
		parts := readParts(t, enc.contentType, enc.data)
		names := make([]string, 0, len(parts))
		for _, p := range parts {
			names = append(names, p.name)
		}
		if strings.Join(names, ",") != "a,b,c" {
			t.Fatalf("part order = %v; want a,b,c", names)
		}
	}
}

// TestEncodeMultipartBody_Base64File proves the binary convention: a
// content_base64 attribute is decoded to raw bytes, and a file part
// with no declared type defaults to application/octet-stream.
func TestEncodeMultipartBody_Base64File(t *testing.T) {
	raw := []byte{0x00, 0x01, 0xff, 0xfe, 'h', 'i'}
	enc, err := encodeMultipartBody(map[string]any{
		"file": map[string]any{
			"filename":       "blob.bin",
			"content_base64": base64.StdEncoding.EncodeToString(raw),
		},
	})
	if err != nil {
		t.Fatalf("encodeMultipartBody: %v", err)
	}
	part := findPart(t, readParts(t, enc.contentType, enc.data), "file")
	if part.body != string(raw) {
		t.Errorf("decoded body = %q; want the raw bytes", part.body)
	}
	if part.contentType != octetStream {
		t.Errorf("part content-type = %q; want %s", part.contentType, octetStream)
	}
}

// TestEncodeMultipartBody_UnpaddedBase64 accepts the unpadded standard
// alphabet, which models emit interchangeably with the padded one.
func TestEncodeMultipartBody_UnpaddedBase64(t *testing.T) {
	enc, err := encodeMultipartBody(map[string]any{
		"file": map[string]any{
			"filename":       "a.txt",
			"content_base64": base64.RawStdEncoding.EncodeToString([]byte("hello")),
		},
	})
	if err != nil {
		t.Fatalf("encodeMultipartBody: %v", err)
	}
	if got := findPart(t, readParts(t, enc.contentType, enc.data), "file").body; got != "hello" {
		t.Errorf("body = %q; want hello", got)
	}
}

// TestEncodeMultipartBody_TypedFieldWithoutFilename covers the part
// descriptor that names a type but no filename: a JSON metadata field
// sent alongside a file, which several upstreams require.
func TestEncodeMultipartBody_TypedFieldWithoutFilename(t *testing.T) {
	enc, err := encodeMultipartBody(map[string]any{
		"metadata": map[string]any{
			"content_type": applicationJSON,
			"content":      `{"title":"report"}`,
		},
	})
	if err != nil {
		t.Fatalf("encodeMultipartBody: %v", err)
	}
	part := findPart(t, readParts(t, enc.contentType, enc.data), "metadata")
	if part.filename != "" {
		t.Errorf("filename = %q; want none", part.filename)
	}
	if part.contentType != applicationJSON {
		t.Errorf("part content-type = %q; want %s", part.contentType, applicationJSON)
	}
	if part.body != `{"title":"report"}` {
		t.Errorf("body = %q", part.body)
	}
}

// TestEncodeMultipartBody_ArrayRepeatsField proves an array value
// becomes one part per element under the same field name, which is how
// an upstream taking several files under one name expects them.
func TestEncodeMultipartBody_ArrayRepeatsField(t *testing.T) {
	enc, err := encodeMultipartBody(map[string]any{
		"tag": []any{"a", "b"},
		"files": []any{
			map[string]any{"filename": "one.txt", "content": "1"},
			map[string]any{"filename": "two.txt", "content": "2"},
		},
	})
	if err != nil {
		t.Fatalf("encodeMultipartBody: %v", err)
	}
	parts := readParts(t, enc.contentType, enc.data)
	var tags, filenames []string
	for _, p := range parts {
		switch p.name {
		case "tag":
			tags = append(tags, p.body)
		case "files":
			filenames = append(filenames, p.filename)
		}
	}
	if strings.Join(tags, ",") != "a,b" {
		t.Errorf("tag parts = %v; want [a b]", tags)
	}
	if strings.Join(filenames, ",") != "one.txt,two.txt" {
		t.Errorf("file parts = %v; want [one.txt two.txt]", filenames)
	}
}

// TestEncodeMultipartBody_NestedObjectAsJSON proves an object that is
// not a part descriptor is JSON-encoded into a text field rather than
// silently dropped.
func TestEncodeMultipartBody_NestedObjectAsJSON(t *testing.T) {
	enc, err := encodeMultipartBody(map[string]any{
		"options": map[string]any{"strict": true},
	})
	if err != nil {
		t.Fatalf("encodeMultipartBody: %v", err)
	}
	if got := findPart(t, readParts(t, enc.contentType, enc.data), "options").body; got != `{"strict":true}` {
		t.Errorf("options = %q; want the JSON encoding", got)
	}
}

// TestEncodeMultipartBody_NilFieldSkipped mirrors query-string
// assembly, where a null value adds nothing.
func TestEncodeMultipartBody_NilFieldSkipped(t *testing.T) {
	enc, err := encodeMultipartBody(map[string]any{"a": nil, "b": "1"})
	if err != nil {
		t.Fatalf("encodeMultipartBody: %v", err)
	}
	parts := readParts(t, enc.contentType, enc.data)
	if len(parts) != 1 || parts[0].name != "b" {
		t.Errorf("parts = %+v; want only b", parts)
	}
}

func TestEncodeMultipartBody_Errors(t *testing.T) {
	cases := []struct {
		name    string
		body    any
		wantSub string
	}{
		{
			name:    "string body",
			body:    "--boundary\r\nContent-Disposition: form-data\r\n\r\nx\r\n--boundary--",
			wantSub: "body must be an object of form fields",
		},
		{
			name:    "array body",
			body:    []any{"a"},
			wantSub: "body must be an object of form fields",
		},
		{
			name:    "descriptor with no bytes",
			body:    map[string]any{"f": map[string]any{"filename": "a.txt"}},
			wantSub: "carries no bytes",
		},
		{
			// A part descriptor is not a passthrough object: keeping
			// "sha256" would send a part whose checksum the caller
			// believes traveled with it and which was in fact dropped.
			name:    "unknown attribute on a descriptor",
			body:    map[string]any{"f": map[string]any{"filename": "a.txt", "content": "x", "sha256": "deadbeef"}},
			wantSub: `unknown part attribute(s) sha256`,
		},
		{
			name:    "misspelled attribute on a descriptor",
			body:    map[string]any{"f": map[string]any{"file_name": "a.txt", "content": "x"}},
			wantSub: `unknown part attribute(s) file_name`,
		},
		{
			name:    "bad descriptor inside an array",
			body:    map[string]any{"f": []any{map[string]any{"filename": "a.txt"}}},
			wantSub: "carries no bytes",
		},
		{
			name: "both content forms",
			body: map[string]any{"f": map[string]any{
				"filename": "a.txt", "content": "x", "content_base64": "eA==",
			}},
			wantSub: "supply exactly one",
		},
		{
			name:    "content not a string",
			body:    map[string]any{"f": map[string]any{"filename": "a.txt", "content": 7}},
			wantSub: "content must be a string",
		},
		{
			name:    "content_base64 not a string",
			body:    map[string]any{"f": map[string]any{"filename": "a.txt", "content_base64": 7}},
			wantSub: "content_base64 must be a string",
		},
		{
			name:    "invalid base64",
			body:    map[string]any{"f": map[string]any{"filename": "a.txt", "content_base64": "not base64!!"}},
			wantSub: "not valid base64",
		},
		{
			name:    "filename not a string",
			body:    map[string]any{"f": map[string]any{"filename": 7, "content": "x"}},
			wantSub: "filename must be a string",
		},
		{
			name:    "line break in filename",
			body:    map[string]any{"f": map[string]any{"filename": "a\r\nX-Evil: 1", "content": "x"}},
			wantSub: "must not contain a line break",
		},
		{
			name:    "line break in content_type",
			body:    map[string]any{"f": map[string]any{"content_type": "text/csv\r\nX-Evil: 1", "content": "x"}},
			wantSub: "must not contain a line break",
		},
		{
			name:    "line break in field name",
			body:    map[string]any{"f\r\nX-Evil: 1": "x"},
			wantSub: "must not contain a line break",
		},
		{
			name:    "unencodable nested value",
			body:    map[string]any{"opts": map[string]any{"ch": make(chan int)}},
			wantSub: "encoding multipart field",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := encodeMultipartBody(c.body)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error = %v; want it to mention %q", err, c.wantSub)
			}
		})
	}
}

// TestEncodeMultipartBody_EscapesQuotes proves a quote in a field name
// or filename is escaped rather than closing the quoted-string
// parameter early.
func TestEncodeMultipartBody_EscapesQuotes(t *testing.T) {
	enc, err := encodeMultipartBody(map[string]any{
		`od"d`: map[string]any{"filename": `a"b.txt`, "content": "x"},
	})
	if err != nil {
		t.Fatalf("encodeMultipartBody: %v", err)
	}
	part := findPart(t, readParts(t, enc.contentType, enc.data), `od"d`)
	if part.filename != `a"b.txt` {
		t.Errorf("filename = %q; want a\"b.txt", part.filename)
	}
}

// TestEncodeBody_CatalogMultipart_ObjectBody proves the catalog drives
// selection: the operation declares multipart/form-data, so an object
// body is assembled by the multipart encoder rather than JSON-marshaled.
func TestEncodeBody_CatalogMultipart_ObjectBody(t *testing.T) {
	enc, err := encodeBody("POST", map[string]any{"benchmark": "Public_AR_Current"},
		[]string{multipartFormData}, nil)
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if !strings.HasPrefix(enc.contentType, multipartFormData+"; boundary=") {
		t.Fatalf("content-type = %q; want a multipart type with a boundary", enc.contentType)
	}
	if got := findPart(t, readParts(t, enc.contentType, enc.data), "benchmark").body; got != "Public_AR_Current" {
		t.Errorf("benchmark = %q", got)
	}
}

// TestEncodeBody_CatalogMultipart_NonObjectRefused is the honest-failure
// half of issue #1296: a body the gateway cannot encode as multipart is
// refused at the encoder with a message naming the shape it wants,
// instead of going out malformed and returning a confusing upstream 400.
func TestEncodeBody_CatalogMultipart_NonObjectRefused(t *testing.T) {
	_, err := encodeBody("POST", "--b\r\nContent-Disposition: form-data\r\n\r\nx\r\n--b--",
		[]string{multipartFormData}, nil)
	if err == nil {
		t.Fatal("want a refusal for a string body on a multipart operation")
	}
	for _, want := range []string{"multipart/form-data", "object of form fields", "filename", "content_base64"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q: %v", want, err)
		}
	}
}

// TestEncodeBody_CallerPinnedMultipart_ObjectBody covers the caller who
// sets Content-Type by hand. Before #1296 that pin routed an object
// body to json.Marshal, producing JSON under a multipart header; the
// gateway now encodes it and owns the boundary.
func TestEncodeBody_CallerPinnedMultipart_ObjectBody(t *testing.T) {
	enc, err := encodeBody("POST", map[string]any{"a": "1"}, nil,
		map[string]string{"content-type": "multipart/form-data; boundary=caller-chose-this"})
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if !enc.authoritative {
		t.Error("gateway-assembled multipart must override the caller's header")
	}
	if strings.Contains(enc.contentType, "caller-chose-this") {
		t.Errorf("content-type = %q; the boundary must be the gateway's", enc.contentType)
	}
	if got := findPart(t, readParts(t, enc.contentType, enc.data), "a").body; got != "1" {
		t.Errorf("a = %q", got)
	}
}

// TestEncodeBody_CallerPinnedMultipart_StringBody proves the verbatim
// escape hatch is unchanged: a caller who pins the full Content-Type
// (boundary included) and hands raw bytes still gets them through
// untouched, and the gateway does not claim authority over the header.
func TestEncodeBody_CallerPinnedMultipart_StringBody(t *testing.T) {
	raw := "--b\r\nContent-Disposition: form-data; name=\"a\"\r\n\r\n1\r\n--b--\r\n"
	enc, err := encodeBody("POST", raw, nil,
		map[string]string{"Content-Type": "multipart/form-data; boundary=b"})
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if enc.authoritative {
		t.Error("a verbatim string body must not override the caller's Content-Type")
	}
	if string(enc.data) != raw {
		t.Errorf("body altered: %q", string(enc.data))
	}
}

// TestEncodeBody_CatalogMultipartAndJSON_PrefersJSON confirms the
// existing preference is untouched: an operation declaring both takes
// the encoding the gateway can build from any body shape.
func TestEncodeBody_CatalogMultipartAndJSON_PrefersJSON(t *testing.T) {
	enc, err := encodeBody("POST", map[string]any{"a": "1"},
		[]string{applicationJSON, multipartFormData}, nil)
	if err != nil {
		t.Fatalf("encodeBody: %v", err)
	}
	if enc.contentType != applicationJSON {
		t.Errorf("content-type = %q; want %s", enc.contentType, applicationJSON)
	}
}

// TestBuildRequest_AuthoritativeContentTypeOverrides proves the request
// builder honors the flag: without it the caller's header would stand
// and the boundary would not match the bytes.
func TestBuildRequest_AuthoritativeContentTypeOverrides(t *testing.T) {
	req, err := buildRequest(context.Background(), requestSpec{
		method:        http.MethodPost,
		url:           "http://example.invalid/x",
		body:          []byte("--gw--\r\n"),
		contentType:   multipartFormData + "; boundary=gw",
		authoritative: true,
		headers:       map[string]string{"Content-Type": multipartFormData + "; boundary=caller"},
	})
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}
	if got := req.Header.Get(headerContentType); got != multipartFormData+"; boundary=gw" {
		t.Errorf("Content-Type = %q; want the gateway's boundary", got)
	}
}

// TestInvoke_EndToEnd_Multipart is the integration proof for issue
// #1296: a real invoke() against a real HTTP server, whose handler
// parses the request with net/http's own multipart parser. That parser
// is what returned "Required part 'addressFile' is not present" for the
// hand-built body in the issue, so a green assertion here is the
// evidence the gap is closed.
func TestInvoke_EndToEnd_Multipart(t *testing.T) {
	var (
		gotBenchmark string
		gotFilename  string
		gotFile      string
		gotPartType  string
		parseErr     error
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Bound the read before parsing, the same way a real handler
		// must: without it the form parser will buffer whatever the
		// client sends.
		r.Body = http.MaxBytesReader(w, r.Body, maxTestUploadBytes)
		if parseErr = r.ParseMultipartForm(maxTestUploadBytes); parseErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotBenchmark = r.FormValue("benchmark")
		file, header, ferr := r.FormFile("addressFile")
		if ferr != nil {
			parseErr = ferr
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer func() { _ = file.Close() }()
		gotFilename = header.Filename
		gotPartType = header.Header.Get("Content-Type")
		content, rerr := io.ReadAll(file)
		if rerr != nil {
			parseErr = rerr
		}
		gotFile = string(content)
		w.Header().Set("Content-Type", applicationJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
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
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	out, err := invoke(context.Background(), invocation{
		cfg: cfg, auth: auth, client: newHTTPClient(cfg),
		specs: map[string]*specState{"main": mustParseSpec(t, multipartOpSpec)},
	}, InvokeInput{
		Connection: "x", Method: http.MethodPost, Path: "/geocoder/addressbatch",
		Body: map[string]any{
			"addressFile": map[string]any{
				"filename":     "batch.csv",
				"content_type": "text/csv",
				"content":      "1,123 Main St,Springfield,IL,62701\n",
			},
			"benchmark": "Public_AR_Current",
		},
	})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if parseErr != nil {
		t.Fatalf("upstream could not parse the multipart body: %v", parseErr)
	}
	if out.Status != http.StatusOK {
		t.Fatalf("status = %d; want 200", out.Status)
	}
	if gotBenchmark != "Public_AR_Current" {
		t.Errorf("benchmark = %q", gotBenchmark)
	}
	if gotFilename != "batch.csv" {
		t.Errorf("filename = %q; want batch.csv", gotFilename)
	}
	if gotPartType != "text/csv" {
		t.Errorf("part content-type = %q; want text/csv", gotPartType)
	}
	if gotFile != "1,123 Main St,Springfield,IL,62701\n" {
		t.Errorf("uploaded file = %q", gotFile)
	}
}
