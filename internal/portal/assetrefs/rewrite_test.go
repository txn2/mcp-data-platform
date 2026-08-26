package assetrefs_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
)

const (
	testBase    = "https://platform.example.com"
	logoURI     = "mcp://global/brand/logo.png"
	logoToken   = "tok-logo"
	photoURI    = "mcp://global/brand/photo.jpg"
	photoToken  = "tok-photo"
	testAssetID = "asset_1"
)

func refs(pairs ...[2]string) []assetrefs.Ref {
	out := make([]assetrefs.Ref, 0, len(pairs))
	for i, p := range pairs {
		out = append(out, assetrefs.Ref{
			AssetID: testAssetID, TargetKind: assetrefs.TargetResource,
			TargetID: p[1], URI: p[0], RefToken: p[1], Position: i,
		})
	}
	return out
}

// TestURLIsAbsolute is the property the whole design rests on: an asset renders
// inside an iframe whose document came from a blob: URL, where a root-relative
// path does not name the server at all.
func TestURLIsAbsolute(t *testing.T) {
	got := assetrefs.URL(testBase, testAssetID, logoToken)
	assert.Equal(t, "https://platform.example.com/portal/refs/asset_1/tok-logo", got)
	assert.True(t, strings.HasPrefix(got, "https://"), "a blob: document cannot resolve a relative path")
}

// TestURLTolerantOfTrailingSlash proves a base URL configured either way builds
// one well-formed URL, not one with a doubled separator.
func TestURLTolerantOfTrailingSlash(t *testing.T) {
	assert.Equal(t,
		assetrefs.URL(testBase, testAssetID, logoToken),
		assetrefs.URL(testBase+"/", testAssetID, logoToken))
}

// TestRewriteReplacesDeclaredURI is acceptance criterion 1 at the rewrite
// level: the URI the author wrote becomes a working URL in what is served.
func TestRewriteReplacesDeclaredURI(t *testing.T) {
	content := []byte(`<img src="` + logoURI + `">`)
	out := assetrefs.Rewrite(content, "text/html", testBase, testAssetID,
		refs([2]string{logoURI, logoToken}))

	assert.Equal(t, `<img src="https://platform.example.com/portal/refs/asset_1/tok-logo">`, string(out))
	assert.NotContains(t, string(out), logoURI)
}

// TestRewriteLeavesUndeclaredURIAlone is acceptance criterion 3: the grant is
// the declaration, never a string that happens to appear in the body.
func TestRewriteLeavesUndeclaredURIAlone(t *testing.T) {
	content := []byte(`<img src="mcp://persona/finance/secret.png"><img src="` + logoURI + `">`)
	out := assetrefs.Rewrite(content, "text/html", testBase, testAssetID,
		refs([2]string{logoURI, logoToken}))

	assert.Contains(t, string(out), "mcp://persona/finance/secret.png",
		"an undeclared URI must be served exactly as written")
	assert.Contains(t, string(out), "/portal/refs/asset_1/tok-logo")
}

// TestRewriteLongestURIFirst pins the ordering rule. One declared URI can be a
// prefix of another; replacing the shorter first would rewrite the head of the
// longer and strand its tail in the markup.
func TestRewriteLongestURIFirst(t *testing.T) {
	shortURI := "mcp://global/brand/logo.png"
	longURI := "mcp://global/brand/logo.png.bak"
	content := []byte(shortURI + "|" + longURI)

	// Declared shortest-first, which is the order that would break a naive
	// replacer.
	out := assetrefs.Rewrite(content, "text/markdown", testBase, testAssetID,
		refs([2]string{shortURI, "tok-short"}, [2]string{longURI, "tok-long"}))

	assert.Equal(t,
		"https://platform.example.com/portal/refs/asset_1/tok-short|"+
			"https://platform.example.com/portal/refs/asset_1/tok-long",
		string(out))
	assert.NotContains(t, string(out), ".bak", "the longer URI must not be left half-rewritten")
}

