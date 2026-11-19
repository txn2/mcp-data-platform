package catalog

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/apidocs"
	"github.com/txn2/mcp-data-platform/internal/swagger2"
)

// #2721: a JSON spec is decoded as JSON, and the document is the one the
// loader's own reading produces. The platform's admin API, converted, is the
// largest spec the repository holds.
func TestLoadSpec_JSONReadsAsTheLoaderReadsIt(t *testing.T) {
	converted, err := swagger2.Convert(apidocs.SwaggerJSON())
	require.NoError(t, err)

	viaJSON, err := loadSpec(&openapi3.Loader{Context: context.Background()}, []byte(converted))
	require.NoError(t, err)
	viaLoader, err := (&openapi3.Loader{Context: context.Background()}).LoadFromData([]byte(converted))
	require.NoError(t, err)

	a, err := json.Marshal(viaJSON)
	require.NoError(t, err)
	b, err := json.Marshal(viaLoader)
	require.NoError(t, err)
	assert.JSONEq(t, string(b), string(a), "the JSON reading and the loader's reading are one document")
	assert.NotEmpty(t, viaJSON.Paths.Map())
}

// A YAML spec still loads, references resolved; a JSON spec with a duplicate
// key is refused, as the loader refused it before.
func TestLoadSpec_YAMLAndDuplicateKeys(t *testing.T) {
	yamlSpec := "openapi: 3.0.0\ninfo:\n  title: t\n  version: '1'\npaths:\n  /a:\n    get:\n      operationId: getA\n      responses:\n        '200':\n          description: ok\n          content:\n            application/json:\n              schema:\n                $ref: '#/components/schemas/A'\ncomponents:\n  schemas:\n    A:\n      type: object\n"
	doc, err := ParseSpec(yamlSpec)
	require.NoError(t, err)
	op := doc.Paths.Find("/a").Get
	require.NotNil(t, op)
	assert.NotNil(t, op.Responses.Status(200).Value.Content["application/json"].Schema.Value, "the reference is resolved")

	dup := `{"openapi":"3.0.0","info":{"title":"first","version":"1","title":"last"},"paths":{}}`
	_, err = ParseSpec(dup)
	require.ErrorIs(t, err, ErrInvalidContent)
	_, loaderErr := (&openapi3.Loader{Context: context.Background()}).LoadFromData([]byte(dup))
	require.Error(t, loaderErr, "the loader refuses it too")

	_, err = ParseSpec(`{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{"/a":{"get":{"responses":{"200":{"$ref":"#/components/responses/missing"}}}}}}`)
	require.Error(t, err, "an unresolved reference in a JSON spec is refused")
	assert.ErrorIs(t, err, ErrInvalidContent)
}

// The parse budget holds the bytes parsed at once: a spec larger than the
// budget takes all of it, so a second parse waits for the first to release.
func TestAcquireParse_HoldsTheBudget(t *testing.T) {
	release := acquireParse(parseBudget * 2)
	acquired := make(chan func())
	go func() { acquired <- acquireParse(1) }()
	select {
	case <-acquired:
		t.Fatal("a parse started while a larger-than-budget parse held the whole budget")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case r := <-acquired:
		r()
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting parse never started after the release")
	}

	small := acquireParse(1 << 20)
	other := acquireParse(1 << 20)
	small()
	other()
	assert.True(t, parseSlots.TryAcquire(parseBudget), "every release returned its bytes")
	parseSlots.Release(parseBudget)
}
