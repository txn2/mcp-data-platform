package cfgmap

import (
	"testing"
	"time"
)

func TestBool(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want bool
	}{
		{"native true", true, true},
		{"native false", false, false},
		{"string true", "true", true},
		{"string 1", "1", true},
		{"string false", "false", false},
		{"string garbage", "nope", false},
		{"absent", nil, false},
		{"wrong type", 42, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := map[string]any{}
			if tt.val != nil {
				cfg["k"] = tt.val
			}
			if got := Bool(cfg, "k"); got != tt.want {
				t.Errorf("Bool(%v) = %v; want %v", tt.val, got, tt.want)
			}
		})
	}
}

// TestString covers the two shapes a connection config presents a
// string in: the value the operator saved, and the absence of one.
func TestString(t *testing.T) {
	cfg := map[string]any{"present": "value", "wrong-type": 42}
	if got := String(cfg, "present"); got != "value" {
		t.Errorf("String(present) = %q; want %q", got, "value")
	}
	if got := String(cfg, "absent"); got != "" {
		t.Errorf("String(absent) = %q; want empty", got)
	}
	if got := String(cfg, "wrong-type"); got != "" {
		t.Errorf("String(wrong-type) = %q; want empty", got)
	}
	if got := String(nil, "any"); got != "" {
		t.Errorf("String(nil map) = %q; want empty", got)
	}
}

// TestStringDefault pins the empty-string fallback, which is the whole
// reason this reader is distinct from String: an admin form that
// submits every field writes "" for the ones left blank, and those
// must take the default rather than override it with emptiness.
func TestStringDefault(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"set", map[string]any{"k": "given"}, "given"},
		{"empty string falls back", map[string]any{"k": ""}, "fallback"},
		{"absent falls back", map[string]any{}, "fallback"},
		{"wrong type falls back", map[string]any{"k": 7}, "fallback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StringDefault(tc.cfg, "k", "fallback"); got != tc.want {
				t.Errorf("StringDefault = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestDuration covers every wire shape a duration arrives in. The bare
// number cases are what a JSON round-trip of an operator's "30"
// produces, and they must read as seconds rather than nanoseconds.
func TestDuration(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want time.Duration
	}{
		{"duration string", "45s", 45 * time.Second},
		{"minutes string", "2m", 2 * time.Minute},
		{"unparseable string falls back", "later", 10 * time.Second},
		{"native duration", 3 * time.Second, 3 * time.Second},
		{"int seconds", 7, 7 * time.Second},
		{"int64 seconds", int64(8), 8 * time.Second},
		{"float64 seconds", float64(9), 9 * time.Second},
		{"wrong type falls back", true, 10 * time.Second},
		{"absent falls back", nil, 10 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{}
			if tc.val != nil {
				cfg["k"] = tc.val
			}
			if got := Duration(cfg, "k", 10*time.Second); got != tc.want {
				t.Errorf("Duration(%v) = %v; want %v", tc.val, got, tc.want)
			}
		})
	}
}

// TestInt64 covers float64, which is the only shape encoding/json ever
// produces for a JSON number and therefore the shape every saved
// connection presents.
func TestInt64(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want int64
	}{
		{"int", 5, 5},
		{"int64", int64(6), 6},
		{"float64 from JSON", float64(1048576), 1048576},
		{"wrong type falls back", "8", 99},
		{"absent falls back", nil, 99},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{}
			if tc.val != nil {
				cfg["k"] = tc.val
			}
			if got := Int64(cfg, "k", 99); got != tc.want {
				t.Errorf("Int64(%v) = %d; want %d", tc.val, got, tc.want)
			}
		})
	}
}

// TestStringMap covers both map shapes and the nil-on-empty contract
// callers rely on to test the result with len() alone.
func TestStringMap(t *testing.T) {
	t.Run("map[string]string", func(t *testing.T) {
		got := StringMap(map[string]any{"k": map[string]string{"A": "1"}}, "k")
		if len(got) != 1 || got["A"] != "1" {
			t.Errorf("StringMap = %#v; want {A:1}", got)
		}
	})
	t.Run("map[string]any skips non-strings", func(t *testing.T) {
		got := StringMap(map[string]any{"k": map[string]any{"A": "1", "B": 2}}, "k")
		if len(got) != 1 || got["A"] != "1" {
			t.Errorf("StringMap = %#v; want only the string entry", got)
		}
	})
	t.Run("all non-string yields nil", func(t *testing.T) {
		if got := StringMap(map[string]any{"k": map[string]any{"B": 2}}, "k"); got != nil {
			t.Errorf("StringMap = %#v; want nil", got)
		}
	})
	for _, tc := range []struct {
		name string
		val  any
	}{
		{"empty map[string]string", map[string]string{}},
		{"empty map[string]any", map[string]any{}},
		{"wrong type", "not a map"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := StringMap(map[string]any{"k": tc.val}, "k"); got != nil {
				t.Errorf("StringMap = %#v; want nil", got)
			}
		})
	}
	t.Run("absent", func(t *testing.T) {
		if got := StringMap(map[string]any{}, "k"); got != nil {
			t.Errorf("StringMap = %#v; want nil", got)
		}
	})
}

// TestStringMap_CopiesTheSource proves the reader hands back a copy: a
// connection's stored map must not be mutable through the value a
// toolkit reads out of it.
func TestStringMap_CopiesTheSource(t *testing.T) {
	src := map[string]string{"A": "1"}
	got := StringMap(map[string]any{"k": src}, "k")
	got["A"] = "mutated"
	if src["A"] != "1" {
		t.Errorf("source map mutated through the returned copy: %#v", src)
	}
}
