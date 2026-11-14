package callrun

import (
	"context"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/cfgmap"
	"github.com/txn2/mcp-data-platform/internal/pathtemplate"
)

// APIOperations is what the api gateway answers about a call on one of its
// connections, read from its arguments without sending it
// (apigateway.Toolkit).
type APIOperations interface {
	OperationKey(ctx context.Context, connection, method, path, operationID string) string
	ResolveOperationRequest(ctx context.Context, connection, operationID, spec string, pathParams map[string]string) (method, path string, ok bool)
	OperationTemplate(ctx context.Context, connection, method, path string) string
}

// APIShapes reads an api call's shape through the gateway (#2436): the
// catalog operation (its id, else "METHOD template", else ""), the method,
// the path with an operation_id's parameters filled in, and the path's
// parameters by name. It is what a tool-call handler is matched against and
// given, before any stored secret is filled.
type APIShapes struct{ API APIOperations }

// Shapes reads what an api call addresses (APIShapes).
type Shapes interface {
	CallShape(ctx context.Context, connection string, args map[string]any) Shape
}

// Shape is what an api call addresses: its catalog operation, method, path
// and path parameters.
type Shape struct {
	Operation, Method, Path string
	Params                  map[string]string
}

// CallShape is the call's operation, method, path and path parameters.
func (s APIShapes) CallShape(ctx context.Context, connection string, args map[string]any) Shape {
	method := strings.ToUpper(strings.TrimSpace(cfgmap.String(args, "method")))
	path := cfgmap.String(args, "path")
	if opID := cfgmap.String(args, "operation_id"); opID != "" {
		given := cfgmap.StringMap(args, "path_params")
		if m, p, ok := s.API.ResolveOperationRequest(ctx, connection, opID, cfgmap.String(args, "spec"), given); ok {
			method, path = m, p
		}
		return Shape{Operation: s.API.OperationKey(ctx, connection, "", "", opID), Method: method, Path: path, Params: given}
	}
	path = pathtemplate.StripQuery(path)
	params := map[string]string{}
	if tpl := s.API.OperationTemplate(ctx, connection, method, path); tpl != "" {
		params = pathtemplate.Params(tpl, path)
	}
	return Shape{Operation: s.API.OperationKey(ctx, connection, method, path, ""), Method: method, Path: path, Params: params}
}
