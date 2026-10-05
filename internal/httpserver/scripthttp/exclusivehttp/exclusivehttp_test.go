package exclusivehttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// serve drives one PUT through the mounted route with edit standing in for
// scripthttp's owned edit.
func serve(t *testing.T, body string, edit func(http.ResponseWriter, *http.Request, func(*script.Script)) (*script.Script, bool)) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	New(Deps{Edit: edit}).Register(mux, func(h http.Handler) http.Handler { return h })
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/portal/scripts/s1/exclusive", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// applying is an edit that applies the mutation to a fresh script.
func applying(_ http.ResponseWriter, _ *http.Request, mutate func(*script.Script)) (*script.Script, bool) {
	sc := &script.Script{ID: "s1"}
	mutate(sc)
	return sc, true
}

// TestSetExclusive_SavesAndStatesTheMeaning sends both values and reads the
// sentence each answers with.
func TestSetExclusive_SavesAndStatesTheMeaning(t *testing.T) {
	rec := serve(t, `{"exclusive":true}`, applying)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"exclusive":true`)
	assert.Contains(t, rec.Body.String(), "A run cannot start while another run of this script is pending or running.")

	rec = serve(t, `{"exclusive":false}`, applying)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Runs of this script may overlap.")
}

// TestSetExclusive_RefusesABodyWithoutTheSetting keeps a missing or malformed
// value from reading as "turn it off", and edits nothing.
func TestSetExclusive_RefusesABodyWithoutTheSetting(t *testing.T) {
	for _, body := range []string{`{}`, `not json`, `{"exclusive":"yes"}`} {
		edited := false
		rec := serve(t, body, func(http.ResponseWriter, *http.Request, func(*script.Script)) (*script.Script, bool) {
			edited = true
			return nil, false
		})
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
		assert.False(t, edited, body)
	}
}

// TestSetExclusive_LeavesARefusedEditsAnswerAlone writes nothing more once the
// edit has answered with its own refusal.
func TestSetExclusive_LeavesARefusedEditsAnswerAlone(t *testing.T) {
	rec := serve(t, `{"exclusive":true}`, func(w http.ResponseWriter, _ *http.Request, _ func(*script.Script)) (*script.Script, bool) {
		w.WriteHeader(http.StatusConflict)
		return nil, false
	})
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Empty(t, rec.Body.String())
}
