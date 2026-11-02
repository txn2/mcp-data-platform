package audit

import (
	"encoding/json"
	"fmt"
	"maps"
)

// boundValueTools are the tools whose arguments carry values bound to a SQL
// statement as parameters (#2400): the postgres kind's postgres_query and
// postgres_execute. Their statement is recorded and their values never are; a
// value bound from a decrypted form field is the case this protects. The
// postgres package's tests hold this list to its tool names.
var boundValueTools = map[string]bool{"postgres_query": true, "postgres_execute": true}

// Argument keys of a bound-value tool.
const (
	argParams     = "params"
	argStatements = "statements"
)

// IsBoundValueTool reports whether tool binds the values in its params
// argument to a statement.
func IsBoundValueTool(tool string) bool { return boundValueTools[tool] }

// SanitizeToolParameters is what audit_logs.parameters keeps of one call's
// arguments: the values a bound-value tool binds replaced by their types
// (ShapeBoundValues), then every secret replaced (SanitizeParameters).
func SanitizeToolParameters(tool string, params map[string]any) map[string]any {
	return SanitizeParameters(ShapeBoundValues(tool, params))
}

// ShapeBoundValues returns a copy of a tool call's arguments in which the
// values a bound-value tool binds are replaced by the JSON type each was sent
// as: a list of values keeps its positions ($1, $2, ...), an object its names.
// The arguments of any other tool are returned as they are.
//
// SanitizeToolParameters runs it before SanitizeParameters, on the arguments
// the audit row, and the call record built from it, keep.
func ShapeBoundValues(tool string, params map[string]any) map[string]any {
	if !boundValueTools[tool] || params == nil {
		return params
	}
	out := make(map[string]any, len(params))
	maps.Copy(out, params)
	if p, ok := params[argParams]; ok {
		out[argParams] = paramShapes(p)
	}
	if list, ok := params[argStatements].([]any); ok {
		out[argStatements] = shapeStatements(list)
	}
	return out
}

// shapeStatements shapes each statement's params in a statements list.
func shapeStatements(list []any) []any {
	shaped := make([]any, len(list))
	for i, item := range list {
		stmt, ok := item.(map[string]any)
		if !ok {
			shaped[i] = jsonTypeName(item)
			continue
		}
		copied := make(map[string]any, len(stmt))
		maps.Copy(copied, stmt)
		if p, ok := stmt[argParams]; ok {
			copied[argParams] = paramShapes(p)
		}
		shaped[i] = copied
	}
	return shaped
}

// paramShapes is params with every value replaced by its JSON type.
func paramShapes(params any) any {
	switch t := params.(type) {
	case []any:
		out := make(map[string]any, len(t))
		for i, v := range t {
			out[fmt.Sprintf("$%d", i+1)] = jsonTypeName(v)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, v := range t {
			out[k] = jsonTypeName(v)
		}
		return out
	case nil:
		return nil
	default:
		return jsonTypeName(params)
	}
}

// jsonTypeName names a decoded JSON value's type.
func jsonTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, float32, int, int64, int32, json.Number:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "value"
	}
}
