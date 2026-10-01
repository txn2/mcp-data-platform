package swagger2

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// swagger2YAML is a Swagger 2.0 document as swaggo/swag and most hand-written
// specs carry it: YAML, an unquoted response code, a basePath and no host.
const swagger2YAML = `swagger: "2.0"
info:
  title: Orders
  version: "1.0"
basePath: /api/v1
paths:
  /orders/{id}:
    get:
      operationId: getOrder
      parameters:
        - name: id
          in: path
          required: true
          type: string
      responses:
        200:
          description: an order
`

func TestIs(t *testing.T) {
	assert.True(t, Is(swagger2YAML))
	assert.True(t, Is(`{"swagger": "2.0", "info": {}}`))
	assert.True(t, Is(`{"swagger": 2.0}`), "an unquoted 2.0 is the same declaration")
	assert.False(t, Is(`{"openapi": "3.0.3"}`))
	assert.False(t, Is(`{"swagger": "1.2"}`))
	assert.False(t, Is("{not yaml: ["))
}

func TestConvert(t *testing.T) {
	out, err := Convert(swagger2YAML)
	require.NoError(t, err)
	loaded, err := openapi3.NewLoader().LoadFromData([]byte(out))
	require.NoError(t, err)
	require.NoError(t, loaded.Validate(t.Context()), "the converted document is valid OpenAPI 3")
	require.NotNil(t, loaded.Paths.Find("/orders/{id}"))
	assert.Equal(t, "getOrder", loaded.Paths.Find("/orders/{id}").Get.OperationID)
	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &doc))
	servers, _ := doc["servers"].([]any)
	require.Len(t, servers, 1)
	server, ok := servers[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "/api/v1", server["url"], "a basePath with no host is a relative server")

	withHost, err := Convert(`{"swagger":"2.0","info":{"title":"t","version":"1"},"host":"svc:8080","schemes":["http"],"basePath":"/v2","paths":{}}`)
	require.NoError(t, err)
	assert.Contains(t, withHost, `"url":"http://svc:8080/v2"`)

	unquoted, err := Convert("swagger: 2.0\ninfo: {title: t, version: \"1\"}\npaths: {}\n")
	require.NoError(t, err, "an unquoted 2.0 converts as the declaration it is")
	assert.Contains(t, unquoted, `"openapi":"3.0`)

	_, err = Convert(`{"openapi":"3.0.3"}`)
	assert.True(t, errors.Is(err, ErrNotSwagger))
	_, err = Convert(`{"swagger":"2.0","paths":"not an object"}`)
	assert.Error(t, err)
}

func TestStringKeys(t *testing.T) {
	got := stringKeys([]any{map[any]any{200: map[any]any{true: "x"}}})
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"200":{"true":"x"}}]`, string(raw))
}
