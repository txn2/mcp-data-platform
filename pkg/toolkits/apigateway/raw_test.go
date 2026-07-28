package apigateway

import (
	"net/http"
	"testing"
)

// TestCopyRawHeaders_ScriptableUpstreamIsNotRenderable pins the reason the raw
// path stopped forwarding the upstream's rendering headers: an upstream that
// calls its bytes text/html must not reproduce that document on the platform's
// origin.
func TestCopyRawHeaders_ScriptableUpstreamIsNotRenderable(t *testing.T) {
	in := http.Header{}
	in.Set("Content-Type", "text/html; charset=utf-8")
	in.Set("Content-Disposition", `inline; filename="page.html"`)

	sink := newCaptureSink()
	copyRawHeaders(in, sink)

	if got := sink.headers.Get("Content-Disposition"); got != `attachment; filename="page.html"` {
		t.Errorf("Content-Disposition = %q; want an attachment despite the upstream's inline", got)
	}
	if got := sink.headers.Get("Content-Security-Policy"); got != "default-src 'none'; sandbox" {
		t.Errorf("Content-Security-Policy = %q; want the sandbox policy", got)
	}
	if got := sink.headers.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q; want nosniff", got)
	}
	if got := sink.headers.Get("Content-Type"); got != "text/html" {
		t.Errorf("Content-Type = %q; want the parameter-free media type", got)
	}
}

// TestCopyRawHeaders_SanitizesUpstreamFilename asserts that the characters
// which would close the quoted filename parameter or start a second header
// cannot survive an upstream Content-Disposition, and that exactly one
// Content-Disposition reaches the sink whatever the upstream sent.
func TestCopyRawHeaders_SanitizesUpstreamFilename(t *testing.T) {
	tests := []struct {
		name        string
		disposition string
		want        string
	}{
		{
			name:        "quote closes the parameter",
			disposition: `attachment; filename="re\"port.pdf"`,
			want:        `attachment; filename="re_port.pdf"`,
		},
		{
			name:        "backslash escapes the closing quote",
			disposition: `attachment; filename="re\\port.pdf"`,
			want:        `attachment; filename="re_port.pdf"`,
		},
		{
			name:        "control characters are dropped",
			disposition: "attachment; filename=\"re\x01port.pdf\"",
			want:        `attachment; filename="report.pdf"`,
		},
		{
			name:        "path separators cannot steer a download",
			disposition: `attachment; filename="../../etc/passwd"`,
			want:        `attachment; filename=".._.._etc_passwd"`,
		},
		{
			name:        "an unparseable disposition contributes nothing",
			disposition: "inline; filename=\"a\"; x=\"b\"\r\nX-Injected: yes",
			want:        "inline",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := http.Header{}
			in.Set("Content-Type", "application/pdf")
			in.Set("Content-Disposition", tt.disposition)

			sink := newCaptureSink()
			copyRawHeaders(in, sink)

			if got := sink.headers.Values("Content-Disposition"); len(got) != 1 {
				t.Fatalf("Content-Disposition written %d times: %q; a second header is header injection", len(got), got)
			}
			if got := sink.headers.Get("Content-Disposition"); got != tt.want {
				t.Errorf("Content-Disposition = %q; want %q", got, tt.want)
			}
			if got := sink.headers.Values("X-Injected"); len(got) != 0 {
				t.Errorf("X-Injected reached the sink: %q", got)
			}
		})
	}
}

// TestCopyRawHeaders_ForwardsFramingAndValidators asserts the headers that
// carry no rendering decision still reach the client unchanged, so body
// framing and conditional requests keep working through the raw path.
func TestCopyRawHeaders_ForwardsFramingAndValidators(t *testing.T) {
	in := http.Header{}
	in.Set("Content-Type", "application/pdf")
	in.Set("Content-Length", "4096")
	in.Set("Content-Encoding", "gzip")
	in.Set("Content-Range", "bytes 0-4095/1048576")
	in.Set("Etag", `"v3"`)
	in.Set("Last-Modified", "Wed, 21 Oct 2026 07:28:00 GMT")

	sink := newCaptureSink()
	copyRawHeaders(in, sink)

	for name, want := range map[string]string{
		"Content-Length":   "4096",
		"Content-Encoding": "gzip",
		"Content-Range":    "bytes 0-4095/1048576",
		"Etag":             `"v3"`,
		"Last-Modified":    "Wed, 21 Oct 2026 07:28:00 GMT",
	} {
		if got := sink.headers.Get(name); got != want {
			t.Errorf("%s = %q; want %q", name, got, want)
		}
	}
	if got := sink.headers.Values("Content-Type"); len(got) != 1 {
		t.Errorf("Content-Type written %d times: %q; the derived value must replace the upstream's, not join it", len(got), got)
	}
	if got := sink.headers.Get("Accept-Ranges"); got != "" {
		t.Errorf("Accept-Ranges = %q; the route answers a POST and honors no transport-level Range, so it must not advertise one", got)
	}
	if got := sink.headers.Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q; a passive type with no upstream filename stays inline", got)
	}
}

