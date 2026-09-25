package jsonpath

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	var v any
	require.NoError(t, d.Decode(&v))
	return v
}

func TestLookup(t *testing.T) {
	doc := decode(t, `{"data":{"events":[{"id":7},{"id":"b"}],"a.b":true},"n":null}`)
	tests := []struct {
		path  string
		want  string
		found bool
	}{
		{"$", `{"data":{"a.b":true,"events":[{"id":7},{"id":"b"}]},"n":null}`, true},
		{"$.data.events[0].id", "7", true},
		{"data.events[1].id", "b", true},
		{"$.data['a.b']", "true", true},
		{`$["data"]["events"][1]["id"]`, "b", true},
		{"$.n", "", true},
		{"$.data.events[2]", "", false},
		{"$.data.events.id", "", false},
		{"$.missing", "", false},
		{"$.data.events[0].id.x", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			p, err := Parse(tt.path)
			require.NoError(t, err)
			v, ok := p.Lookup(doc)
			assert.Equal(t, tt.found, ok)
			if ok {
				assert.Equal(t, tt.want, Scalar(v))
			}
		})
	}
}

func TestParseRefuses(t *testing.T) {
	for _, bad := range []string{"", "  ", "$.", "$.a..b", "$[", "$[x]", "$[-1]", "$.a[1"} {
		_, err := Parse(bad)
		assert.Error(t, err, bad)
	}
}

func TestParseKeepsText(t *testing.T) {
	p, err := Parse(" $.a ")
	require.NoError(t, err)
	assert.Equal(t, "$.a", p.String())
}

func TestScalar(t *testing.T) {
	assert.Equal(t, "false", Scalar(false))
	assert.Equal(t, "[1,2]", Scalar([]any{json.Number("1"), json.Number("2")}))
	assert.Equal(t, "", Scalar(func() {}))
}
