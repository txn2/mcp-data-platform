package gqlschema

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// errEmptySchema reports schema text with nothing in it, which is what an
// operator gets for pasting an empty box.
var errEmptySchema = errors.New("gqlschema: schema is empty")

// Schema is a loaded GraphQL schema plus the identity of the text it
// was loaded from. The hash is what an operation index is keyed on: a
// re-introspection that produces the same schema leaves the index and
// its embeddings alone, and one that does not rebuilds them.
type Schema struct {
	sdl  string
	hash string
	ast  *ast.Schema
}

// Load parses SDL into a schema. The text may be SDL an operator
// uploaded or SDL rendered from an introspection result; by this point
// the two are the same thing.
func Load(sdl string) (*Schema, error) {
	if strings.TrimSpace(sdl) == "" {
		return nil, errEmptySchema
	}
	parsed, err := gqlparser.LoadSchema(&ast.Source{Name: "upstream.graphql", Input: sdl})
	if err != nil {
		return nil, fmt.Errorf("gqlschema: loading schema: %w", err)
	}
	sum := sha256.Sum256([]byte(sdl))
	return &Schema{sdl: sdl, hash: hex.EncodeToString(sum[:]), ast: parsed}, nil
}

// LoadIntrospection normalizes an introspection result to SDL and loads
// it. This is the path a connection takes on register and on reconcile.
func LoadIntrospection(payload []byte) (*Schema, error) {
	sdl, err := SDLFromIntrospection(payload)
	if err != nil {
		return nil, err
	}
	return Load(sdl)
}

// LoadAny accepts either SDL or an introspection result and loads
// whichever it was given. The admin upload route takes both, and an
// operator pasting a schema should not have to declare which of the two
// they pasted.
func LoadAny(payload []byte) (*Schema, error) {
	if looksLikeIntrospection(payload) {
		return LoadIntrospection(payload)
	}
	return Load(string(payload))
}

// looksLikeIntrospection reports whether a payload is a JSON
// introspection result rather than SDL. SDL is not JSON, so the first
// non-space byte decides it.
func looksLikeIntrospection(payload []byte) bool {
	for _, b := range payload {
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		case '{':
			// SDL never opens with a brace: its first token is a
			// description, a keyword (type, schema, scalar) or a comment.
			return true
		default:
			return false
		}
	}
	return false
}

// SDL returns the schema text this schema was loaded from.
func (s *Schema) SDL() string { return s.sdl }

// Hash is the sha256 of the SDL, hex-encoded. It identifies the schema
// version an operation index and its embeddings belong to.
func (s *Schema) Hash() string { return s.hash }
