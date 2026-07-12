package middleware

import (
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resolveJSON round-trips v through JSON (as it would travel over the wire) and
// returns it as the generic value a resolved schema validates.
func resolveJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

func mustResolve(t *testing.T, s *jsonschema.Schema) *jsonschema.Resolved {
	t.Helper()
	r, err := s.Resolve(nil)
	require.NoError(t, err)
	return r
}

// bodyType is a representative success-body struct: a couple of required fields
// plus an omitempty optional one.
type bodyType struct {
	Name  string   `json:"name"`
	Count int      `json:"count"`
	Notes []string `json:"notes,omitempty"`
}

func TestOpenToolOutputSchema_OpensAndDeclaresError(t *testing.T) {
	base, err := jsonschema.For[bodyType](nil)
	require.NoError(t, err)
	// jsonschema.For makes structs closed and marks non-omitempty fields required.
	require.NotNil(t, base.AdditionalProperties)
	require.NotEmpty(t, base.Required)

	got := OpenToolOutputSchema(base)

	// additionalProperties is opened to the empty ("true") schema.
	require.NotNil(t, got.AdditionalProperties)
	assert.Empty(t, got.AdditionalProperties.Type, "additionalProperties should be the open {} schema")
	// Nothing is required, so a body-less error envelope validates.
	assert.Nil(t, got.Required)
	// The shared error envelope is declared.
	_, hasError := got.Properties[errorEnvelopeKey]
	assert.True(t, hasError, "schema declares the shared error property")
	// Original body properties are preserved.
	assert.Contains(t, got.Properties, "name")
	assert.Contains(t, got.Properties, "count")
}

func TestOpenToolOutputSchema_ValidatesSuccessBody(t *testing.T) {
	schema := MustOutputSchema[bodyType]()
	resolved := mustResolve(t, schema)

	body := bodyType{Name: "alpha", Count: 3, Notes: []string{"n"}}
	assert.NoError(t, resolved.Validate(resolveJSON(t, body)),
		"a full success body validates against its own schema")
}

func TestOpenToolOutputSchema_ValidatesErrorEnvelope(t *testing.T) {
	schema := MustOutputSchema[bodyType]()
	resolved := mustResolve(t, schema)

	// The error contract replaces the body with just the {error} envelope
	// (BuildErrorResult). It must validate against the declared schema even
	// though none of the body fields are present.
	envelope := map[string]any{
		errorEnvelopeKey: errorPayload{
			Code:     CodeNotFound,
			Category: ErrCategoryNotFound,
			Message:  "nope",
			Hint:     "try again",
		},
	}
	assert.NoError(t, resolved.Validate(resolveJSON(t, envelope)),
		"the shared error envelope validates against the declared schema")
}

func TestOpenToolOutputSchema_AdmitsEnrichmentKeys(t *testing.T) {
	schema := MustOutputSchema[bodyType]()
	resolved := mustResolve(t, schema)

	// Enrichment mirrors open-ended top-level keys into structuredContent. The
	// open schema must admit them alongside the body.
	enriched := map[string]any{
		"name":             "alpha",
		"count":            1,
		"semantic_context": map[string]any{"description": "a table"},
		"related_memories": []any{map[string]any{"id": "m1"}},
	}
	assert.NoError(t, resolved.Validate(resolveJSON(t, enriched)),
		"middleware-injected enrichment keys validate against the open schema")
}

func TestBuildErrorResult_StructuredContentMatchesEnvelopeSchema(t *testing.T) {
	// Prove the actual BuildErrorResult output conforms to the shared fragment,
	// so a client branching on result.error is validating against a real shape.
	res := BuildErrorResult(NotFoundError(CodeNotFound, "missing", "look elsewhere"))
	require.NotNil(t, res.StructuredContent)

	// The error property fragment validates the envelope's error object.
	frag := mustResolve(t, ErrorEnvelopeProperty())
	scMap, ok := resolveJSON(t, res.StructuredContent).(map[string]any)
	require.True(t, ok)
	errObj, ok := scMap[errorEnvelopeKey]
	require.True(t, ok, "BuildErrorResult puts the error object under the shared key")
	assert.NoError(t, frag.Validate(errObj))
}

func TestMustOutputSchema_PanicsOnUnreflectableType(t *testing.T) {
	// A map with a non-string key cannot be reflected into a JSON Schema.
	assert.Panics(t, func() { _ = MustOutputSchema[map[int]string]() })
}

func TestOpenToolOutputSchema_NilBaseYieldsOpenObject(t *testing.T) {
	got := OpenToolOutputSchema(nil)
	assert.Equal(t, "object", got.Type)
	require.NotNil(t, got.AdditionalProperties)
	assert.Empty(t, got.AdditionalProperties.Type)
	_, ok := got.Properties[errorEnvelopeKey]
	assert.True(t, ok, "a nil base still gains the shared error property")
}

func TestOpenToolOutputSchema_PreservesCallerDeclaredError(t *testing.T) {
	// A base that already declares an "error" property keeps it rather than
	// being overwritten by the shared fragment.
	sentinel := &jsonschema.Schema{Type: "string"}
	base := &jsonschema.Schema{
		Type:       "object",
		Properties: map[string]*jsonschema.Schema{errorEnvelopeKey: sentinel},
	}
	got := OpenToolOutputSchema(base)
	assert.Same(t, sentinel, got.Properties[errorEnvelopeKey], "caller's error property is left intact")
}
