package tabletype

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Object is a JSON object with its keys in the order they were written.
// Decoding into a Go map loses that order, and the order is what a table's
// columns and a ROW's fields are declared in.
type Object struct {
	Keys   []string
	Values []any
}

// Get returns the value under a key, and whether the object holds one.
func (o *Object) Get(key string) (any, bool) {
	for i, k := range o.Keys {
		if k == key {
			return o.Values[i], true
		}
	}
	return nil, false
}

// ErrTooDeep is returned for a JSON value nested beyond what a table's type
// can be declared with.
var ErrTooDeep = fmt.Errorf("a value is nested more than %d levels deep", maxDepth)

// DecodeJSON decodes one JSON value into the values the inference reads: nil,
// bool, json.Number, string, []any and *Object. A second value after the first
// is an error, and so is one nested deeper than maxDepth.
func DecodeJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errTrailing
	}
	return v, nil
}

// errTrailing marks a second JSON value after the first.
var errTrailing = errors.New("more than one JSON value")

// IsTrailing reports whether DecodeJSON refused a second value after the
// first.
func IsTrailing(err error) bool { return errors.Is(err, errTrailing) }

func decodeValue(dec *json.Decoder, depth int) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err //nolint:wrapcheck // the decoder's text is the caller's to word
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	if depth >= maxDepth {
		return nil, ErrTooDeep
	}
	if delim == '{' {
		return decodeObject(dec, depth+1)
	}
	return decodeArray(dec, depth+1)
}

func decodeObject(dec *json.Decoder, depth int) (*Object, error) {
	obj := &Object{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err //nolint:wrapcheck // worded by the caller
		}
		key, _ := tok.(string)
		v, err := decodeValue(dec, depth)
		if err != nil {
			return nil, err
		}
		obj.Keys = append(obj.Keys, key)
		obj.Values = append(obj.Values, v)
	}
	_, err := dec.Token() // the closing brace
	return obj, err       //nolint:wrapcheck // worded by the caller
}

func decodeArray(dec *json.Decoder, depth int) ([]any, error) {
	out := make([]any, 0)
	for dec.More() {
		v, err := decodeValue(dec, depth)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	_, err := dec.Token() // the closing bracket
	return out, err       //nolint:wrapcheck // worded by the caller
}
