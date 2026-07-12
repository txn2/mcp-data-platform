package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type decodeTarget struct {
	Name  string   `json:"name"`
	Tags  []string `json:"tags,omitempty"`
	Count int      `json:"count,omitempty"`
}

func newDecodeRequest(body string) (*httptest.ResponseRecorder, *http.Request) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(body))
	return httptest.NewRecorder(), r
}

func TestDecodeStrict(t *testing.T) {
	t.Run("decodes a valid body", func(t *testing.T) {
		w, r := newDecodeRequest(`{"name":"a","tags":["x"],"count":2}`)
		var dst decodeTarget
		require.NoError(t, decodeStrict(w, r, &dst))
		assert.Equal(t, "a", dst.Name)
		assert.Equal(t, []string{"x"}, dst.Tags)
		assert.Equal(t, 2, dst.Count)
	})

	t.Run("rejects an unknown field naming it", func(t *testing.T) {
		w, r := newDecodeRequest(`{"name":"a","tools":{"allow":["*"]}}`)
		var dst decodeTarget
		err := decodeStrict(w, r, &dst)
		require.Error(t, err)
		assert.Equal(t, `unknown field "tools"`, err.Error())
	})

	t.Run("rejects malformed JSON with a generic message", func(t *testing.T) {
		w, r := newDecodeRequest(`{bad`)
		var dst decodeTarget
		err := decodeStrict(w, r, &dst)
		require.Error(t, err)
		assert.Equal(t, errInvalidBody, err.Error())
	})

	t.Run("rejects trailing data after the first value", func(t *testing.T) {
		w, r := newDecodeRequest(`{"name":"a"}{"name":"b"}`)
		var dst decodeTarget
		err := decodeStrict(w, r, &dst)
		require.Error(t, err)
		assert.Equal(t, errInvalidBody, err.Error())
	})

	t.Run("rejects an oversized body", func(t *testing.T) {
		big := `{"name":"` + strings.Repeat("a", maxAdminBodyBytes+16) + `"}`
		w, r := newDecodeRequest(big)
		var dst decodeTarget
		err := decodeStrict(w, r, &dst)
		require.Error(t, err)
		assert.Equal(t, "request body too large", err.Error())
	})

	t.Run("rejects an empty required body", func(t *testing.T) {
		w, r := newDecodeRequest("")
		var dst decodeTarget
		err := decodeStrict(w, r, &dst)
		require.Error(t, err)
		assert.Equal(t, errInvalidBody, err.Error())
	})
}

func TestDecodeStrictLimit(t *testing.T) {
	t.Run("accepts a body larger than the default cap under a bigger limit", func(t *testing.T) {
		// A payload that exceeds maxAdminBodyBytes but fits a caller-supplied
		// larger bound must still decode (issue #923 regression: catalog spec
		// content routinely exceeds the small default cap).
		big := `{"name":"` + strings.Repeat("a", maxAdminBodyBytes+16) + `"}`
		w, r := newDecodeRequest(big)
		var dst decodeTarget
		require.NoError(t, decodeStrictLimit(w, r, &dst, maxAdminBodyBytes*4))
		assert.Equal(t, maxAdminBodyBytes+16, len(dst.Name))
	})

	t.Run("still rejects unknown fields under a bigger limit", func(t *testing.T) {
		w, r := newDecodeRequest(`{"name":"a","nope":1}`)
		var dst decodeTarget
		err := decodeStrictLimit(w, r, &dst, maxAdminBodyBytes*4)
		require.Error(t, err)
		assert.Equal(t, `unknown field "nope"`, err.Error())
	})

	t.Run("enforces the supplied limit", func(t *testing.T) {
		big := `{"name":"` + strings.Repeat("a", 4096) + `"}`
		w, r := newDecodeRequest(big)
		var dst decodeTarget
		err := decodeStrictLimit(w, r, &dst, 1024)
		require.Error(t, err)
		assert.Equal(t, "request body too large", err.Error())
	})
}

func TestDecodeStrictOptional(t *testing.T) {
	t.Run("treats an empty body as success", func(t *testing.T) {
		w, r := newDecodeRequest("")
		var dst decodeTarget
		require.NoError(t, decodeStrictOptional(w, r, &dst))
		assert.Equal(t, decodeTarget{}, dst)
	})

	t.Run("treats a nil body as success", func(t *testing.T) {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
		r.Body = nil
		var dst decodeTarget
		require.NoError(t, decodeStrictOptional(httptest.NewRecorder(), r, &dst))
	})

	t.Run("still rejects unknown fields when a body is present", func(t *testing.T) {
		w, r := newDecodeRequest(`{"return_to":"/x"}`)
		var dst decodeTarget
		err := decodeStrictOptional(w, r, &dst)
		require.Error(t, err)
		assert.Equal(t, `unknown field "return_to"`, err.Error())
	})

	t.Run("decodes a present valid body", func(t *testing.T) {
		w, r := newDecodeRequest(`{"name":"a"}`)
		var dst decodeTarget
		require.NoError(t, decodeStrictOptional(w, r, &dst))
		assert.Equal(t, "a", dst.Name)
	})
}

// TestNoBareBodyDecodeInHandlers guards the acceptance criterion that no admin
// write endpoint returns to a lenient json.NewDecoder(r.Body).Decode that would
// silently drop unknown fields. New handlers must route request bodies through
// decodeStrict / decodeStrictOptional. decode.go itself is the one legitimate
// site of the raw decoder.
func TestNoBareBodyDecodeInHandlers(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	const bare = "json.NewDecoder(r.Body).Decode"
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") || name == "decode.go" {
			continue
		}
		data, rerr := os.ReadFile(filepath.Clean(name))
		require.NoError(t, rerr)
		assert.NotContains(t, string(data), bare,
			"%s uses a lenient body decode; route it through decodeStrict/decodeStrictOptional (issue #923)", name)
	}
}
