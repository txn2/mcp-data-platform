// Package swagger2 converts a Swagger 2.0 document to the OpenAPI 3 the API
// gateway reads (#2005). Many generators (swaggo/swag, the usual one for Go
// APIs) still emit 2.0, and an operator pasting one should not have to know
// that before saving it. A catalog spec saved or refreshed as 2.0, and the
// platform's own admin API document, go through the one conversion here.
package swagger2

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// ConvertedFrom names the format a spec's content was converted from when it
// was supplied as Swagger 2.0.
const ConvertedFrom = "swagger 2.0"

// ErrNotSwagger is returned by Convert for a document that does not declare
// `swagger: "2.0"`.
var ErrNotSwagger = errors.New("swagger2: not a Swagger 2.0 document")

// Is reports whether raw, JSON or YAML, declares `swagger: "2.0"`.
func Is(raw string) bool {
	var head struct {
		Swagger any `json:"swagger" yaml:"swagger"`
	}
	if err := yaml.Unmarshal([]byte(raw), &head); err != nil {
		return false
	}
	switch v := head.Swagger.(type) {
	case string:
		return v == "2.0"
	case float64:
		// An unquoted 2.0 is read as a number.
		return v == 2
	default:
		return false
	}
}

// Convert converts a Swagger 2.0 document, JSON or YAML, to OpenAPI 3 JSON.
func Convert(raw string) (string, error) {
	if !Is(raw) {
		return "", ErrNotSwagger
	}
	asJSON, err := yamlToJSON(raw)
	if err != nil {
		return "", fmt.Errorf("decoding swagger 2.0: %w", err)
	}
	var v2 openapi2.T
	if err := json.Unmarshal(asJSON, &v2); err != nil {
		return "", fmt.Errorf("decoding swagger 2.0: %w", err)
	}
	v3, err := openapi2conv.ToV3(&v2)
	if err != nil {
		return "", fmt.Errorf("converting swagger 2.0 to openapi 3.0: %w", err)
	}
	// The converter writes basePath into servers[0].url only when the
	// document names a host. A document with a basePath and no host keeps
	// it as a relative server URL, which the gateway takes its path prefix
	// from, so a refresh of a document whose basePath changed moves with it.
	if len(v3.Servers) == 0 && v2.BasePath != "" && v2.BasePath != "/" {
		v3.Servers = openapi3.Servers{{URL: v2.BasePath}}
	}
	out, err := v3.MarshalJSON()
	if err != nil {
		return "", fmt.Errorf("encoding openapi 3.0: %w", err)
	}
	return string(out), nil
}

// yamlToJSON re-encodes a YAML (or JSON, which is YAML) document as JSON.
func yamlToJSON(raw string) ([]byte, error) {
	var doc any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, err //nolint:wrapcheck // the caller wraps it
	}
	doc = stringKeys(doc)
	// An unquoted `swagger: 2.0` is a number to YAML, and the document model
	// takes the version as a string; Is has already read it as 2.0.
	if m, ok := doc.(map[string]any); ok {
		m["swagger"] = "2.0"
	}
	return json.Marshal(doc) //nolint:wrapcheck // the caller wraps it
}

// stringKeys turns every mapping key into a string. YAML reads an unquoted
// response code (`200:`) as an integer key, which JSON cannot encode.
func stringKeys(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			t[k] = stringKeys(child)
		}
		return t
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			out[fmt.Sprint(k)] = stringKeys(child)
		}
		return out
	case []any:
		for i, child := range t {
			t[i] = stringKeys(child)
		}
		return t
	default:
		return v
	}
}
