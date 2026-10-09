package pathtemplate

import "testing"

// TestPathMatchesTemplate_MultiPlaceholderSegment proves the router side
// agrees with the substitution side: a concrete path produced by
// the api gateway's path_params substitution matches the template it came from. Without this
// the two halves of issue #1297 would disagree and Content-Type
// negotiation would silently miss the operation.
func TestPathMatchesTemplate_MultiPlaceholderSegment(t *testing.T) {
	cases := []struct {
		name     string
		concrete string
		template string
		want     bool
	}{
		{"comma-joined pair matches", "/points/37.41,-94.70", "/points/{latitude},{longitude}", true},
		{"missing separator does not match", "/points/37.41", "/points/{latitude},{longitude}", false},
		{"empty first hole does not match", "/points/,-94.70", "/points/{latitude},{longitude}", false},
		{"empty second hole does not match", "/points/37.41,", "/points/{latitude},{longitude}", false},
		{"extra segment does not match", "/points/37.41,-94.70/x", "/points/{latitude},{longitude}", false},
		{"literal suffix matches", "/files/report.json", "/files/{name}.json", true},
		{"literal suffix required", "/files/report.yaml", "/files/{name}.json", false},
		{"literal suffix needs a non-empty hole", "/files/.json", "/files/{name}.json", false},
		{"literal prefix matches", "/v1/id-42", "/v1/id-{id}", true},
		{"literal prefix required", "/v1/42", "/v1/id-{id}", false},
		{"nested template segment matches", "/gridpoints/EAX/50,60/forecast", "/gridpoints/{office}/{gridX},{gridY}/forecast", true},
		{"whole-segment placeholder still matches", "/things/abc", "/things/{id}", true},
		{"literal segment still exact", "/things", "/things", true},
		{"value containing the separator still matches greedily", "/points/a,b,c", "/points/{latitude},{longitude}", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Match(c.concrete, c.template); got != c.want {
				t.Errorf("Match(%q, %q) = %v; want %v",
					c.concrete, c.template, got, c.want)
			}
		})
	}
}

// TestCountTemplatePlaceholders_CountsOccurrences proves specificity
// ranking counts holes rather than templated segments, so a template
// spending two placeholders on one segment ranks below one that spends
// a whole segment per placeholder when both match.
func TestCountTemplatePlaceholders_CountsOccurrences(t *testing.T) {
	cases := []struct {
		template string
		want     int
	}{
		{"/v1/things", 0},
		{"/v1/things/{id}", 1},
		{"/v1/orgs/{org}/things/{id}", 2},
		{"/points/{latitude},{longitude}", 2},
		{"/gridpoints/{office}/{gridX},{gridY}/forecast", 3},
		{"/files/{name}.json", 1},
		{"/v1/{}", 0},
	}
	for _, c := range cases {
		t.Run(c.template, func(t *testing.T) {
			if got := CountPlaceholders(c.template); got != c.want {
				t.Errorf("CountPlaceholders(%q) = %d; want %d", c.template, got, c.want)
			}
		})
	}
}

// TestIsPlaceholderSegment_WholeSegmentOnly proves the whole-segment
// test rejects the shapes that must route through the literal-aware
// matcher instead, so a segment with literal text is never treated as a
// match-anything wildcard.
func TestIsPlaceholderSegment_WholeSegmentOnly(t *testing.T) {
	cases := map[string]bool{
		"{id}":                   true,
		"{datasetId}":            true,
		"things":                 false,
		"":                       false,
		"{}":                     false,
		"{latitude},{longitude}": false,
		"{name}.json":            false,
		"id-{id}":                false,
		"{a}{b}":                 false,
	}
	for seg, want := range cases {
		t.Run(seg, func(t *testing.T) {
			if got := IsPlaceholderSegment(seg); got != want {
				t.Errorf("IsPlaceholderSegment(%q) = %v; want %v", seg, got, want)
			}
		})
	}
}

// TestSegmentHelpers covers the helpers the WebDAV matcher and the
// specificity ranking read directly.
func TestSegmentHelpers(t *testing.T) {
	if got := Split("/a/b/"); len(got) != 3 || got[2] != "b" {
		t.Errorf("Split = %q", got)
	}
	if !IsTemplated("{lat},{lon}") || IsTemplated("users") {
		t.Error("IsTemplated")
	}
	if !SegmentMatches("x", "{id}") || SegmentMatches("", "{id}") || !SegmentMatches("a", "a") || !SegmentMatches("{", "{") {
		t.Error("SegmentMatches")
	}
	if templatedSegmentMatches("x", "{") || !templatedSegmentMatches("{", "{") {
		t.Error("a template with no well-formed placeholder is compared literally")
	}
}

// TestValidateCallerPath holds the shapes a caller's path is refused for: one
// that would reach another host once joined to the base URL, and one a route
// rule would match differently from the path the upstream receives.
func TestValidateCallerPath(t *testing.T) {
	for _, ok := range []string{"/v1/users", "/v1/users/", "/v1/users/%20name"} {
		if err := ValidateCallerPath(ok); err != nil {
			t.Errorf("ValidateCallerPath(%q) = %v; want accepted", ok, err)
		}
	}
	for _, bad := range []string{
		"", "v1/users", "//evil.example/foo", "/foo@evil.example/bar", "/foo\rEvil: x", "/foo\nEvil: x", "/foo\x00bar",
		"/v1/users/..", "/v1/users/.", "/v1/../etc/passwd", "/v1//admin/secret", "/v1///admin",
		"/v1/users/%2E%2E", "/v1/users/%2e%2e", "/v1/users/%2E", "/v1/users/%2",
	} {
		if err := ValidateCallerPath(bad); err == nil {
			t.Errorf("ValidateCallerPath(%q) accepted; want rejection", bad)
		}
	}
}
