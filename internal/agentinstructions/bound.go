// Package agentinstructions owns the policy that applies to an agent's
// instruction text whichever layer it belongs to and whichever writer produced
// it: the byte bound and size advisory on the deployment's customized layer
// (server.agent_instructions), the store that layer is read and written
// through, and the index-entry form both layers point at a knowledge page with.
//
// The customized layer is the deployment's own half of what a session reads,
// composed beneath the platform's built-in baseline by
// pkg/platform/instructions.ComposeForCaller and carried in the first response
// of every session. Two writers produce it -- the apply_knowledge
// agent_instructions sink and PUT /admin/config/entries/server.agent_instructions
// -- so the policy lives here rather than in either of them.
package agentinstructions

import (
	"fmt"
	"strings"
)

// Byte bounds on the customized instruction layer -- the deployment's own
// server.agent_instructions, the second of the two layers ComposeForCaller
// joins. The layer is composed into the first response of every session on the
// deployment, so its size is paid for by every caller before any work happens,
// and nothing else bounds it: the column is unbounded TEXT and neither the
// config store nor its REST writer measured a value before #1607.
//
// The bound belongs to the layer rather than to whichever writer produced it,
// so both writers enforce it: the apply_knowledge agent_instructions sink and
// PUT /api/v1/admin/config/entries/server.agent_instructions.
const (
	// MaxCustomizedBytes refuses a write past roughly eight thousand tokens of
	// instruction text. It is a ceiling on runaway growth, not a target: a
	// deployment writing a document this size is carrying a knowledge base in a
	// field composed into every session.
	MaxCustomizedBytes = 32 << 10
	// AdviseCustomizedBytes is where a write still succeeds but says the layer
	// is getting long. It sits at roughly three thousand tokens, above what a
	// short set of hard operating rules needs, so the advisory arrives while
	// compaction is still cheap.
	AdviseCustomizedBytes = 12 << 10
)

// KnowledgePageAlternative is what an over-limit write is told to do instead:
// the sentence is here so the refusal, the advisory, and the editor's banner
// name one remedy rather than three phrasings of it.
const KnowledgePageAlternative = "move the longer guidance to a knowledge page and index it " +
	"from the instructions as mcp:knowledge_page:<slug>"

// OversizeError refuses a customized-layer write that would push the layer past
// MaxCustomizedBytes. It names the size, the limit, the overage, and the home
// the content belongs in instead, so a caller can act on the refusal without
// reading documentation.
type OversizeError struct {
	// Size is the byte length of the value that was refused.
	Size int
	// Limit is the byte limit it exceeded.
	Limit int
}

// Over is how many bytes over the limit the refused value was.
func (e *OversizeError) Over() int { return e.Size - e.Limit }

// Error implements the error interface.
func (e *OversizeError) Error() string {
	return fmt.Sprintf("agent instructions would be %d bytes, %d over the %d-byte limit; "+
		"this layer is composed into every session's first response, so %s",
		e.Size, e.Over(), e.Limit, KnowledgePageAlternative)
}

// CheckCustomizedSize returns an *OversizeError when value exceeds
// MaxCustomizedBytes, and nil otherwise. Both writers of the customized layer
// call it before the write, so an over-limit value is refused rather than
// stored and paid for by every later session.
func CheckCustomizedSize(value string) error {
	if len(value) <= MaxCustomizedBytes {
		return nil
	}
	return &OversizeError{Size: len(value), Limit: MaxCustomizedBytes}
}

// CustomizedNotice returns the soft advisory for a customized layer that has
// grown past AdviseCustomizedBytes, or "" when it has not. The write it
// describes has already succeeded: this is the signal that the layer is
// drifting from a set of rules toward a document, in time to compact it rather
// than after a refusal.
func CustomizedNotice(value string) string {
	if len(value) <= AdviseCustomizedBytes {
		return ""
	}
	return fmt.Sprintf("The customized agent instructions are now %d bytes, above the "+
		"%d-byte advisory (the limit is %d). Every session pays for this layer in its first "+
		"response. Keep it to the rules a session must know before it does anything and %s.",
		len(value), AdviseCustomizedBytes, MaxCustomizedBytes, KnowledgePageAlternative)
}

// KnowledgePageRef is the reference form `fetch` resolves a knowledge page by.
// A built-in page's row id is generated per deployment at reconcile time, so a
// slug is the only handle written guidance can name (#1476).
func KnowledgePageRef(slug string) string { return "`mcp:knowledge_page:" + slug + "`" }

// IndexEntry renders one line of a page index in an agent's instructions: the
// page's reference followed by what reading it answers.
//
// Both instruction layers render this line -- the platform baseline's index of
// its shipped guidance (pkg/platform/instructions.pageIndex) and the customized
// layer's entry left behind by a promotion too long to stay a rule (#1607). One
// renderer serves both, so a reader is never handed two shapes for one thing.
func IndexEntry(slug, about string) string {
	line := "- " + KnowledgePageRef(slug)
	if about = strings.TrimSpace(about); about != "" {
		line += " -- " + strings.TrimSuffix(about, ".") + "."
	}
	return line
}
