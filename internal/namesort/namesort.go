// Package namesort is the order the platform lists named things in for a
// person to pick from (#2389): case-insensitive, with runs of digits compared
// as numbers, so conn-2 precedes conn-10 and Alpha sits beside alpha. The
// portal sorts every option list the same way (ui/src/lib/sortByName.ts); a
// route that sorts here hands an API caller the order a person sees.
package namesort

import (
	"cmp"
	"strings"
	"unicode/utf8"
)

// Compare orders a and b case-insensitively, comparing runs of ASCII digits
// by their value. Names equal under that order fall back to a byte compare,
// so the order is total.
func Compare(a, b string) int {
	if c := natural(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

func natural(a, b string) int {
	for a != "" && b != "" {
		if isDigit(a[0]) && isDigit(b[0]) {
			da, ra := digits(a)
			db, rb := digits(b)
			if c := compareNumbers(da, db); c != 0 {
				return c
			}
			a, b = ra, rb
			continue
		}
		ca, sa := utf8.DecodeRuneInString(a)
		cb, sb := utf8.DecodeRuneInString(b)
		if ca != cb {
			return cmp.Compare(ca, cb)
		}
		a, b = a[sa:], b[sb:]
	}
	return compareRemainders(a, b)
}

// compareRemainders orders what is left once one name has run out: the
// shorter sorts first.
func compareRemainders(a, b string) int {
	switch {
	case a == "" && b == "":
		return 0
	case a == "":
		return -1
	default:
		return 1
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// digits splits s into its leading run of digits and the rest.
func digits(s string) (run, rest string) {
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return s[:i], s[i:]
}

// compareNumbers compares two runs of digits by value: leading zeros are
// ignored, a longer run is larger, and equal values with more leading zeros
// sort after.
func compareNumbers(a, b string) int {
	ta, tb := strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(ta) != len(tb) {
		if len(ta) < len(tb) {
			return -1
		}
		return 1
	}
	if c := strings.Compare(ta, tb); c != 0 {
		return c
	}
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return 0
}
