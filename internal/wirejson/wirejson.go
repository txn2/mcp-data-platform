// Package wirejson is the one encoder for JSON the platform answers with: every
// REST response body and every MCP tool result (#1832).
//
// It encodes exactly as encoding/json does, with one difference: a nil slice is
// an empty array, [], never null. Go gives a slice that nothing was appended to
// the value nil, and so does a lookup of a map key that is not there, and
// encoding/json writes that as null. A caller iterating the field then fails
// on it: a managed script's `for it in section.get("items", [])` gets None,
// because the key is present, and stops with "NoneType value is not
// iterable". Normalizing here covers every slice field a response carries, the
// ones written before this package and every one added after, where a fix per
// field covers only the fields someone remembered.
//
// A nil map is still null, and a nil []byte is "" (the empty base64 string)
// rather than null. json.RawMessage keeps its own encoding: a nil one is null.
//
// test/structure holds every response to this encoder: a function that writes
// an http.ResponseWriter, returns an MCP tool result, or builds its text block
// may not call encoding/json's Marshal, MarshalIndent or NewEncoder.
package wirejson

import (
	"bytes"
	stdjson "encoding/json"
	"io"

	jsonv2 "github.com/go-json-experiment/json"
	jsonv1 "github.com/go-json-experiment/json/v1"
)

// options are encoding/json's own, as the v2 module that became
// encoding/json/v2 reproduces them, with nil slices written as [].
var options = jsonv2.JoinOptions(jsonv1.DefaultOptionsV1(), jsonv2.FormatNilSliceAsNull(false))

// Marshal returns the JSON encoding of v, as json.Marshal does, with every nil
// slice written as [].
func Marshal(v any) ([]byte, error) {
	return jsonv2.Marshal(v, options) //nolint:wrapcheck // the encoder's error names the value it could not encode
}

// MarshalIndent is Marshal with each element on its own line, as
// json.MarshalIndent formats it.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	b, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := stdjson.Indent(&out, b, prefix, indent); err != nil {
		return nil, err //nolint:wrapcheck // Marshal's own output always indents
	}
	return out.Bytes(), nil
}

// Encode writes the JSON encoding of v to w followed by a newline, as
// json.NewEncoder(w).Encode(v) does.
func Encode(w io.Writer, v any) error {
	b, err := Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err //nolint:wrapcheck // the writer's own error
}