// TestCopyRawHeaders_CacheControl pins the one default an upstream may
// override: a silent upstream leaves the response carrying no directive at
// all, so the platform states `private` itself.
func TestCopyRawHeaders_CacheControl(t *testing.T) {
	tests := []struct {
		name     string
		upstream string
		want     string
	}{
		{name: "silent upstream gets the platform default", upstream: "", want: "private"},
		{name: "upstream directive is honored", upstream: "public, max-age=600", want: "public, max-age=600"},
		{name: "no-store is honored", upstream: "no-store", want: "no-store"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := http.Header{}
			in.Set("Content-Type", "application/pdf")
			if tt.upstream != "" {
				in.Set("Cache-Control", tt.upstream)
			}

			sink := newCaptureSink()
			copyRawHeaders(in, sink)

			if got := sink.headers.Values("Cache-Control"); len(got) != 1 {
				t.Fatalf("Cache-Control written %d times: %q", len(got), got)
			}
			if got := sink.headers.Get("Cache-Control"); got != tt.want {
				t.Errorf("Cache-Control = %q; want %q", got, tt.want)
			}
		})
	}
}

// TestCopyRawHeaders_UndeclaredTypeIsOpaque asserts an upstream that names no
// type produces application/octet-stream rather than an unset header, which
// would let the HTTP writer sniff a type the upstream never declared.
func TestCopyRawHeaders_UndeclaredTypeIsOpaque(t *testing.T) {
	sink := newCaptureSink()
	copyRawHeaders(http.Header{}, sink)

	if got := sink.headers.Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q; want application/octet-stream", got)
	}
	if got := sink.headers.Get("Content-Disposition"); got != "inline" {
		t.Errorf("Content-Disposition = %q; want inline", got)
	}
}

// TestRawContentHeaders_MultipartByteRangesKeepsItsBoundary asserts the one
// Content-Type parameter the raw path preserves. A multi-range 206 is
// parseable only through its boundary, so stripping it would cost the caller a
// capability for no security gain — and a boundary that could not be emitted
// as a bare token is dropped rather than quoted.
func TestRawContentHeaders_MultipartByteRangesKeepsItsBoundary(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        string
	}{
		{
			name:        "token boundary is preserved",
			contentType: "multipart/byteranges; boundary=3d6b6a416f9b5",
			want:        "multipart/byteranges; boundary=3d6b6a416f9b5",
		},
		{
			name:        "a boundary carrying a quote is dropped",
			contentType: `multipart/byteranges; boundary="a\"b"`,
			want:        "multipart/byteranges",
		},
		{
			name:        "a boundary carrying a space is dropped",
			contentType: `multipart/byteranges; boundary="a b"`,
			want:        "multipart/byteranges",
		},
		{
			name:        "an absent boundary leaves the bare type",
			contentType: "multipart/byteranges",
			want:        "multipart/byteranges",
		},
		{
			name:        "no other type keeps its parameters",
			contentType: "multipart/form-data; boundary=3d6b6a416f9b5",
			want:        "multipart/form-data",
		},
		{
			name:        "charset is still dropped",
			contentType: "text/csv; charset=utf-16",
			want:        "text/csv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := http.Header{}
			in.Set("Content-Type", tt.contentType)

			got := rawContentHeaders(in).Get("Content-Type")

			if got != tt.want {
				t.Errorf("Content-Type = %q; want %q", got, tt.want)
			}
		})
	}
}

// TestRawContentOptions asserts what the raw path recovers from an upstream
// response before handing it to blobserve.
func TestRawContentOptions(t *testing.T) {
	tests := []struct {
		name            string
		contentType     string
		disposition     string
		wantName        string
		wantAttachment  bool
		wantContentType string
	}{
		{
			name:            "attachment intent and filename are recovered",
			contentType:     "application/pdf",
			disposition:     `attachment; filename="report.pdf"`,
			wantName:        "report.pdf",
			wantAttachment:  true,
			wantContentType: "application/pdf",
		},
		{
			name:            "case is not significant to the intent",
			contentType:     "application/pdf",
			disposition:     `ATTACHMENT; FileName="report.pdf"`,
			wantName:        "report.pdf",
			wantAttachment:  true,
			wantContentType: "application/pdf",
		},
		{
			name:            "inline carries no attachment intent",
			contentType:     "image/png",
			disposition:     `inline; filename="shot.png"`,
			wantName:        "shot.png",
			wantAttachment:  false,
			wantContentType: "image/png",
		},
		{
			name:            "an RFC 2231 encoded filename is decoded",
			contentType:     "application/pdf",
			disposition:     `attachment; filename*=UTF-8''r%C3%A9sum%C3%A9.pdf`,
			wantName:        "résumé.pdf",
			wantAttachment:  true,
			wantContentType: "application/pdf",
		},
		{
			name:            "absent disposition leaves both unset",
			contentType:     "application/pdf",
			disposition:     "",
			wantName:        "",
			wantAttachment:  false,
			wantContentType: "application/pdf",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := http.Header{}
			in.Set("Content-Type", tt.contentType)
			if tt.disposition != "" {
				in.Set("Content-Disposition", tt.disposition)
			}

			got := rawContentOptions(in)

			if got.Name != tt.wantName {
				t.Errorf("Name = %q; want %q", got.Name, tt.wantName)
			}
			if got.ForceAttachment != tt.wantAttachment {
				t.Errorf("ForceAttachment = %v; want %v", got.ForceAttachment, tt.wantAttachment)
			}
			if got.ContentType != tt.wantContentType {
				t.Errorf("ContentType = %q; want %q", got.ContentType, tt.wantContentType)
			}
		})
	}
}
