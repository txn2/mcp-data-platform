package epbinding

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidatePage(t *testing.T) {
	tooMany := make([]string, maxPageOrigins+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("https://cdn%d.example.com", i)
	}
	atLimit := tooMany[:maxPageOrigins]

	page := func(mutate func(*Binding)) Binding {
		b := Binding{ResponseMode: ResponsePage, Methods: []string{"GET", "POST"}}
		if mutate != nil {
			mutate(&b)
		}
		return b
	}

	tests := []struct {
		name    string
		binding Binding
		wantErr string
	}{
		{name: "an api binding", binding: Binding{ResponseMode: ResponseAPI, Methods: []string{"POST"}}},
		{name: "no mode is an api binding", binding: Binding{Methods: []string{"POST"}}},
		{name: "a page answering GET", binding: page(nil)},
		{name: "a page with CDNs and origins", binding: page(func(b *Binding) {
			b.PageCDNs = []string{"https://cdn.example.com", "https://cdn.example.com:8443"}
			b.AllowedOrigins = []string{"https://www.example.com", "http://localhost:3000"}
		})},
		{name: "a page at the limit of CDNs", binding: page(func(b *Binding) { b.PageCDNs = atLimit })},
		{name: "an api binding with any allowed origins", binding: Binding{ResponseMode: ResponseAPI, AllowedOrigins: []string{"https://a.example/with/path"}}},

		{name: "an unknown mode", binding: Binding{ResponseMode: "iframe"}, wantErr: `response_mode "iframe"`},
		{name: "page_cdns without page mode", binding: Binding{ResponseMode: ResponseAPI, PageCDNs: []string{"https://cdn.example.com"}}, wantErr: "page_cdns applies to an endpoint with response_mode page"},
		{name: "page_cdns with no mode", binding: Binding{PageCDNs: []string{"https://cdn.example.com"}}, wantErr: "page_cdns applies"},
		{name: "a page that does not answer GET", binding: page(func(b *Binding) { b.Methods = []string{"POST"} }), wantErr: "a page endpoint answers GET"},
		{name: "a CDN with a path", binding: page(func(b *Binding) { b.PageCDNs = []string{"https://cdn.example.com/lib"} }), wantErr: "page_cdns"},
		{name: "an http CDN", binding: page(func(b *Binding) { b.PageCDNs = []string{"http://cdn.example.com"} }), wantErr: "page_cdns"},
		{name: "a wildcard CDN with two labels after it", binding: page(func(b *Binding) { b.PageCDNs = []string{"https://*.js.stripe.com"} })},
		{name: "a wildcard over a top-level domain", binding: page(func(b *Binding) { b.PageCDNs = []string{"https://*.com"} }), wantErr: "page_cdns"},
		{name: "a wildcard alone", binding: page(func(b *Binding) { b.PageCDNs = []string{"https://*"} }), wantErr: "page_cdns"},
		{name: "a wildcard in the middle", binding: page(func(b *Binding) { b.PageCDNs = []string{"https://js.*.stripe.com"} }), wantErr: "page_cdns"},
		{name: "a bare wildcard", binding: page(func(b *Binding) { b.PageCDNs = []string{"*"} }), wantErr: "page_cdns"},
		{name: "a CDN holding a space", binding: page(func(b *Binding) { b.PageCDNs = []string{"https://a.example https://b.example"} }), wantErr: "page_cdns"},
		{name: "a CDN holding a semicolon", binding: page(func(b *Binding) { b.PageCDNs = []string{"https://a.example;script-src"} }), wantErr: "page_cdns"},
		{name: "a CDN with upper case", binding: page(func(b *Binding) { b.PageCDNs = []string{"https://CDN.example.com"} }), wantErr: "page_cdns"},
		{name: "too many CDNs", binding: page(func(b *Binding) { b.PageCDNs = tooMany }), wantErr: "at most"},
		{name: "an allowed origin holding a quote", binding: page(func(b *Binding) { b.AllowedOrigins = []string{`https://a.example'`} }), wantErr: "allowed_origins"},
		{name: "an allowed origin holding a double quote", binding: page(func(b *Binding) { b.AllowedOrigins = []string{`https://a.example"x`} }), wantErr: "allowed_origins"},
		{name: "an allowed origin with a path", binding: page(func(b *Binding) { b.AllowedOrigins = []string{"https://a.example/path"} }), wantErr: "allowed_origins"},
		{name: "an allowed origin that is a wildcard", binding: page(func(b *Binding) { b.AllowedOrigins = []string{"*"} }), wantErr: "allowed_origins"},
		{name: "an allowed origin with a newline", binding: page(func(b *Binding) { b.AllowedOrigins = []string{"https://a.example\nx"} }), wantErr: "allowed_origins"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.binding.validatePage()
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestValidate_RunsThePageCheck(t *testing.T) {
	b := Binding{
		Name: "orders", Methods: []string{"GET"}, AuthMode: AuthPublic, MaxBodyBytes: 1024, MaxMemoryBytes: 1 << 20,
		TimeoutSeconds: 10, RateLimit: 10, CallerRateLimit: 10, ResponseMode: "nonsense",
	}
	require.ErrorContains(t, b.Validate(), "response_mode")
}

// directive is one directive of a content policy, its sources split out.
func directive(t *testing.T, policy, name string) []string {
	t.Helper()
	for part := range strings.SplitSeq(policy, ";") {
		fields := strings.Fields(part)
		if len(fields) > 0 && fields[0] == name {
			return fields[1:]
		}
	}
	require.Failf(t, "directive absent", "no %s in %s", name, policy)
	return nil
}

func TestPagePolicy_ScriptIsSelfAndTheNonceAlone(t *testing.T) {
	policy := Binding{}.PagePolicy("abc123")
	script := directive(t, policy, "script-src")
	assert.Contains(t, script, "'self'")
	assert.Contains(t, script, "'nonce-abc123'")
	assert.NotContains(t, script, "'unsafe-inline'")
	assert.NotContains(t, script, "'unsafe-eval'")
	assert.Equal(t, []string{"'none'"}, directive(t, policy, "default-src"))
	assert.Equal(t, []string{"'none'"}, directive(t, policy, "base-uri"))
	assert.Equal(t, []string{"'self'"}, directive(t, policy, "form-action"))
}

func TestPagePolicy_CDNsAreInEachLoadDirective(t *testing.T) {
	const cdn1, cdn2 = "https://cdn.example.com", "https://fonts.example.com"
	policy := Binding{PageCDNs: []string{cdn1, cdn2}}.PagePolicy("n")
	for _, name := range []string{"script-src", "style-src", "img-src", "font-src"} {
		t.Run(name, func(t *testing.T) {
			sources := directive(t, policy, name)
			assert.Contains(t, sources, "'self'")
			assert.Contains(t, sources, cdn1)
			assert.Contains(t, sources, cdn2)
		})
	}
	assert.Equal(t, []string{"'self'"}, directive(t, policy, "connect-src"), "a page's script reaches only its own origin")
}

func TestPagePolicy_FontSrcHasNoEmptySource(t *testing.T) {
	assert.Equal(t, []string{"'self'"}, directive(t, Binding{}.PagePolicy("n"), "font-src"))
}

func TestPagePolicy_Framing(t *testing.T) {
	tests := []struct {
		name    string
		origins []string
		want    []string
	}{
		{name: "no allowed origins frames nowhere", want: []string{"'none'"}},
		{name: "allowed origins frame it", origins: []string{"https://a.example", "https://b.example"}, want: []string{"https://a.example", "https://b.example"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Binding{AllowedOrigins: tt.origins}.PagePolicy("n")
			assert.Equal(t, tt.want, directive(t, got, "frame-ancestors"))
		})
	}
}

func TestPagePolicy_ANewNonceEachCall(t *testing.T) {
	b := Binding{}
	assert.NotEqual(t, b.PagePolicy("one"), b.PagePolicy("two"))
}

func TestBuild_ResponseModeDefaultsAndKeeps(t *testing.T) {
	var b Binding
	apply(&b, Request{ResponseMode: " page ", PageCDNs: []string{"https://cdn.example.com"}})
	assert.Equal(t, ResponsePage, b.ResponseMode)
	assert.Equal(t, []string{"https://cdn.example.com"}, b.PageCDNs)

	apply(&b, Request{})
	assert.Equal(t, ResponsePage, b.ResponseMode, "an empty mode keeps what the binding had")
	assert.Equal(t, []string{"https://cdn.example.com"}, b.PageCDNs, "nil CDNs keep them")

	apply(&b, Request{PageCDNs: []string{}})
	assert.Empty(t, b.PageCDNs, "an empty list clears them")
}

// TestValidatePage_TheFrameConnectAndFormLists is #2554's refusals: each list
// takes https origins and one leading wildcard label of the accepted shape,
// at most twenty, and only on a page binding; the refusal names the field.
func TestValidatePage_TheFrameConnectAndFormLists(t *testing.T) {
	page := func(set func(*Binding, []string), origins ...string) Binding {
		b := Binding{ResponseMode: ResponsePage, Methods: []string{"GET"}}
		set(&b, origins)
		return b
	}
	lists := map[string]func(*Binding, []string){
		"page_frame_origins":   func(b *Binding, o []string) { b.PageFrameOrigins = o },
		"page_connect_origins": func(b *Binding, o []string) { b.PageConnectOrigins = o },
		"page_form_origins":    func(b *Binding, o []string) { b.PageFormOrigins = o },
	}
	tooMany := make([]string, maxPageOrigins+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("https://pay%d.example.com", i)
	}
	for field, set := range lists {
		t.Run(field, func(t *testing.T) {
			require.NoError(t, page(set, "https://pay.example.test", "https://*.js.stripe.com", "https://pay.example.test:8443").validatePage())
			require.NoError(t, page(set, tooMany[:maxPageOrigins]...).validatePage())
			for _, bad := range []string{"http://pay.example.test", "https://pay.example.test/checkout", "https://*", "https://*.com", "*", "https://a.example b"} {
				require.ErrorContains(t, page(set, bad).validatePage(), field+" ", bad)
			}
			require.ErrorContains(t, page(set, tooMany...).validatePage(), field+": at most 20")
			api := Binding{ResponseMode: ResponseAPI, Methods: []string{"POST"}}
			set(&api, []string{"https://pay.example.test"})
			require.ErrorContains(t, api.validatePage(), field+" applies to an endpoint with response_mode page")
		})
	}
}

// TestPagePolicy_FrameConnectAndFormOrigins is #2554's policy: each list
// lands in its directive, and a binding that lists none answers byte for byte
// the policy it answered before they existed.
func TestPagePolicy_FrameConnectAndFormOrigins(t *testing.T) {
	const before = "default-src 'none'; script-src 'self' 'nonce-n' https://cdn.example.com; style-src 'self' 'unsafe-inline' https://cdn.example.com; " +
		"img-src 'self' data: https://cdn.example.com; font-src 'self' https://cdn.example.com; connect-src 'self'; form-action 'self'; " +
		"base-uri 'none'; frame-ancestors https://www.example.com"
	b := Binding{PageCDNs: []string{"https://cdn.example.com"}, AllowedOrigins: []string{"https://www.example.com"}}
	assert.Equal(t, before, b.PagePolicy("n"), "no frame, connect or form origin: today's policy")
	assert.NotContains(t, b.PagePolicy("n"), "frame-src")

	b.PageFrameOrigins = []string{"https://js.stripe.com", "https://*.js.stripe.com"}
	b.PageConnectOrigins = []string{"https://api.stripe.com"}
	b.PageFormOrigins = []string{"https://pay.example.test"}
	policy := b.PagePolicy("n")
	assert.Equal(t, []string{"https://js.stripe.com", "https://*.js.stripe.com"}, directive(t, policy, "frame-src"))
	assert.Equal(t, []string{"'self'", "https://api.stripe.com"}, directive(t, policy, "connect-src"))
	assert.Equal(t, []string{"'self'", "https://pay.example.test"}, directive(t, policy, "form-action"))
	assert.Equal(t, []string{"https://www.example.com"}, directive(t, policy, "frame-ancestors"), "framing of the page itself is unchanged")
}

func TestBuild_PageOriginListsKeepAndClear(t *testing.T) {
	var b Binding
	apply(&b, Request{PageFrameOrigins: []string{"https://a.example.com"}, PageConnectOrigins: []string{"https://b.example.com"}, PageFormOrigins: []string{"https://c.example.com"}})
	apply(&b, Request{})
	assert.Equal(t, []string{"https://a.example.com"}, b.PageFrameOrigins, "nil keeps")
	assert.Equal(t, []string{"https://b.example.com"}, b.PageConnectOrigins)
	assert.Equal(t, []string{"https://c.example.com"}, b.PageFormOrigins)
	apply(&b, Request{PageFrameOrigins: []string{}, PageConnectOrigins: []string{}, PageFormOrigins: []string{}})
	assert.Empty(t, b.PageFrameOrigins, "an empty list clears")
	assert.Empty(t, b.PageConnectOrigins)
	assert.Empty(t, b.PageFormOrigins)
}