// TestRewriteSkipsBinaryContent proves stored bytes are never scanned: a PNG
// that happens to contain the URI's bytes must come back byte-identical.
func TestRewriteSkipsBinaryContent(t *testing.T) {
	content := []byte("\x89PNG\r\n" + logoURI)
	out := assetrefs.Rewrite(content, "image/png", testBase, testAssetID,
		refs([2]string{logoURI, logoToken}))

	assert.Equal(t, content, out)
}

// TestRewriteNoOpCases covers every input that must leave content untouched.
func TestRewriteNoOpCases(t *testing.T) {
	declared := refs([2]string{logoURI, logoToken})
	content := []byte("<p>no references here</p>")

	tests := []struct {
		name    string
		content []byte
		ct      string
		refs    []assetrefs.Ref
	}{
		{"no references declared", content, "text/html", nil},
		{"empty content", nil, "text/html", declared},
		{"declared but not mentioned", content, "text/html", declared},
		{
			"reference with no token", content, "text/html",
			[]assetrefs.Ref{{URI: logoURI}},
		},
		{
			"reference with no uri", []byte(logoURI), "text/html",
			[]assetrefs.Ref{{RefToken: logoToken}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := assetrefs.Rewrite(tt.content, tt.ct, testBase, testAssetID, tt.refs)
			assert.Equal(t, tt.content, out)
		})
	}
}

// TestRewriteEveryDeclaredOccurrence proves a URI used more than once in the
// same document is rewritten everywhere, not only the first time: a report that
// shows its logo in the header and the footer must show it twice.
func TestRewriteEveryDeclaredOccurrence(t *testing.T) {
	content := []byte(logoURI + " ... " + photoURI + " ... " + logoURI)
	out := assetrefs.Rewrite(content, "text/markdown", testBase, testAssetID,
		refs([2]string{logoURI, logoToken}, [2]string{photoURI, photoToken}))

	assert.Equal(t, 2, strings.Count(string(out), "/portal/refs/asset_1/tok-logo"))
	assert.Equal(t, 1, strings.Count(string(out), "/portal/refs/asset_1/tok-photo"))
	assert.NotContains(t, string(out), "mcp://")
}

// TestBaseURLPrefersTheConfiguredOrigin pins that an operator's setting wins
// over whatever host a request arrived on.
func TestBaseURLPrefersTheConfiguredOrigin(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://internal:8080/x", http.NoBody)

	assert.Equal(t, testBase, assetrefs.BaseURL(req, testBase))
	assert.Equal(t, testBase, assetrefs.BaseURL(req, testBase+"/"))
}

// TestBaseURLFallsBackToTheRequestOrigin is why the fallback exists: a
// deployment that never set portal.public_base_url still serves share links,
// and references must not be the one surface that emits URLs no reader can
// follow.
func TestBaseURLFallsBackToTheRequestOrigin(t *testing.T) {
	tests := []struct {
		name      string
		forwarded string
		tls       bool
		want      string
	}{
		{"plain http", "", false, "http://reports.example.com"},
		{"proxy says https", "https", false, "https://reports.example.com"},
		{"proxy chain uses the leftmost", "https, http", false, "https://reports.example.com"},
		{"malformed proxy header ignored", "javascript", false, "http://reports.example.com"},
		{"terminating tls ignores the header", "http", true, "https://reports.example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
				"http://reports.example.com/x", http.NoBody)
			if tt.forwarded != "" {
				req.Header.Set("X-Forwarded-Proto", tt.forwarded)
			}
			if tt.tls {
				req.TLS = &tls.ConnectionState{}
			}
			assert.Equal(t, tt.want, assetrefs.BaseURL(req, ""))
		})
	}
}

// TestBaseURLWithNothingToResolve covers the shapes that name no origin at all,
// which yield a root-relative URL rather than a malformed absolute one.
func TestBaseURLWithNothingToResolve(t *testing.T) {
	assert.Empty(t, assetrefs.BaseURL(nil, ""))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)
	req.Host = ""
	assert.Empty(t, assetrefs.BaseURL(req, ""))
	assert.Equal(t, "/portal/refs/asset_1/tok-logo",
		assetrefs.URL(assetrefs.BaseURL(req, ""), testAssetID, logoToken))
}
