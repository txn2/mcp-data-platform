package scriptrec

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func recorded(t *testing.T) []byte {
	t.Helper()
	r := NewRecorder(Header{RunID: "dpx_1", Params: map[string]any{"conn": "orders"}})
	r.OnCall("trino_query", map[string]any{"connection": "orders", "sql": "SELECT 1"}, map[string]any{"rows": []any{}}, nil)
	r.OnCall("api_invoke_endpoint", map[string]any{"connection": "billing", "headers": map[string]any{"Authorization": "Bearer {{secret:stripe-key}}"}}, map[string]any{"status": 200.0}, nil)
	data, reason := r.Finish()
	require.Empty(t, reason)
	return data
}

func TestRenamedMapsArgumentsAndRebuildsKeys(t *testing.T) {
	data := recorded(t)
	rename := func(s string) string {
		switch {
		case s == "orders":
			return "orders-prod"
		case strings.Contains(s, "{{secret:stripe-key}}"):
			return strings.ReplaceAll(s, "{{secret:stripe-key}}", "{{secret:stripe-live}}")
		}
		return s
	}
	out, err := Renamed(data, rename)
	require.NoError(t, err)
	rec, err := Decode(out)
	require.NoError(t, err)
	assert.Equal(t, "orders-prod", rec.Params["conn"])
	require.Len(t, rec.Calls, 2)
	q := map[string]any{"connection": "orders-prod", "sql": "SELECT 1"}
	assert.Equal(t, q, rec.Calls[0].Args)
	assert.Equal(t, ToolKey("trino_query", q), rec.Calls[0].Key, "a renamed call is found by the renamed source's call")
	headers, ok := rec.Calls[1].Args["headers"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Bearer {{secret:stripe-live}}", headers["Authorization"])
	assert.Equal(t, "billing", rec.Calls[1].Args["connection"])

	same, err := Renamed(data, func(s string) string { return s })
	require.NoError(t, err)
	assert.True(t, bytes.Equal(data, same), "nothing renamed returns the recording as given")
	assert.True(t, Same(data, same))
	assert.False(t, Same(data, out))
	assert.False(t, Same(data, []byte("not gzip")))

	_, err = Renamed([]byte("not gzip"), rename)
	require.Error(t, err)
}

func TestEncodeRoundTripsAndRefusesOverTheCap(t *testing.T) {
	rec, err := Decode(recorded(t))
	require.NoError(t, err)
	out, err := Encode(rec)
	require.NoError(t, err)
	again, err := Decode(out)
	require.NoError(t, err)
	assert.Equal(t, rec, again)

	big := &Recording{Calls: []Call{{Tool: "x", Args: map[string]any{"blob": randomText(2 * MaxBytes)}}}}
	_, err = Encode(big)
	require.ErrorContains(t, err, "over the")
}

// randomText is n bytes that gzip cannot shrink much.
func randomText(n int) string {
	var b strings.Builder
	x := uint32(2463534242)
	for b.Len() < n {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		_ = b.WriteByte(byte('!' + x%90))
	}
	return b.String()
}
