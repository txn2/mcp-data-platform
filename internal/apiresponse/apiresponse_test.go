package apiresponse

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInlineable(t *testing.T) {
	for ct, want := range map[string]bool{
		"":                                  true,
		"text/plain; charset=utf-8":         true,
		"application/json":                  true,
		"application/problem+json":          true,
		"application/atom+xml":              true,
		"application/x-www-form-urlencoded": true,
		"application/zip":                   false,
		"image/png":                         false,
		"application/pdf":                   false,
		"application/zip; broken":           true,
	} {
		assert.Equal(t, want, Inlineable(ct), ct)
	}
}

func TestHeaders_PassesTheClosedSetOnly(t *testing.T) {
	assert.Nil(t, Headers(nil))
	assert.Nil(t, Headers(http.Header{"Set-Cookie": {"a=b"}}), "nothing in the set is nil")
	got := Headers(http.Header{
		"Set-Cookie": {"a=b"}, "Link": {"<x>; rel=next"}, "Sunset": {"Wed, 01 Jan 2027 00:00:00 GMT"},
		"Deprecation": {"@1767225600"}, "Server": {"nginx"},
	})
	assert.Equal(t, map[string][]string{
		"Link": {"<x>; rel=next"}, "Sunset": {"Wed, 01 Jan 2027 00:00:00 GMT"}, "Deprecation": {"@1767225600"},
	}, got)
}
