package accessreq

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type memSettings struct {
	v      Settings
	author string
	err    error
}

func (m *memSettings) Read(context.Context) Settings { return m.v }

func (m *memSettings) Write(_ context.Context, v Settings, author string) error {
	if m.err != nil {
		return m.err
	}
	m.v, m.author = v, author
	return nil
}

func settingsCall(store *memSettings, method, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	RegisterSettings(mux, func(h http.Handler) http.Handler { return h }, store,
		func(*http.Request) string { return "admin@example.com" })
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), method, settingsPath, strings.NewReader(body)))
	return w
}

// The settings read back what was written, record who wrote them, and refuse
// a period out of range, a body that is not the settings, and a store that
// fails (#2090).
func TestSettingsRoutes(t *testing.T) {
	store := &memSettings{v: Defaults()}
	w := settingsCall(store, http.MethodGet, "")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"reveal_names":false,"expire_days":14}`, w.Body.String())

	w = settingsCall(store, http.MethodPut, `{"reveal_names":true,"expire_days":30}`)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, Settings{RevealNames: true, ExpireDays: 30}, store.v)
	assert.Equal(t, "admin@example.com", store.author)

	for _, body := range []string{`{"expire_days":0}`, `{"expire_days":366}`, `{"expire_days":`, `{"unknown":1}`} {
		assert.Equal(t, http.StatusBadRequest, settingsCall(store, http.MethodPut, body).Code, body)
	}
	store.err = errors.New("down")
	assert.Equal(t, http.StatusInternalServerError, settingsCall(store, http.MethodPut, `{"expire_days":7}`).Code)
}
