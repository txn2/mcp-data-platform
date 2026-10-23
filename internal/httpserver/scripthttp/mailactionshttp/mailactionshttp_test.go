package mailactionshttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

func serve(t *testing.T, body string, edit func(http.ResponseWriter, *http.Request, func(*script.Script)) (*script.Script, bool)) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	New(Deps{Edit: edit}).Register(mux, func(h http.Handler) http.Handler { return h })
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/portal/scripts/s1/mail-actions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func applying(_ http.ResponseWriter, _ *http.Request, mutate func(*script.Script)) (*script.Script, bool) {
	sc := &script.Script{ID: "s1"}
	mutate(sc)
	return sc, true
}

func TestSetMailActions_SavesAndStatesTheMeaning(t *testing.T) {
	rec := serve(t, `{"mail_actions":true}`, applying)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"mail_actions":true`)
	assert.Contains(t, rec.Body.String(), "reads mail and acts on it")

	rec = serve(t, `{"mail_actions":false}`, applying)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "cannot write anything afterwards")
}

func TestSetMailActions_RefusesABodyWithoutTheSetting(t *testing.T) {
	for _, body := range []string{`{}`, `not json`, `{"mail_actions":"yes"}`} {
		edited := false
		rec := serve(t, body, func(http.ResponseWriter, *http.Request, func(*script.Script)) (*script.Script, bool) {
			edited = true
			return nil, false
		})
		assert.Equal(t, http.StatusBadRequest, rec.Code, body)
		assert.False(t, edited, body)
	}
}

func TestSetMailActions_LeavesARefusedEditsAnswerAlone(t *testing.T) {
	rec := serve(t, `{"mail_actions":true}`, func(w http.ResponseWriter, _ *http.Request, _ func(*script.Script)) (*script.Script, bool) {
		w.WriteHeader(http.StatusForbidden)
		return nil, false
	})
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, rec.Body.String())
}
