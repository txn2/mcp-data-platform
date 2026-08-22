package httpjson

import (
	"encoding/json"
	"fmt"
)

// OptionalInt is a JSON integer field that distinguishes the three states a
// PATCH-shaped request body can put it in: absent (leave the stored value
// alone), explicitly null (clear the stored value), and present with a number
// (write that number).
//
// A plain *int cannot carry this. encoding/json decodes both an absent field
// and an explicit null into a nil pointer, so a request that means "go back to
// the default" is indistinguishable from a request that never mentioned the
// field. Reaching for a sentinel number instead would spend a value the caller
// is entitled to send: the fields this guards -- an asset's version-retention
// cap first among them -- treat 0 and every positive number as meaningful.
//
// Marshaling is not implemented because this is a request-side type: it names
// what a caller asked for, and responses carry the stored value itself. It
// serves the MCP tool surfaces as well as the REST ones -- a tool argument is
// the same JSON with the same three states.
type OptionalInt struct {
	// Present reports that the field appeared in the payload at all.
	Present bool
	// Value is the number sent, or nil when the field was sent as null.
	Value *int
}

// UnmarshalJSON records that the field was present and captures its value.
func (o *OptionalInt) UnmarshalJSON(b []byte) error {
	o.Present = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("decoding integer field: %w", err)
	}
	o.Value = &n
	return nil
}

// Resolve renders the field as the pair an update struct carries: the value to
// write, and whether to reset the stored value instead. An absent field returns
// (nil, false), which leaves both untouched.
func (o OptionalInt) Resolve() (value *int, reset bool) {
	if !o.Present {
		return nil, false
	}
	if o.Value == nil {
		return nil, true
	}
	return o.Value, false
}
