package assettags

import (
	"sort"
	"strings"
	"unicode"
)

// DefaultPerTag is how many existing tags are suggested for one given tag.
const DefaultPerTag = 3

// Variant is an existing tag close to one a caller gave: a case or spelling
// variant of it, never the same string.
type Variant struct {
	// Tag is the existing tag, in its most used spelling.
	Tag string `json:"tag" example:"finance"`
	// Count is how many assets the caller can open carry it.
	Count int `json:"asset_count" example:"14"`
	// CloseTo is the tag the caller gave that it is close to.
	CloseTo string `json:"close_to" example:"Finance"`
}

// Variants returns, for each given tag, the existing tags that are a variant
// of it, most carried first and at most perTag each. An existing tag spelled
// exactly as given is the tag itself and is never offered.
func Variants(given []string, existing []Tag, perTag int) []Variant {
	if perTag <= 0 {
		perTag = DefaultPerTag
	}
	out := []Variant{}
	for _, g := range given {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		var found []Variant
		for _, e := range existing {
			if e.Tag != g && Close(g, e.Tag) {
				found = append(found, Variant{Tag: e.Tag, Count: e.Count, CloseTo: g})
			}
		}
		sort.SliceStable(found, func(i, j int) bool { return found[i].Count > found[j].Count })
		out = append(out, found[:min(len(found), perTag)]...)
	}
	return out
}

// The lengths past which a tag may be one or two letters from another and
// still be read as the same tag: two edits at eight letters or more, one at
// four or more, none below.
const (
	longTag  = 8
	shortTag = 4
)

// Close reports whether two tags are variants of each other: the same without
// case, the same once spaces and punctuation are dropped
// (finance-reports, "finance reports", FinanceReports), or one or two letters
// apart (report, reports; finanse, finance). The edit distance allowed grows
// with the tag: one letter from four letters, two from eight, so short tags
// that differ by a letter (q3, q4) are kept apart.
func Close(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la == lb {
		return true
	}
	sa, sb := squash(la), squash(lb)
	if sa == "" || sb == "" {
		return false
	}
	if sa == sb {
		return true
	}
	shorter := min(len([]rune(sa)), len([]rune(sb)))
	switch d := distance(sa, sb); {
	case shorter >= longTag:
		return d <= 2
	case shorter >= shortTag:
		return d <= 1
	}
	return false
}

// squash keeps a tag's letters and digits.
func squash(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}

// maxDistanceRunes bounds the strings distance compares.
const maxDistanceRunes = 256

// distance is the Levenshtein distance between two strings, by rune.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	// A tag is at most portaldomain's 100 characters; bounding the operand
	// keeps the row size from overflowing on any input.
	if len(rb) > maxDistanceRunes {
		rb = rb[:maxDistanceRunes]
	}
	if len(ra) > maxDistanceRunes {
		ra = ra[:maxDistanceRunes]
	}
	width := len(rb) + 1
	prev := make([]int, width)
	cur := make([]int, width)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
