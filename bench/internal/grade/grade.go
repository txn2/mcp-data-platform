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
func Entity(final string, aliases, wrongAliases []string) (matched string, correct bool) {
	line := strings.ToLower(firstLine(final))
	for _, w := range wrongAliases {
		if w != "" && strings.Contains(line, strings.ToLower(w)) {
			return "", false
		}
	}
	for _, a := range aliases {
		if a != "" && strings.Contains(line, strings.ToLower(a)) {
			return a, true
		}
	}
	return "", false
}
