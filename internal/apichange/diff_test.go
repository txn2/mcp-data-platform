package apichange

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// specV1 and specV2 are one API before and after a vendor release: listPets
// gains a parameter, getPet is deprecated, deletePet is removed, createPet
// added, a response schema moves, and info.version changes.
const specV1 = `openapi: 3.0.3
info: {title: Pets, version: "1.0"}
paths:
  /pets:
    get:
      operationId: listPets
      summary: List pets
      parameters:
        - {name: limit, in: query, schema: {type: integer}}
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: array, items: {$ref: "#/components/schemas/Pet"}}
  /pets/{id}:
    parameters:
      - {name: id, in: path, required: true, schema: {type: string}}
    get:
      operationId: getPet
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {$ref: "#/components/schemas/Pet"}
    delete:
      operationId: deletePet
      responses: {"204": {description: gone}}
  /health:
    get:
      responses: {"200": {description: ok}}
components:
  schemas:
    Pet:
      type: object
      required: [id]
      properties:
        id: {type: string}
        name: {type: string}
`

const specV2 = `openapi: 3.0.3
info: {title: Pets, version: "2.0"}
paths:
  /pets:
    get:
      operationId: listPets
      summary: List pets
      parameters:
        - {name: limit, in: query, schema: {type: integer}}
        - {name: species, in: query, required: true, schema: {type: string}}
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {type: array, items: {$ref: "#/components/schemas/Pet"}}
    post:
      operationId: createPet
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: "#/components/schemas/Pet"}
      responses: {"201": {description: made}}
  /pets/{id}:
    parameters:
      - {name: id, in: path, required: true, schema: {type: string}}
    get:
      operationId: getPet
      deprecated: true
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {$ref: "#/components/schemas/Pet"}
  /health:
    get:
      responses: {"200": {description: ok}}
components:
  schemas:
    Pet:
      type: object
      required: [id, name]
      properties:
        id: {type: string}
        name: {type: string}
`

func ids(ops []Op) []string {
	out := make([]string, 0, len(ops))
	for _, o := range ops {
		out = append(out, o.ID)
	}
	return out
}

func TestCompare_NamesEveryKindOfChange(t *testing.T) {
	r, err := Compare(specV1, specV2)
	require.NoError(t, err)
	assert.Equal(t, "1.0", r.FromVersion)
	assert.Equal(t, "2.0", r.ToVersion)
	assert.Equal(t, []string{"createPet"}, ids(r.Added))
	assert.Equal(t, []string{"deletePet"}, ids(r.Removed))
	assert.Equal(t, []string{"getPet"}, ids(r.Deprecated))

	changed := map[string][]string{}
	for _, c := range r.Changed {
		changed[c.ID] = c.Parts
	}
	assert.Equal(t, []string{PartParameters, PartResponses}, changed["listPets"],
		"a new required parameter, and the Pet schema the array holds now requires name")
	assert.Equal(t, []string{PartResponses}, changed["getPet"], "the referenced schema moved")
	assert.NotContains(t, changed, "GET /health", "an unchanged operation is not reported")
	assert.False(t, r.Empty())
	assert.Equal(t, "1 added, 1 removed, 2 changed, 1 deprecated; version 1.0 to 2.0", r.Counts())
	assert.Equal(t, "DELETE", r.Removed[0].Method)
	assert.Equal(t, "/pets/{id}", r.Removed[0].Path)
}

func TestCompare_AFormattingChangeIsEmpty(t *testing.T) {
	reformatted := strings.ReplaceAll(specV1, "summary: List pets", "summary:   List pets")
	r, err := Compare(specV1, reformatted+"\n\n")
	require.NoError(t, err)
	assert.True(t, r.Empty())
	assert.Equal(t, []Op{}, r.Added, "an empty list is [], never null")
	assert.Equal(t, "0 added, 0 removed, 0 changed, 0 deprecated", r.Counts())
}

func TestCompare_AnOperationWithNoIDIsNamedByMethodAndPath(t *testing.T) {
	r, err := Compare(specV1, strings.Replace(specV1, "  /health:\n    get:\n      responses: {\"200\": {description: ok}}\n", "", 1))
	require.NoError(t, err)
	assert.Equal(t, []string{"GET /health"}, ids(r.Removed))
}

func TestCompare_AVersionChangeAloneIsReported(t *testing.T) {
	r, err := Compare(specV1, strings.Replace(specV1, `version: "1.0"`, `version: "1.1"`, 1))
	require.NoError(t, err)
	assert.False(t, r.Empty())
	assert.Contains(t, r.Counts(), "version 1.0 to 1.1")
	assert.Equal(t, "version (none) to 1.0", Report{ToVersion: "1.0"}.Counts()[len("0 added, 0 removed, 0 changed, 0 deprecated; "):])
}

func TestCompare_RefusesWhatDoesNotParse(t *testing.T) {
	_, err := Compare("not: [a spec", specV1)
	require.ErrorContains(t, err, "the copy in use")
	_, err = Compare(specV1, "openapi: 3.0.3\ninfo: {title: x}\npaths: 7\n")
	require.ErrorContains(t, err, "the fetched spec")
}

func TestCompare_ARecursiveSchemaEnds(t *testing.T) {
	tree := func(extra string) string {
		return `openapi: 3.0.3
info: {title: T, version: "1"}
paths:
  /nodes:
    get:
      operationId: listNodes
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema: {$ref: "#/components/schemas/Node"}
components:
  schemas:
    Node:
      type: object
      properties:
        children: {type: array, items: {$ref: "#/components/schemas/Node"}}
        next: {oneOf: [{$ref: "#/components/schemas/Node"}, {type: string}]}` + extra + "\n"
	}
	r, err := Compare(tree(""), tree("\n        label: {type: string, enum: [a, b], nullable: true}"))
	require.NoError(t, err)
	require.Len(t, r.Changed, 1)
	assert.Equal(t, []string{PartResponses}, r.Changed[0].Parts)
}

func TestCompare_ARequestBodyChangeIsNamed(t *testing.T) {
	r, err := Compare(specV2, strings.Replace(specV2, "      requestBody:\n        required: true", "      requestBody:\n        required: false", 1))
	require.NoError(t, err)
	require.Len(t, r.Changed, 1)
	assert.Equal(t, "createPet", r.Changed[0].ID)
	assert.Equal(t, []string{PartRequestBody}, r.Changed[0].Parts)
}

func TestCompare_ListsAreBounded(t *testing.T) {
	b := strings.Builder{}
	b.Grow(64 * (maxListed + 5))
	_, _ = b.WriteString("openapi: 3.0.3\ninfo: {title: Many, version: \"1\"}\npaths:\n")
	for i := range maxListed + 5 {
		fmt.Fprintf(&b, "  /r%d:\n    get:\n      operationId: op%04d\n      responses: {\"200\": {description: ok}}\n", i, i)
	}
	r, err := Compare("openapi: 3.0.3\ninfo: {title: Many, version: \"1\"}\npaths: {}\n", b.String())
	require.NoError(t, err)
	assert.Len(t, r.Added, maxListed)
	assert.True(t, r.Truncated)
}

func TestClean_BoundsAndStripsUntrustedText(t *testing.T) {
	assert.Equal(t, "ab", clean("a\u202eb"), "a bidirectional override is removed")
	assert.Equal(t, "a b", clean("a\nb"))
	long := clean(strings.Repeat("é", maxText))
	assert.LessOrEqual(t, len(long), maxText)
	assert.True(t, strings.HasPrefix(strings.Repeat("é", maxText), long))
}
