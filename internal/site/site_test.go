package site

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"marketing", true},
		{"a", true},
		{"a-b-c", true},
		{"a1", true},
		{"a" + strings.Repeat("b", 62), true},
		{"a" + strings.Repeat("b", 63), false},
		{"", false},
		{"Marketing", false},
		{"1site", false},
		{"-site", false},
		{"site-", false},
		{"a--b", false},
		{"a_b", false},
		{"a.b", false},
		{"site\n", false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.name), func(t *testing.T) {
			err := ValidateName(tc.name)
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "site name")
		})
	}
}

func TestValidateEnvironment(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"prod", true},
		{"pr-1", true},
		{"a" + strings.Repeat("b", 31), true},
		{"a" + strings.Repeat("b", 32), false},
		{"", false},
		{"Prod", false},
		{"1prod", false},
		{"prod-", false},
		{"a--b", false},
		{"pr/1", false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q", tc.name), func(t *testing.T) {
			err := ValidateEnvironment(tc.name)
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "environment name")
		})
	}
}

func TestSiteValidate(t *testing.T) {
	good := func() *Site {
		return &Site{Name: "marketing", Environments: []Environment{{Name: EnvDefault}, {Name: "prod"}}}
	}
	require.NoError(t, good().Validate())

	cases := []struct {
		name   string
		change func(*Site)
		want   string
	}{
		{"a bad name", func(s *Site) { s.Name = "Bad Name" }, "site name"},
		{"a long description", func(s *Site) { s.Description = strings.Repeat("x", MaxDescription+1) }, "description"},
		{"a bad environment", func(s *Site) { s.Environments[1].Name = "a--b" }, "environment name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := good()
			tc.change(s)
			require.ErrorContains(t, s.Validate(), tc.want)
		})
	}

	atTheBound := good()
	atTheBound.Description = strings.Repeat("x", MaxDescription)
	require.NoError(t, atTheBound.Validate())
}

func TestSiteEnvironment(t *testing.T) {
	s := &Site{Environments: []Environment{{Name: EnvDefault, Release: 1}, {Name: "prod", Release: 3}}}
	got := s.Environment("prod")
	require.NotNil(t, got)
	assert.Equal(t, 3, got.Release)
	assert.Same(t, &s.Environments[1], got, "the environment read is the site's own, not a copy")
	assert.Nil(t, s.Environment("stage"))
	assert.Nil(t, (&Site{}).Environment(EnvDefault))
}

func TestValidLevel(t *testing.T) {
	cases := map[string]bool{
		LevelManage: true, LevelView: true,
		"": false, "owner": false, "MANAGE": false,
	}
	for level, want := range cases {
		assert.Equal(t, want, ValidLevel(level), "%q", level)
	}
	assert.False(t, ValidLevel(LevelManage+" "), "a level with a space is not a level")
}

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"pat@client.example":        "pat@client.example",
		"PAT@Client.Example":        "pat@client.example",
		"  pat@client.example \t\n": "pat@client.example",
		"":                          "",
		"   ":                       "",
	}
	for in, want := range cases {
		assert.Equal(t, want, NormalizeEmail(in), "%q", in)
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"www.example.com":       "www.example.com",
		"WWW.Example.COM":       "www.example.com",
		"www.example.com.":      "www.example.com",
		"www.example.com:8443":  "www.example.com",
		"www.example.com.:8443": "www.example.com",
		"  www.example.com \t":  "www.example.com",
		"[2001:db8::1]:443":     "2001:db8::1",
		"":                      "",
	}
	for in, want := range cases {
		assert.Equal(t, want, NormalizeHost(in), in)
	}
}

func TestValidateHostname(t *testing.T) {
	longLabel := strings.Repeat("a", 64)
	tooLong := strings.Repeat(strings.Repeat("a", 60)+".", 4) + "example.com"
	cases := []struct {
		host string
		ok   bool
	}{
		{"www.example.com", true},
		{"example.com", true},
		{"a-b.example.co.uk", true},
		{"123.example.com", true},
		{"localhost", false},
		{"", false},
		{"*.example.com", false},
		{"www.example.com:443", false},
		{"1.2.3.4", false},
		{"-a.example.com", false},
		{"a-.example.com", false},
		{"a_b.example.com", false},
		{"WWW.example.com", false},
		{"www.example.com.", false},
		{"www.example.com\n", false},
		{longLabel + ".example.com", false},
		{tooLong, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%.40q", tc.host), func(t *testing.T) {
			err := ValidateHostname(tc.host)
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, "a DNS name")
		})
	}
}

