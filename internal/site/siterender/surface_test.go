package siterender

import (
	"runtime/debug"
	"slices"
	"testing"

	"github.com/osteele/liquid/filters"
)

// linearInInput are the filters whose result is at most a fixed multiple of what
// they were given, so that metering assignment and output bounds them. Filters
// are recorded here rather than wrapped because the meter need only see the
// places a value can accumulate.
var linearInInput = []string{
	"abs", "append", "at_least", "at_most", "capitalize", "ceil", "compact", "concat", "default", "divided_by", "downcase",
	"escape", "escape_once", "first", "floor", "handle", "handleize", "inspect", "json", "last", "lstrip", "map", "minus", "modulo",
	"newline_to_br", "pluralize", "plus", "prepend", "remove", "remove_first", "remove_last", "replace_first", "replace_last",
	"reverse", "round", "rstrip", "size", "slice", "sort", "sort_natural", "split", "strip", "strip_html", "strip_newlines",
	"sum", "times", "truncate", "truncatewords", "type", "uniq", "upcase", "url_decode", "url_encode", "where",
}

// replacedFilters are the ones this package supplies its own version of.
var replacedFilters = []string{"date", "join", "replace"}

type filterNames []string

func (f *filterNames) AddFilter(name string, _ any) { *f = append(*f, name) }

func TestEveryFilterIsAccountedFor(t *testing.T) {
	var names filterNames
	filters.AddStandardFilters(&names)
	for _, name := range names {
		if !slices.Contains(linearInInput, name) && !slices.Contains(replacedFilters, name) {
			t.Errorf("filter %q is new: read what it can spend, then list it", name)
		}
	}
	for _, name := range append(slices.Clone(linearInInput), replacedFilters...) {
		if !slices.Contains(names, name) {
			t.Errorf("filter %q no longer exists", name)
		}
	}
}

func TestTheEngineIsTheReviewedVersion(t *testing.T) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Skip("no build info")
	}
	for _, dep := range info.Deps {
		if dep.Path == "github.com/osteele/liquid" && dep.Version != reviewedEngine {
			t.Fatalf("the engine is %s and was reviewed at %s", dep.Version, reviewedEngine)
		}
	}
}
