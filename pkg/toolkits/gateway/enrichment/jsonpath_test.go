package enrichment

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolveJSONPath_RootAndFields(t *testing.T) {
	root := map[string]any{
		"foo": "hello",
		"bar": map[string]any{"baz": 42},
		"arr": []any{
			map[string]any{"name": "first"},
			map[string]any{"name": "second"},
		},
	}
	cases := []struct {
		expr string
		want any
	}{
		{"$", root},
		{"$.foo", "hello"},
		{"$.bar.baz", 42},
		{"$.arr[0].name", "first"},
		{"$.arr[1].name", "second"},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			got, err := resolveJSONPath(tc.expr, root)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveJSONPath_Errors(t *testing.T) {
	root := map[string]any{
		"foo": "hello",
		"arr": []any{1, 2},
	}
	cases := []struct {
		name string
		expr string
		want string // substring of error
	}{
		{"empty", "", "empty"},
		{"no dollar", "foo", "must start with"},
		{"trailing dot", "$.", "trailing"},
		{"missing bracket", "$.foo[0", "missing"},
		{"non-numeric index", "$.foo[abc]", "numeric index"},
		{"key on non-map", "$.foo.bar", "non-object"},
		{"missing key", "$.missing", "not found"},
		{"index on non-array", "$.foo[0]", "non-array"},
		{"out of range", "$.arr[99]", "out of range"},
		{"unexpected char", "$x", "unexpected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveJSONPath(tc.expr, root)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestResolveJSONPath_TrailingKeyWithoutDot(t *testing.T) {
	// "$.a" tokenizes to one key "a" with no following separator.
	root := map[string]any{"a": "ok"}
	got, err := resolveJSONPath("$.a", root)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "ok" {
		t.Errorf("got %v", got)
	}
}

func TestResolveJSONPath_NegativeIndex(t *testing.T) {
	root := map[string]any{"arr": []any{1, 2, 3}}
	_, err := resolveJSONPath("$.arr[-1]", root)
	if err == nil {
		t.Fatal("negative index should error")
	}
}
