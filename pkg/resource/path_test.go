package resource

import (
	"strings"
	"testing"
)

func TestValidatePathAcceptsTheShapesAFolderChainTakes(t *testing.T) {
	tests := []string{
		"data",
		// Every value that was a legal flat category before folders existed is a
		// legal one-segment path now, which is what lets the migration rewrite
		// no rows and change no URIs.
		"samples",
		"a",
		"data/media-manager",
		"data/media-manager/shows",
		"a/b/c/d/e/f/g/h",
		strings.Repeat("a", 31),
	}
	for _, p := range tests {
		t.Run(p, func(t *testing.T) {
			if err := ValidatePath(p); err != nil {
				t.Errorf("ValidatePath(%q) = %v, want nil", p, err)
			}
		})
	}
}

// TestValidatePathNamesTheRuleItRefusesOn is the requirement itself: a person
// types one path, and a refusal that restated the whole grammar would leave them
// to work out which part of it they broke.
func TestValidatePathNamesTheRuleItRefusesOn(t *testing.T) {
	tests := []struct {
		name string
		path string
		says string
	}{
		{"empty", "", "required"},
		{"leading slash", "/data", "start or end"},
		{"trailing slash", "data/", "start or end"},
		{"empty segment", "data//shows", "empty folder name"},
		{"relative segment", "data/../etc", "relative segment"},
		{"a bare dot", "data/./shows", "relative segment"},
		{"too deep", "a/b/c/d/e/f/g/h/i", "folders deep"},
		{"too long overall", strings.TrimSuffix(strings.Repeat(strings.Repeat("a", 31)+"/", 8), "/"), "exceeds 200 characters"},
		{"an uppercase segment", "data/Shows", `"Shows" must match`},
		{"a segment starting with a digit", "data/2024", `"2024" must match`},
		{"an underscore", "data/media_manager", `"media_manager" must match`},
		{"a segment too long", strings.Repeat("a", 32), "must match"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePath(tc.path)
			if err == nil {
				t.Fatalf("ValidatePath(%q) = nil, want a refusal", tc.path)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("ValidatePath(%q) = %q, want it to name %q", tc.path, err, tc.says)
			}
		})
	}
}

// TestValidatePathRefusesDepthBeforeLength keeps the two bounds from reporting
// each other. A path that breaks both is fixed by removing folders, so naming
// the character limit would send the person shortening names that were never
// the problem.
func TestValidatePathRefusesDepthBeforeLength(t *testing.T) {
	tooDeepAndTooLong := strings.TrimSuffix(strings.Repeat(strings.Repeat("a", 31)+"/", 9), "/")
	if err := ValidatePath(tooDeepAndTooLong); err == nil ||
		!strings.Contains(err.Error(), "folders deep") {
		t.Errorf("err = %v, want the depth rule", err)
	}
}

func TestPathSegments(t *testing.T) {
	if got := PathSegments(""); got != nil {
		t.Errorf("PathSegments(\"\") = %v, want nil: the library root has no folders", got)
	}
	got := PathSegments("data/media-manager/shows")
	if len(got) != 3 || got[0] != "data" || got[2] != "shows" {
		t.Errorf("PathSegments = %v", got)
	}
}

// TestPathUnderCountsTheSeparator is the rule a folder rename rests on: without
// the separator in the comparison, renaming "data" would drag in "data-archive".
func TestPathUnderCountsTheSeparator(t *testing.T) {
	tests := []struct {
		path, prefix string
		want         bool
	}{
		{"data", "", true},
		{"data/shows", "", true},
		{"data", "data", true},
		{"data/shows", "data", true},
		{"data/media-manager/shows", "data/media-manager", true},
		{"data-archive", "data", false},
		{"database", "data", false},
		{"other/data", "data", false},
		{"data", "data/shows", false},
	}
	for _, tc := range tests {
		t.Run(tc.path+" under "+tc.prefix, func(t *testing.T) {
			if got := PathUnder(tc.path, tc.prefix); got != tc.want {
				t.Errorf("PathUnder(%q, %q) = %v, want %v", tc.path, tc.prefix, got, tc.want)
			}
		})
	}
}

func TestRepointPath(t *testing.T) {
	tests := []struct {
		name, path, from, to, want string
	}{
		{"the folder itself", "data", "data", "archive", "archive"},
		{"a file in it", "data/shows", "data", "archive", "archive/shows"},
		{"a file deeper in it", "data/a/b", "data", "archive", "archive/a/b"},
		{"nesting it", "data/shows", "data", "cold/data", "cold/data/shows"},
		{"moving it up", "a/b/x", "a/b", "a", "a/x"},
		{"a sibling is untouched", "data-archive/x", "data", "archive", "data-archive/x"},
		{"an unrelated path is untouched", "other", "data", "archive", "other"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RepointPath(tc.path, tc.from, tc.to); got != tc.want {
				t.Errorf("RepointPath(%q, %q, %q) = %q, want %q", tc.path, tc.from, tc.to, got, tc.want)
			}
		})
	}
}
