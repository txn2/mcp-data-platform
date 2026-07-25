package apigen

import (
	"encoding/json"
	"fmt"
)

// SpecJSON emits the OpenAPI 3.0.3 document for one tier. Output is
// deterministic: the document is built as nested maps and encoding/json
// sorts map keys, so regeneration is byte-identical. Schemas are inlined
// per operation (no components/schemas indirection) so every operation's
// schema is self-contained for api_get_endpoint_schema and per-endpoint
// tool generation alike.
func (c *Catalog) SpecJSON(tier int) ([]byte, error) {
	paths := map[string]any{}
	for _, op := range c.TierOperations(tier) {
		item, ok := paths[op.Path].(map[string]any)
		if !ok {
			item = map[string]any{}
			paths[op.Path] = item
		}
		item[op.Method] = operationObject(op)
	}
	doc := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "ACME Business Systems API",
			"description": "REST API over the ACME back-office systems: CRM, commerce, and supporting business domains.",
			"version":     "1.0.0",
		},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"apiKey": map[string]any{"type": "apiKey", "in": "header", "name": "X-API-Key"},
			},
		},
		"security": []any{map[string]any{"apiKey": []any{}}},
		"paths":    paths,
	}
	// Compact encoding: these are machine artifacts (registered with the
	// platform, read by tools), and compact keeps the committed t2 spec
	// at roughly half the indented size.
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal tier %d spec: %w", tier, err)
	}
	return append(raw, '\n'), nil
}

// operationObject builds one OpenAPI operation object.
func operationObject(op Operation) map[string]any {
	o := map[string]any{
		"operationId": op.ID,
		"summary":     op.Summary,
		"tags":        []any{op.Tag},
		"responses":   responsesObject(op),
	}
	if op.Deprecated {
		o["deprecated"] = true
	}
	if len(op.Params) > 0 {
		params := make([]any, 0, len(op.Params))
		for _, p := range op.Params {
			params = append(params, parameterObject(p))
		}
		o["parameters"] = params
	}
	if op.Request != nil {
		o["requestBody"] = map[string]any{
			"required": true,
			"content": map[string]any{
				"application/json": map[string]any{"schema": objectSchema(op.Request)},
			},
		}
	}
	return o
}

// parameterObject builds one OpenAPI parameter object.
func parameterObject(p Param) map[string]any {
	o := map[string]any{
		"name":        p.Name,
		"in":          p.In,
		"description": p.Desc,
		"schema":      fieldSchema(Field{Type: p.Type}),
	}
	if p.Required {
		o["required"] = true
	}
	return o
}

// responsesObject builds the responses map for an operation's kind.
func responsesObject(op Operation) map[string]any {
	if op.Kind == KindDelete {
		return map[string]any{"204": map[string]any{"description": "Deleted"}}
	}
	var schema map[string]any
	switch op.Kind {
	case KindList, KindSearch:
		schema = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": map[string]any{
					"type":        "array",
					"description": "One page of results",
					"items":       objectSchema(op.Response),
				},
				"next_cursor": map[string]any{
					"type":        "string",
					"description": "Cursor for the next page; absent on the last page",
				},
			},
		}
	case KindAggregate:
		schema = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"groups": map[string]any{
					"type":        "array",
					"description": "One entry per group",
					"items":       objectSchema(op.Response),
				},
			},
		}
	default:
		schema = objectSchema(op.Response)
	}
	status := "200"
	if op.Kind == KindCreate {
		status = "201"
	}
	responses := map[string]any{
		status: map[string]any{
			"description": "Successful response",
			"content": map[string]any{
				"application/json": map[string]any{"schema": schema},
			},
		},
	}
	if op.Forbidden {
		responses["403"] = forbiddenResponse()
	}
	return responses
}

// forbiddenResponse is the documented 403 on separately entitled areas. It
// is distinct from a successful empty collection, and the spec says so, so
// that "nothing provisioned" and "not allowed" are distinguishable from
// the contract alone.
func forbiddenResponse() map[string]any {
	return map[string]any{
		"description": "The credential is not entitled to this product area. Distinct from a successful response carrying an empty collection, which means the account has none of the resource.",
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": objectSchema([]Field{
					{Name: "error", Type: "string", Desc: "Human-readable reason the request was refused"},
				}),
			},
		},
	}
}

// objectSchema builds an object schema from fields.
func objectSchema(fields []Field) map[string]any {
	props := map[string]any{}
	for _, f := range fields {
		props[f.Name] = fieldSchema(f)
	}
	return map[string]any{"type": "object", "properties": props}
}

// fieldSchema maps a Field to its OpenAPI schema.
func fieldSchema(f Field) map[string]any {
	s := map[string]any{}
	if f.Type == "date-time" {
		s["type"] = "string"
		s["format"] = "date-time"
	} else {
		s["type"] = f.Type
	}
	if f.Desc != "" {
		s["description"] = f.Desc
	}
	return s
}
