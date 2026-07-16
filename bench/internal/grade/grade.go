// Package grade implements the deterministic graders: numeric tolerance match
// and entity alias match, plus the FINAL ANSWER extraction convention shared
// with the system-prompt scaffold.
//
// Both graders score only the first line after the FINAL ANSWER marker (the
// convention the system prompt mandates), so trailing commentary cannot flip a
// grade. They are deliberately simple and documented rather than clever;
// judgment-call scoring is the phase-2 LLM judge's job, and every attempt's
// transcript is persisted for manual audit.
package grade

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// finalMarker matches the answer convention the system prompt mandates. The
// last occurrence wins so a model that restates the marker while reasoning is
// graded on its actual final line.
var finalMarker = regexp.MustCompile(`(?i)FINAL ANSWER:\s*`)

// numberPattern matches a decimal number, tolerating $ prefixes and thousands
// separators in the surrounding text.
var numberPattern = regexp.MustCompile(`-?\d[\d,]*(?:\.\d+)?`)

// ExtractFinal returns the text after the last "FINAL ANSWER:" marker, or the
// whole trimmed text when the marker is absent.
func ExtractFinal(text string) string {
	locs := finalMarker.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(text[locs[len(locs)-1][1]:])
}

// firstLine isolates the answer line the graders score.
func firstLine(final string) string {
	line, _, _ := strings.Cut(final, "\n")
	return strings.TrimSpace(line)
}

// Numeric grades a final answer against an expected value. Candidates come
// from the first line only; among them, numbers carrying a decimal point or
// thousands separator are preferred over bare integers (the tasks demand
// cent-rounded USD, while bare integers in a verbose answer are usually
// restated years or counts). The first such candidate is graded; with no
// decimal-bearing candidate, the first number is. ok is false when the line
// carries no number at all.
func Numeric(final string, expected, absTolerance float64) (got float64, ok, correct bool) {
	matches := numberPattern.FindAllString(firstLine(final), -1)
	if len(matches) == 0 {
		return 0, false, false
	}
	candidate := matches[0]
	for _, m := range matches {
		if strings.ContainsAny(m, ".,") {
			candidate = m
			break
		}
	}
	got, err := strconv.ParseFloat(strings.ReplaceAll(candidate, ",", ""), 64)
	if err != nil {
		return 0, false, false
	}
	return got, true, math.Abs(got-expected) <= absTolerance
}

// Entity grades a final answer's first line against accepted aliases,
// case-insensitively. An answer is correct only when a correct alias appears
// AND no wrong alias does: the wrong-alias list enumerates the task's known
// trap answers (the deprecated table, the gross-revenue region), so a verbose
// answer that names the trap while mentioning the truth is not credited.
// Aliases match on word boundaries, not bare substrings: "East" must not veto
// "at least", and "West" must not match "southwest". Letters, digits, and
// underscores are word characters; dots are boundaries, so a schema-qualified
// alias still matches inside a longer qualified name. The boundary rule also
// narrows the veto: an answer naming a suffixed variant of a trap identifier
// ("legacy_orders_v9") is not vetoed by the "legacy_orders" wrong alias — the
// wrong-alias list enumerates the warehouse's ACTUAL trap answers, and a
// nonexistent variant neither names the trap nor matches a correct alias, so
// it cannot be credited either.
func Entity(final string, aliases, wrongAliases []string) (matched string, correct bool) {
	line := strings.ToLower(firstLine(final))
	for _, w := range wrongAliases {
		if w != "" && containsWord(line, strings.ToLower(w)) {
			return "", false
		}
	}
	for _, a := range aliases {
		if a != "" && containsWord(line, strings.ToLower(a)) {
			return a, true
		}
	}
	return "", false
}

// containsWord reports whether needle occurs in line with no word character
// (letter, digit, underscore) immediately adjacent on either side.
func containsWord(line, needle string) bool {
	if needle == "" {
		return false
	}
	for start := 0; ; start++ {
		i := strings.Index(line[start:], needle)
		if i < 0 {
			return false
		}
		start += i
		if boundaryBefore(line, start) && boundaryAfter(line, start+len(needle)) {
			return true
		}
	}
}

// boundaryBefore reports whether position i starts at a word boundary.
func boundaryBefore(s string, i int) bool {
	if i == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return !isWordRune(r)
}

// boundaryAfter reports whether position i ends at a word boundary.
func boundaryAfter(s string, i int) bool {
	if i >= len(s) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return !isWordRune(r)
}

// isWordRune reports whether r is a word character for alias matching.
func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
