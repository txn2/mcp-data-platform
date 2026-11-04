package starlarkconv

import (
	"strings"
	"testing"

	"go.starlark.net/starlark"
)

func TestStateObject(t *testing.T) {
	got, err := StateObject(nil, 100)
	if err != nil || len(got) != 0 {
		t.Fatalf("a nil dict is an empty state: %v %v", got, err)
	}
	d := starlark.NewDict(2)
	_ = d.SetKey(starlark.String("cursor"), starlark.MakeInt(42))
	got, err = StateObject(d, 100)
	if err != nil || got["cursor"] != int64(42) {
		t.Fatalf("state = %v, %v", got, err)
	}
	many := starlark.NewDict(30)
	for i := range 30 {
		_ = many.SetKey(starlark.MakeInt(i), starlark.None)
	}
	if _, err := StateObject(many, 100); err == nil || !strings.Contains(err.Error(), "more than the 100-byte limit") {
		t.Fatalf("a dict with more keys than the bound holds: %v", err)
	}
	keyed := starlark.NewDict(1)
	_ = keyed.SetKey(starlark.MakeInt(1), starlark.None)
	if _, err := StateObject(keyed, 100); err == nil || !strings.Contains(err.Error(), "state keys must be strings") {
		t.Fatalf("a non-string key: %v", err)
	}
	bad := starlark.NewDict(1)
	_ = bad.SetKey(starlark.String("f"), starlark.NewBuiltin("f", nil))
	if _, err := StateObject(bad, 100); err == nil || !strings.Contains(err.Error(), `state key "f"`) {
		t.Fatalf("a value that cannot be JSON: %v", err)
	}
}
