package formdata

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
)

// encoded is one encoding, as the tests below read it.
type encoded struct {
	data          []byte
	contentType   string
	authoritative bool
}

// encodeBody runs Encode; every body it assembles is authoritative, since the
// boundary is generated here.
func encodeBody(body any) (encoded, error) {
	data, contentType, err := Encode(body)
	return encoded{data: data, contentType: contentType, authoritative: err == nil}, err
}

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
	if mt != MediaType {
		t.Fatalf("media type = %q; want %s", mt, MediaType)
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

func TestIs(t *testing.T) {
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
		if got := Is(c.in); got != c.want {
			t.Errorf("Is(%q) = %v; want %v", c.in, got, c.want)
		}
	}
}

// TestEncode_FieldsAndFile covers the shape the issue
// asks for: scalars become text fields, an object naming a filename
// becomes a file part, and the gateway supplies the boundary.
func TestEncode_FieldsAndFile(t *testing.T) {
	enc, err := encodeBody(map[string]any{
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

// TestEncode_DeterministicOrder proves field order does
// not vary with Go's randomized map iteration, so the same body always
// produces the same sequence of parts.
func TestEncode_DeterministicOrder(t *testing.T) {
	body := map[string]any{"c": "3", "a": "1", "b": "2"}
	for range 8 {
		enc, err := encodeBody(body)
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

// TestEncode_Base64File proves the binary convention: a
// content_base64 attribute is decoded to raw bytes, and a file part
// with no declared type defaults to application/octet-stream.
func TestEncode_Base64File(t *testing.T) {
	raw := []byte{0x00, 0x01, 0xff, 0xfe, 'h', 'i'}
	enc, err := encodeBody(map[string]any{
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

// TestEncode_UnpaddedBase64 accepts the unpadded standard
// alphabet, which models emit interchangeably with the padded one.
func TestEncode_UnpaddedBase64(t *testing.T) {
	enc, err := encodeBody(map[string]any{
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

// TestEncode_TypedFieldWithoutFilename covers the part
// descriptor that names a type but no filename: a JSON metadata field
// sent alongside a file, which several upstreams require.
func TestEncode_TypedFieldWithoutFilename(t *testing.T) {
	enc, err := encodeBody(map[string]any{
		"metadata": map[string]any{
			"content_type": "application/json",
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
	if part.contentType != "application/json" {
		t.Errorf("part content-type = %q; want %s", part.contentType, "application/json")
	}
	if part.body != `{"title":"report"}` {
		t.Errorf("body = %q", part.body)
	}
}

// TestEncode_ArrayRepeatsField proves an array value
// becomes one part per element under the same field name, which is how
// an upstream taking several files under one name expects them.
func TestEncode_ArrayRepeatsField(t *testing.T) {
	enc, err := encodeBody(map[string]any{
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

// TestEncode_NestedObjectAsJSON proves an object that is
// not a part descriptor is JSON-encoded into a text field rather than
// silently dropped.
func TestEncode_NestedObjectAsJSON(t *testing.T) {
	enc, err := encodeBody(map[string]any{
		"options": map[string]any{"strict": true},
	})
	if err != nil {
		t.Fatalf("encodeMultipartBody: %v", err)
	}
	if got := findPart(t, readParts(t, enc.contentType, enc.data), "options").body; got != `{"strict":true}` {
		t.Errorf("options = %q; want the JSON encoding", got)
	}
}

// TestEncode_NilFieldSkipped mirrors query-string
// assembly, where a null value adds nothing.
func TestEncode_NilFieldSkipped(t *testing.T) {
	enc, err := encodeBody(map[string]any{"a": nil, "b": "1"})
	if err != nil {
		t.Fatalf("encodeMultipartBody: %v", err)
	}
	parts := readParts(t, enc.contentType, enc.data)
	if len(parts) != 1 || parts[0].name != "b" {
		t.Errorf("parts = %+v; want only b", parts)
	}
}

func TestEncode_Errors(t *testing.T) {
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
			_, err := encodeBody(c.body)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error = %v; want it to mention %q", err, c.wantSub)
			}
		})
	}
}

// TestEncode_EscapesQuotes proves a quote in a field name
// or filename is escaped rather than closing the quoted-string
// parameter early.
func TestEncode_EscapesQuotes(t *testing.T) {
	enc, err := encodeBody(map[string]any{
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
