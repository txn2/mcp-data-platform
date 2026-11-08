package apphttp

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appdiagram"
)

// TestDiagramRoute answers an application's diagram at a version (#2540),
// refuses a version that names none, and the signed out.
func TestDiagramRoute(t *testing.T) {
	sv := newServer()
	assert.Equal(t, http.StatusUnauthorized, sv.do(t, "GET", Prefix+"/shop/diagram", "").Code)
	sv.who = &application.Actor{Email: "jane@example.com"}
	require.Equal(t, http.StatusOK, sv.do(t, "POST", Prefix, `{"name":"shop"}`).Code)
	require.Equal(t, http.StatusOK, sv.do(t, "POST", Prefix+"/shop/parts", `{"kind":"script","id":"11111111-1111-1111-1111-111111111111"}`).Code)

	w := sv.do(t, "GET", Prefix+"/shop/diagram?version=latest", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var d appdiagram.Diagram
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &d))
	assert.Equal(t, "latest", d.Version)
	require.Len(t, d.Nodes, 1)
	assert.Equal(t, appdiagram.NodeScript, d.Nodes[0].Kind)
	assert.Contains(t, w.Body.String(), `"links":[]`, "an empty list is [], never null")
	assert.Contains(t, w.Body.String(), `"findings":[]`)

	assert.Equal(t, http.StatusUnprocessableEntity, sv.do(t, "GET", Prefix+"/shop/diagram?version=x", "").Code)
	assert.Equal(t, http.StatusNotFound, sv.do(t, "GET", Prefix+"/none/diagram", "").Code)
}
