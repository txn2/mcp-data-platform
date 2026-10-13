package textpatch

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Elision bounds for a long changed line (#2115).
const (
	// ElideWidth is the length past which a changed line is shown as the
	// span that changed, not the whole line.
	ElideWidth = 400
	// elideContext is how much unchanged text is kept on each side of the
	// span that changed.
	elideContext = 80
	// elideSpan bounds the changed span itself: a line rewritten end to end
	// shows its start and is marked cut.
	elideSpan = 240
)

// elision marks text a shortened line leaves out.
const elision = "…"

// ElideDiff shortens the long lines of a unified diff, for a reader who needs
// to see what changed rather than every character of the line it changed in:
// one word changed inside a 15 KB embedded string otherwise prints the whole
// string twice. A removed line followed by an added one is shown as the span
// where they differ with elideContext characters on each side; a long line
// with no partner, and a long unchanged context line, shows its start. Each
// shortened line ends with the length of the line it stands for. Lines no
// longer than width, and the hunk and file headers, are left as they are. A
// width of zero or less is ElideWidth.
func ElideDiff(diff string, width int) string {
	if width <= 0 {
		width = ElideWidth
	}
	if !hasLongLine(diff, width) {
		return diff
	}
	lines := strings.Split(diff, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		if !isChange(lines[i], byte(tagDelete)) {
			out = append(out, elideLone(lines[i], width))
			i++
			continue
		}
		dels := run(lines, i, byte(tagDelete))
		adds := run(lines, i+len(dels), byte(tagInsert))
		shortAdds := make([]string, len(adds))
		for k := range dels {
			if k < len(adds) {
				old, nw := ElidePair(dels[k][1:], adds[k][1:], width)
				out = append(out, "-"+old)
				shortAdds[k] = "+" + nw
				continue
			}
			out = append(out, elideLone(dels[k], width))
		}
		for k := len(dels); k < len(adds); k++ {
			shortAdds[k] = elideLone(adds[k], width)
		}
		out = append(out, shortAdds...)
		i += len(dels) + len(adds)
	}
	return strings.Join(out, "\n")
}

// ElidePair shortens two versions of one long text to the span where they
// differ, with context on each side, so a changed word reads as a changed
// word. Texts no longer than width are returned as they are.
func ElidePair(oldText, newText string, width int) (shortOld, shortNew string) {
	if width <= 0 {
		width = ElideWidth
	}
	if utf8.RuneCountInString(oldText) <= width && utf8.RuneCountInString(newText) <= width {
		return oldText, newText
	}
	a, b := []rune(oldText), []rune(newText)
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	return window(a, prefix, len(a)-suffix), window(b, prefix, len(b)-suffix)
}

// window renders the changed span [from, to) of a line with context on each
// side and the line's length.
func window(r []rune, from, to int) string {
	start := max(from-elideContext, 0)
	end := min(to+elideContext, len(r))
	if start == 0 && end == len(r) && to-from <= elideSpan {
		return string(r)
	}
	var sb strings.Builder
	if start > 0 {
		_, _ = sb.WriteString(elision)
	}
	if to-from > elideSpan {
		_, _ = sb.WriteString(string(r[start : from+elideSpan]))
		_, _ = sb.WriteString(elision)
	} else {
		_, _ = sb.WriteString(string(r[start:end]))
		if end < len(r) {
			_, _ = sb.WriteString(elision)
		}
	}
	_, _ = fmt.Fprintf(&sb, " [%d characters]", len(r))
	return sb.String()
}

// elideLone shortens a long added or removed line with no partner to its
// start. Context and header lines are returned as they are.
func elideLone(line string, width int) string {
	// A removed, an added and an unchanged context line are shortened alike:
	// a long constant beside the line that changed is context, not the change.
	if !isChange(line, byte(tagDelete)) && !isChange(line, byte(tagInsert)) && !strings.HasPrefix(line, " ") {
		return line
	}
	body := []rune(line[1:])
	if len(body) <= width {
		return line
	}
	return line[:1] + string(body[:elideSpan]) + elision + fmt.Sprintf(" [%d characters]", len(body))
}

// isChange reports whether a diff line is a removed (-) or added (+) line
// rather than a file header.
func isChange(line string, tag byte) bool {
	if line == "" || line[0] != tag {
		return false
	}
	header := string([]byte{tag, tag, tag, ' '})
	return !strings.HasPrefix(line, header)
}

// run collects the consecutive lines from i tagged tag.
func run(lines []string, i int, tag byte) []string {
	j := i
	for j < len(lines) && isChange(lines[j], tag) {
		j++
	}
	return lines[i:j]
}

// hasLongLine reports whether any line of s is longer than width runes.
func hasLongLine(s string, width int) bool {
	for line := range strings.SplitSeq(s, "\n") {
		if len(line) > width && utf8.RuneCountInString(line) > width {
			return true
		}
	}
	return false
}
