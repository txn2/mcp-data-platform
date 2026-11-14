package callrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// gateway answers for one connection whose catalog holds getThing at
// GET /things/{id}.
type gateway struct{}

func (gateway) OperationKey(_ context.Context, connection, method, path, operationID string) string {
	if connection != "c" {
		return ""
	}
	if operationID == "getThing" || (method == "GET" && len(path) > len("/things/")) {
		return "getThing"
	}
	return ""
}

func (gateway) ResolveOperationRequest(_ context.Context, connection, operationID, _ string, params map[string]string) (method, path string, ok bool) {
	if connection != "c" || operationID != "getThing" {
		return "", "", false
	}
	return "GET", "/things/" + params["id"], true
}

func (gateway) OperationTemplate(_ context.Context, connection, method, _ string) string {
	if connection == "c" && method == "GET" {
		return "/things/{id}"
	}
	return ""
}

// TestAPIShapes reads what a call addresses without sending it: by
// operation_id, by a method and path the catalog templates, and nothing on a
// connection the gateway does not serve.
func TestAPIShapes(t *testing.T) {
	s := APIShapes{API: gateway{}}
	ctx := context.Background()
	got := s.CallShape(ctx, "c", map[string]any{"operation_id": "getThing", "path_params": map[string]any{"id": "abc", "n": 2}})
	assert.Equal(t, []string{"getThing", "GET", "/things/abc"}, []string{got.Operation, got.Method, got.Path})
	assert.Equal(t, map[string]string{"id": "abc"}, got.Params, "only string parameters are read")
	got = s.CallShape(ctx, "c", map[string]any{"method": " get ", "path": "/things/xyz?expand=1"})
	assert.Equal(t, []string{"getThing", "GET", "/things/xyz"}, []string{got.Operation, got.Method, got.Path})
	assert.Equal(t, map[string]string{"id": "xyz"}, got.Params)
	got = s.CallShape(ctx, "nope", map[string]any{"method": "DELETE", "path": "/x"})
	assert.Empty(t, got.Operation)
	assert.Empty(t, got.Params)
	got = s.CallShape(ctx, "c", map[string]any{"operation_id": "made-up", "method": "POST"})
	assert.Empty(t, got.Operation)
	assert.Equal(t, "POST", got.Method, "an id that resolves to nothing keeps the method sent")
}
