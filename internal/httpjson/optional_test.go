package httpjson

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// body is a stand-in for the request structs that carry an OptionalInt.
type body struct {
	MaxVersions OptionalInt `json:"max_versions"`
}

func TestOptionalInt_DistinguishesAbsentFromNull(t *testing.T) {
	twentyFive, minusOne := 25, -1
	tests := []struct {
		name        string
		payload     string
		wantValue   *int
		wantClear   bool
		wantPresent bool
	}{
		{"absent leaves the stored value alone", `{}`, nil, false, false},
		{"null clears the stored value", `{"max_versions": null}`, nil, true, true},
		{"zero is a value, not an absence", `{"max_versions": 0}`, new(int), false, true},
		{"a number is written through", `{"max_versions": 25}`, &twentyFive, false, true},
		{"a negative number decodes and is left for validation", `{"max_versions": -1}`, &minusOne, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var b body
			require.NoError(t, json.Unmarshal([]byte(tc.payload), &b))
			assert.Equal(t, tc.wantPresent, b.MaxVersions.Present)

			value, reset := b.MaxVersions.Resolve()
			assert.Equal(t, tc.wantClear, reset)
			if tc.wantValue == nil {
				assert.Nil(t, value)
				return
			}
			require.NotNil(t, value)
			assert.Equal(t, *tc.wantValue, *value)
		})
	}
}

func TestOptionalInt_RejectsANonInteger(t *testing.T) {
	var b body
	err := json.Unmarshal([]byte(`{"max_versions": "many"}`), &b)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "integer")
}
