package portal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusOK, map[string]string{"key": "value"})

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var result map[string]string
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "value", result["key"])
}

func TestWriteJSONCreated(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, http.StatusCreated, statusResponse{Status: "created"})

	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusNotFound, "resource not found")

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/problem+json")

	var pd problemDetail
	err := json.NewDecoder(w.Body).Decode(&pd)
	require.NoError(t, err)
	assert.Equal(t, "about:blank", pd.Type)
	assert.Equal(t, "Not Found", pd.Title)
	assert.Equal(t, http.StatusNotFound, pd.Status)
	assert.Equal(t, "resource not found", pd.Detail)
}

func TestWriteErrorBadRequest(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "invalid input")

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var pd problemDetail
	err := json.NewDecoder(w.Body).Decode(&pd)
	require.NoError(t, err)
	assert.Equal(t, "Bad Request", pd.Title)
	assert.Equal(t, http.StatusBadRequest, pd.Status)
}

func TestWriteErrorServerError(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusInternalServerError, "something broke")

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var pd problemDetail
	err := json.NewDecoder(w.Body).Decode(&pd)
	require.NoError(t, err)
	assert.Equal(t, "Internal Server Error", pd.Title)
}
