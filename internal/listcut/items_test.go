package listcut

import (
	"encoding/json"
	"reflect"
	"testing"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestFindItems(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		path  []string
		total int
		ok    bool
	}{
		{"top-level array", `[1,2,3]`, nil, 3, true},
		{"object with one array", `{"data":[1,2],"total":2,"meta":{"page":1}}`, []string{"data"}, 2, true},
		{"graphql connection", `{"users":{"edges":[{"n":1},{"n":2},{"n":3}],"pageInfo":{"hasNextPage":true}}}`, []string{"users", "edges"}, 3, true},
		{"two arrays", `{"data":[1],"included":[2]}`, nil, 0, false},
		{"scalar", `"a long string"`, nil, 0, false},
		{"object with no array", `{"a":{"b":{"c":1}}}`, nil, 0, false},
		{"list past the depth bound", `{"a":{"b":{"c":{"d":{"e":[1]}}}}}`, nil, 0, false},
		{"two objects", `{"a":{"x":[1]},"b":{"y":[2]}}`, nil, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			it, ok := FindItems(decode(t, tc.body))
			if ok != tc.ok {
				t.Fatalf("FindItems ok = %v; want %v", ok, tc.ok)
			}
			if ok && (!reflect.DeepEqual(it.Path, tc.path) || it.Total != tc.total) {
				t.Errorf("FindItems = %+v; want path %v total %d", it, tc.path, tc.total)
			}
		})
	}
}

// Keep cuts the list and nothing else, and leaves the caller's value whole
// so a second attempt starts from all of it.
func TestKeep(t *testing.T) {
	body := decode(t, `{"users":{"edges":[1,2,3,4],"pageInfo":{"end":"c4"}},"x":1}`)
	cut := Keep(body, []string{"users", "edges"}, 2)
	got, _ := json.Marshal(cut)
	if want := `{"users":{"edges":[1,2],"pageInfo":{"end":"c4"}},"x":1}`; string(got) != want {
		t.Errorf("Keep = %s; want %s", got, want)
	}
	whole, _ := json.Marshal(body)
	if want := `{"users":{"edges":[1,2,3,4],"pageInfo":{"end":"c4"}},"x":1}`; string(whole) != want {
		t.Errorf("the original changed to %s", whole)
	}
	if got, _ := Keep(decode(t, `[1,2,3]`), nil, 5).([]any); len(got) != 3 {
		t.Errorf("Keep past the end = %v; want the whole list", got)
	}
	if got := Keep("s", nil, 1); got != "s" {
		t.Errorf("Keep on a scalar = %v; want it unchanged", got)
	}
	if got := Keep("s", []string{"a"}, 1); got != "s" {
		t.Errorf("Keep on a non-object path = %v; want it unchanged", got)
	}
}

func TestFitItems(t *testing.T) {
	n, ok := FitItems(100, func(n int) bool { return n <= 37 })
	if !ok || n != 37 {
		t.Errorf("FitItems = %d, %v; want 37", n, ok)
	}
	if n, ok := FitItems(100, func(int) bool { return true }); !ok || n != 99 {
		t.Errorf("FitItems with everything fitting = %d, %v; want a cut of 99, below the total", n, ok)
	}
	if _, ok := FitItems(100, func(int) bool { return false }); ok {
		t.Error("FitItems with not even one item fitting reported a cut")
	}
	if _, ok := FitItems(1, func(int) bool { return true }); ok {
		t.Error("FitItems on a one-item list reported a cut")
	}
}
