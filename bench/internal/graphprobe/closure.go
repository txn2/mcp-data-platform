package graphprobe

import (
	"regexp"
	"strings"
)

// Closure grading (#1250). The study's second mechanism is completeness
// closure: a graph closure from an entry node is enumerable and terminating,
// so an agent with edges can know when it is done, while a ranked result
// list certifies nothing about coverage. The probe's grounded-coverage
// instrument measures what a document contains; this file adds what the
// document CLAIMS — so overclaim (claiming completeness the coverage numbers
// contradict) is measurable per arm.

// PromptCompleteness is the frozen elicitation suffix study runs append to
// every cell prompt. It gives the episode an explicit, gradable channel to
// declare gaps. The channel biases against overclaim — declaring a gap costs
// one line — so a measured overclaim rate is a lower bound, which is the
// conservative direction for the claim the study reads on it. The wording
// names no tool and no discovery route, per the non-disclosure rule.
const PromptCompleteness = `End the document with a section titled "Open items": list each thing the document needs that you could not determine, or write "None" if nothing is outstanding.`

// openItemsHeading matches the elicited section's heading line: optional
// markdown heading or emphasis markers around "open items", with an optional
// trailing colon.
var openItemsHeading = regexp.MustCompile(`(?i)^[#*\s]*open items\s*:?[*\s]*$`)

// headingLine matches any markdown heading, which ends the section.
var headingLine = regexp.MustCompile(`^#{1,6}\s`)

// noneItem matches an item list whose only content declares nothing
// outstanding.
var noneItem = regexp.MustCompile(`(?i)^[-*\s]*none[.!]?\s*$`)

// CompletenessClaim is what one final document declared about its own
// coverage.
type CompletenessClaim struct {
	// Stated reports whether the document carried the elicited section at
	// all. A missing section is its own reading: the episode was asked for a
	// coverage statement and did not produce one.
	Stated bool `json:"stated"`
	// Complete reports a stated claim of nothing outstanding.
	Complete bool `json:"complete"`
	// Items are the declared gaps, one per list line, empty for a complete
	// claim.
	Items []string `json:"items,omitempty"`
}

// ReadCompletenessClaim parses the final document's "Open items" section.
// The last matching heading wins, because the deliverable's own closing
// section is the claim, not a draft the document quoted earlier.
func ReadCompletenessClaim(doc string) CompletenessClaim {
	lines := strings.Split(doc, "\n")
	start := -1
	for i, line := range lines {
		if openItemsHeading.MatchString(strings.TrimSpace(line)) {
			start = i
		}
	}
	if start < 0 {
		return CompletenessClaim{}
	}
	return readClaimItems(lines[start+1:])
}

// readClaimItems folds the section's lines into a claim.
func readClaimItems(lines []string) CompletenessClaim {
	claim := CompletenessClaim{Stated: true}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case headingLine.MatchString(trimmed):
			return claim.settle()
		case noneItem.MatchString(trimmed):
			claim.Complete = true
		default:
			claim.Items = append(claim.Items, strings.TrimLeft(trimmed, "-* \t"))
		}
	}
	return claim.settle()
}

// settle reconciles the claim: any declared item outweighs a "None" line,
// and a section with no content at all claims nothing.
func (c CompletenessClaim) settle() CompletenessClaim {
	if len(c.Items) > 0 {
		c.Complete = false
	}
	return c
}

// Overclaim reports whether a document claimed completeness its own graded
// coverage contradicts: the claim says nothing is outstanding while at least
// one constraint's signature is absent from the document. Read against
// covered rather than grounded coverage, because the claim is about what the
// document contains; a covered-but-unread constraint is confabulation, which
// the grounded numbers already charge separately.
func Overclaim(cov Coverage, claim CompletenessClaim) bool {
	covered := cov.EntryCovered + cov.OffEntryCovered
	total := cov.EntryTotal + cov.OffEntryTotal
	return claim.Complete && covered < total
}
