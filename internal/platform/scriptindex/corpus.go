package scriptindex

import (
	"strings"

	"github.com/txn2/mcp-data-platform/internal/textchunk"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Corpus is everything a script is indexed on: its card and its source.
// Its hash decides whether a saved version must be embedded again.
func Corpus(s *script.Script) string {
	if strings.TrimSpace(s.Source) == "" {
		return script.IndexText(s)
	}
	return script.IndexText(s) + "\n\n" + s.Source
}

// Chunks splits a script into the units its vectors are built from, each
// within maxBytes so no part of it is trimmed before it is embedded (#2027):
// the card first, then the source, each source chunk headed by the script's
// title so it is placed in the same space a query about the script is. The
// source is cut where a definition, a load or a comment block starts at the
// left margin, so a comment stays with the code it explains. A maxBytes below
// textchunk.MinViableChunkBytes disables splitting.
func Chunks(s *script.Script, maxBytes int) []string {
	card := script.IndexText(s)
	if maxBytes < textchunk.MinViableChunkBytes {
		return []string{Corpus(s)}
	}
	chunks := textchunk.SplitText(card, maxBytes, func(t string) []string { return []string{t} })
	if strings.TrimSpace(s.Source) == "" {
		return chunks
	}
	head := script.Title(s) + "\n"
	budget := maxBytes - len(head)
	if budget < maxBytes/2 {
		head, budget = "", maxBytes
	}
	for _, part := range textchunk.SplitText(s.Source, budget, starlarkBlocks) {
		chunks = append(chunks, head+part)
	}
	return chunks
}

// starlarkBlocks cuts source before each line at the left margin that starts
// a definition or a load, and before a left-margin comment that follows a
// blank line, which is where a comment introduces the code below it; a
// definition directly under such a comment stays in the comment's block. The
// blocks concatenate back to the source.
func starlarkBlocks(source string) []string {
	var (
		blocks []string
		start  int
		offset int
		blank  bool
		// heading is true while the block so far is a comment introducing
		// what follows it.
		heading bool
	)
	for line := range strings.SplitSeq(source, "\n") {
		comment := strings.HasPrefix(line, "#")
		opens := opensBlock(line, blank, heading)
		if opens && offset > start {
			blocks = append(blocks, source[start:offset])
			start = offset
		}
		heading = comment && (opens || heading)
		blank = strings.TrimSpace(line) == ""
		offset += len(line) + 1
	}
	if start < len(source) {
		blocks = append(blocks, source[start:])
	}
	return blocks
}

// opensBlock reports whether a line starts a block: a definition or a load at
// the left margin, unless it sits directly under a comment introducing it, or
// a left-margin comment after a blank line.
func opensBlock(line string, blank, heading bool) bool {
	code := strings.HasPrefix(line, "def ") || strings.HasPrefix(line, "load(")
	return (code && !heading) || (blank && strings.HasPrefix(line, "#"))
}
