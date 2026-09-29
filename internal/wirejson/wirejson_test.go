package wirejson

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type section struct {
	ID    string   `json:"id"`
	Items []string `json:"items"`
}

type collection struct {
	Sections []section         `json:"sections"`
	Tags     []string          `json:"tags,omitempty"`
	Meta     map[string]any    `json:"meta"`
	Labels   map[string]string `json:"labels"`
	Next     *section          `json:"next"`
	Raw      json.RawMessage   `json:"raw"`
	Blob     []byte            `json:"blob"`
}

// The case #1832 was filed on: a section holding nothing, and a collection
// holding no sections, one level up.
func TestMarshal_NilSlicesAreEmptyArrays(t *testing.T) {
	got, err := Marshal(collection{Sections: []section{{ID: "s1"}}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"sections":[{"id":"s1","items":[]}],"meta":null,"labels":null,"next":null,"raw":null,"blob":""}`, string(got))

	got, err = Marshal(collection{})
	require.NoError(t, err)
	assert.Contains(t, string(got), `"sections":[]`)
	assert.NotContains(t, string(got), `"tags"`, "omitempty still omits an empty slice")
}

// A nil slice reached through an interface, a map value, a pointer and a nested
// slice is normalized wherever it sits, which is what covers a response built
// as map[string]any.
func TestMarshal_NilSlicesAnywhereInTheValue(t *testing.T) {
	var none []string
	v := map[string]any{
		"direct":  none,
		"nested":  map[string]any{"deeper": []any{map[string]any{"rows": []map[string]any(nil)}}},
		"pointer": &section{ID: "p"},
		"grid":    [][]int{nil, {1}},
	}
	got, err := Marshal(v)
	require.NoError(t, err)
	assert.JSONEq(t, `{"direct":[],"nested":{"deeper":[{"rows":[]}]},"pointer":{"id":"p","items":[]},"grid":[[],[1]]}`, string(got))
}

type stamp struct{ at time.Time }

func (s stamp) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.at.Format(time.DateOnly) + `"`), nil
}

type level int

func (l level) MarshalText() ([]byte, error) { return []byte([]string{"low", "high"}[l]), nil }

type embedded struct {
	Inner string `json:"inner"`
}

type everything struct {
	embedded
	Name     string            `json:"name"`
	HTML     string            `json:"html"`
	Count    int64             `json:"count,string"`
	Ratio    float64           `json:"ratio"`
	Big      float64           `json:"big"`
	When     time.Time         `json:"when"`
	Stamp    stamp             `json:"stamp"`
	Level    level             `json:"level"`
	Levels   map[level]int     `json:"levels"`
	Sorted   map[string]int    `json:"sorted"`
	Skip     string            `json:"-"`
	Empty    string            `json:"empty,omitempty"`
	Any      any               `json:"any"`
	Raw      json.RawMessage   `json:"raw"`
	Blob     []byte            `json:"blob"`
	Array    [2]string         `json:"array"`
	Pointer  *int              `json:"pointer"`
	Nested   []everythingChild `json:"nested"`
	unexport string
}

type everythingChild struct {
	Key string `json:"key"`
}

// Everything that is not a nil slice encodes byte for byte as encoding/json
// encodes it: HTML escaping, the string option, map key order, times, the
// methods a type provides, embedded fields, omitempty, raw messages.
func TestMarshal_MatchesEncodingJSONOtherwise(t *testing.T) {
	seven := 7
	v := everything{
		embedded: embedded{Inner: "in"},
		Name:     "café", HTML: "<a href=\"x\">&</a>", Count: 42, Ratio: 0.1, Big: 1e21,
		When: time.Date(2026, 9, 28, 12, 0, 0, 5, time.UTC), Stamp: stamp{at: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
		Level: 1, Levels: map[level]int{0: 1, 1: 2}, Sorted: map[string]int{"b": 2, "a": 1, "c": 3},
		Skip: "x", Any: []any{1.5, "two", nil, true}, Raw: json.RawMessage(`{"z":1,"a":[1,2]}`),
		Blob: []byte("bytes"), Array: [2]string{"x", "y"}, Pointer: &seven,
		Nested: []everythingChild{{Key: "k"}}, unexport: "hidden",
	}
	want, err := json.Marshal(v)
	require.NoError(t, err)
	got, err := Marshal(v)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))

	wantIndent, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	gotIndent, err := MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	assert.Equal(t, string(wantIndent), string(gotIndent))

	var wantEnc, gotEnc bytes.Buffer
	require.NoError(t, json.NewEncoder(&wantEnc).Encode(v))
	require.NoError(t, Encode(&gotEnc, v))
	assert.Equal(t, wantEnc.String(), gotEnc.String())
}

func TestMarshal_ReportsWhatCannotBeEncoded(t *testing.T) {
	_, err := Marshal(map[string]any{"f": func() {}})
	require.Error(t, err)
	_, err = MarshalIndent(math.Inf(1), "", "  ")
	require.Error(t, err)
	require.Error(t, Encode(&bytes.Buffer{}, make(chan int)))
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestEncode_ReportsTheWritersError(t *testing.T) {
	require.EqualError(t, Encode(failingWriter{}, []string(nil)), "closed")
	var out bytes.Buffer
	require.NoError(t, Encode(&out, []string(nil)))
	assert.Equal(t, "[]\n", out.String())
}

type conflictA struct {
	X int
}

type conflictB struct {
	X int
}

type conflicting struct {
	conflictA
	conflictB
	Y int
}

type pointerMarshaler struct{ v int }

func (*pointerMarshaler) MarshalJSON() ([]byte, error) { return []byte(`"p"`), nil }

type holdsPointerMarshaler struct {
	P pointerMarshaler
	M map[string]pointerMarshaler
}

// encoding/json's quieter rules hold too: two embedded fields of one name drop
// each other, a pointer-receiver MarshalJSON is called only where the value is
// addressable, and integer map keys sort as their text.
func TestMarshal_MatchesEncodingJSONOnItsLegacyRules(t *testing.T) {
	for _, v := range []any{
		conflicting{conflictA{1}, conflictB{2}, 3},
		holdsPointerMarshaler{P: pointerMarshaler{1}, M: map[string]pointerMarshaler{"a": {}}},
		&holdsPointerMarshaler{P: pointerMarshaler{1}},
		map[int]string{10: "b", 2: "a"},
	} {
		want, wantErr := json.Marshal(v)
		got, err := Marshal(v)
		assert.Equal(t, wantErr == nil, err == nil, "%T", v)
		assert.Equal(t, string(want), string(got), "%T", v)
	}
}
