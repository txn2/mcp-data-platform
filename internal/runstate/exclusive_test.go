package runstate

import (
	"encoding/json"
	"testing"
)

// TestParseExclusive reads every form the setting arrives in: the mode's
// name, and the boolean it was before it had a third value (#2209).
func TestParseExclusive(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want ExclusiveMode
	}{
		{"off", ExclusiveOff},
		{"refuse", ExclusiveRefuse},
		{"queue", ExclusiveQueue},
		{" Queue ", ExclusiveQueue},
		{true, ExclusiveRefuse},
		{false, ExclusiveOff},
		{"true", ExclusiveRefuse},
		{"false", ExclusiveOff},
		{"", ExclusiveOff},
	} {
		got, err := ParseExclusive(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseExclusive(%v) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []any{"sometimes", 1, nil} {
		if _, err := ParseExclusive(bad); err == nil {
			t.Errorf("ParseExclusive(%v) accepted", bad)
		}
	}
}

func TestExclusiveModeJSON(t *testing.T) {
	var v struct {
		E ExclusiveMode `json:"e"`
	}
	for body, want := range map[string]ExclusiveMode{`{"e":true}`: ExclusiveRefuse, `{"e":"queue"}`: ExclusiveQueue, `{"e":false}`: ExclusiveOff} {
		if err := json.Unmarshal([]byte(body), &v); err != nil || v.E != want {
			t.Errorf("%s = %q, %v", body, v.E, err)
		}
	}
	if err := json.Unmarshal([]byte(`{"e":"x"}`), &v); err == nil {
		t.Error("an unknown mode is refused")
	}
	if err := json.Unmarshal([]byte(`{"e":`), &v); err == nil {
		t.Error("malformed JSON is refused")
	}
	out, err := json.Marshal(struct {
		E ExclusiveMode `json:"e"`
	}{})
	if err != nil || string(out) != `{"e":"off"}` {
		t.Errorf("the zero value marshals as off; got %s, %v", out, err)
	}
}
