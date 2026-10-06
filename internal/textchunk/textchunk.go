// Package textchunk cuts a text longer than an embedding provider's input
// into pieces that each fit, so the whole text reaches the model across
// several calls rather than being trimmed (#1242, #2027). The caller says
// where its kind of text breaks naturally (a markdown heading, a Starlark
// definition); the rest is blank lines, then words, then rune boundaries.
package textchunk

import (
	"strings"
	"unicode/utf8"
)

// MinViableChunkBytes is the smallest input budget worth splitting to. Below
// it, splitting stops being meaningful (a chunk would hold a few words) and a
// hard cut can no longer be guaranteed to make progress: the budget must
// exceed the widest UTF-8 rune, or a rune-boundary cut could produce an empty
// piece and the split would not terminate. A caller handed a budget this small
// embeds its text whole and the provider's own cap applies.
const MinViableChunkBytes = 64

// SplitText cuts text into pieces of at most budget bytes each, so a text
// longer than the provider's input reaches the model whole, across several
// calls, rather than being trimmed (#1242, #2027). sections cuts the text at
// the boundaries its kind has (a markdown heading, a Starlark definition);
// each section that does not fit is cut at blank lines, then at a word on a
// rune boundary, and consecutive pieces are packed back together while they
// fit. sections must be lossless: the pieces it returns concatenate back to
// the text. Whitespace-only pieces are dropped, since they carry nothing a
// vector could match.
func SplitText(text string, budget int, sections func(string) []string) []string {
	parts := sections(text)
	units := make([]string, 0, len(parts))
	for _, part := range parts {
		units = append(units, splitOversized(part, budget)...)
	}
	return packUnits(units, budget)
}

// splitOversized returns s unchanged when it fits the budget, else cuts it at
// paragraph boundaries and, for a paragraph that still does not fit, at a hard
// rune boundary. Every returned piece is at most budget bytes.
func splitOversized(s string, budget int) []string {
	if len(s) <= budget {
		return []string{s}
	}
	var out []string
	for _, para := range splitParagraphs(s) {
		if len(para) <= budget {
			out = append(out, para)
			continue
		}
		out = append(out, hardSplit(para, budget)...)
	}
	return out
}

// splitParagraphs cuts s after each blank line, keeping the separator with the
// paragraph that precedes it so the pieces concatenate back to s.
func splitParagraphs(s string) []string {
	const sep = "\n\n"
	var out []string
	for {
		i := strings.Index(s, sep)
		if i < 0 {
			if s != "" {
				out = append(out, s)
			}
			return out
		}
		cut := i + len(sep)
		out = append(out, s[:cut])
		s = s[cut:]
	}
}

// hardSplit cuts s into budget-sized pieces, backing each cut off to the last
// newline or space within the budget (so a word is not severed) and then to a
// UTF-8 rune boundary (so a multi-byte rune is never split).
func hardSplit(s string, budget int) []string {
	var out []string
	for len(s) > budget {
		cut := TruncateOnRune(s, budget)
		if i := strings.LastIndexAny(cut, " \n"); i > 0 {
			cut = cut[:i+1]
		}
		out = append(out, cut)
		s = s[len(cut):]
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// packUnits concatenates consecutive units while the result stays within
// budget, so a text of many small sections does not pay one provider call per
// section. Whitespace-only units are dropped.
func packUnits(units []string, budget int) []string {
	var (
		out     []string
		current strings.Builder
	)
	flush := func() {
		if strings.TrimSpace(current.String()) != "" {
			out = append(out, current.String())
		}
		current.Reset()
	}
	for _, u := range units {
		if strings.TrimSpace(u) == "" {
			continue
		}
		if current.Len() > 0 && current.Len()+len(u) > budget {
			flush()
		}
		_, _ = current.WriteString(u)
	}
	flush()
	return out
}

// TruncateOnRune returns the longest prefix of s within maxBytes that ends on
// a UTF-8 rune boundary. A non-positive maxBytes returns s unchanged.
func TruncateOnRune(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