func TestSameOrUnder(t *testing.T) {
	cases := []struct {
		host, domain string
		want         bool
	}{
		{"example.com", "example.com", true},
		{"a.example.com", "example.com", true},
		{"a.b.example.com", "example.com", true},
		{"notexample.com", "example.com", false},
		{"example.com.evil.net", "example.com", false},
		{"example.com", "a.example.com", false},
		{"example.com", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, SameOrUnder(tc.host, tc.domain), "%q under %q", tc.host, tc.domain)
	}
}

func TestParseRedirects(t *testing.T) {
	got, err := ParseRedirects("# moved in the redesign\n\n/old /new\r\n/gone /elsewhere 302\n  /blog/* /news/:splat 308  \n/ext https://other.example.com/x 307\n")
	require.NoError(t, err)
	assert.Equal(t, []Redirect{
		{From: "/old", To: "/new", Status: 301},
		{From: "/gone", To: "/elsewhere", Status: 302},
		{From: "/blog/*", To: "/news/:splat", Status: 308},
		{From: "/ext", To: "https://other.example.com/x", Status: 307},
	}, got)

	empty, err := ParseRedirects("# nothing here\n\n")
	require.NoError(t, err)
	assert.NotNil(t, empty, "no rule is [], never null")
	assert.Empty(t, empty)
}

func TestParseRedirects_Refusals(t *testing.T) {
	cases := []struct {
		name, text, want string
	}{
		{"one field", "/only", "line 1: a rule is"},
		{"four fields", "/a /b 301 extra", "a rule is"},
		{"a status that is not a number", "/a /b moved", `status "moved" is not a number`},
		{"a status that is not a redirect", "/a /b 200", "status 200 is not a redirect"},
		{"the line named", "# a comment\n\n/a /b\n/a /b 404", "line 4"},
		{"a from that is not a path", "old /new", "a path starting with /"},
		{"a star in the middle", "/a/*/b /new", "* only as the last segment"},
		{"a protocol-relative target", "/a //evil.example.com", "a local path or an http(s) URL"},
		{"a backslash target", `/a /\evil.example.com`, "a local path or an http(s) URL"},
		{"a script target", "/a javascript:alert(1)", "a local path or an http(s) URL"},
		{"a target with no host", "/a https:///x", "a local path or an http(s) URL"},
		{"a control character", "/a /b\x01c", "control character"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRedirects(tc.text)
			require.ErrorContains(t, err, tc.want)
			assert.Nil(t, got)
		})
	}
}

func TestParseRedirects_RuleCountIsBounded(t *testing.T) {
	line := "/a /b\n"
	got, err := ParseRedirects(strings.Repeat(line, maxRedirects))
	require.NoError(t, err)
	assert.Len(t, got, maxRedirects)

	_, err = ParseRedirects(strings.Repeat(line, maxRedirects+1))
	require.ErrorContains(t, err, "more than")
}

func TestValidateRedirect(t *testing.T) {
	long := "/" + strings.Repeat("a", maxRedirectLen)
	cases := []struct {
		name string
		rule Redirect
		want string
	}{
		{"local", Redirect{From: "/a", To: "/b", Status: 301}, ""},
		{"http", Redirect{From: "/a", To: "http://example.com/", Status: 302}, ""},
		{"https", Redirect{From: "/a/*", To: "https://example.com/:splat", Status: 303}, ""},
		{"status 307", Redirect{From: "/a", To: "/b", Status: 307}, ""},
		{"status 308", Redirect{From: "/a", To: "/b", Status: 308}, ""},
		{"status 0", Redirect{From: "/a", To: "/b"}, "not a redirect"},
		{"status 304", Redirect{From: "/a", To: "/b", Status: 304}, "not a redirect"},
		{"a relative from", Redirect{From: "a", To: "/b", Status: 301}, "a path starting with /"},
		{"a long from", Redirect{From: long, To: "/b", Status: 301}, "a path starting with /"},
		{"a header in from", Redirect{From: "/a\r\nSet-Cookie: x=1", To: "/b", Status: 301}, "a path starting with /"},
		{"two stars", Redirect{From: "/a*/*", To: "/b", Status: 301}, "* only as the last segment"},
		{"a long target", Redirect{From: "/a", To: long, Status: 301}, "too long"},
		{"a header in the target", Redirect{From: "/a", To: "/b\r\nSet-Cookie: x=1", Status: 301}, "control character"},
		{"a delete in the target", Redirect{From: "/a", To: "/b\x7f", Status: 301}, "control character"},
		{"an empty target", Redirect{From: "/a", To: "", Status: 301}, "a local path or an http(s) URL"},
		{"a relative target", Redirect{From: "/a", To: "b", Status: 301}, "a local path or an http(s) URL"},
		{"an ftp target", Redirect{From: "/a", To: "ftp://example.com/", Status: 301}, "a local path or an http(s) URL"},
		{"an unparsable target", Redirect{From: "/a", To: "http://[::1", Status: 301}, "a local path or an http(s) URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRedirect(tc.rule)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.want)
		})
	}
}
