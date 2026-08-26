package assetrefapi

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// Bounds on the occurrence scan. It exists to answer one question -- "does the
// markup still say this?" -- for a person about to remove a reference, and each
// bound keeps that answer from costing more than it is worth.
const (
	// maxScanBytes is the largest stored asset the scan reads. A document
	// nobody could hand-edit is past the point where naming a line helps, and
	// the reference list is read on every open of the asset.
	maxScanBytes = 2 << 20 // 2 MiB

	// maxOccurrencesPerRef caps how many lines one URI is reported on. A URI
	// written a hundred times is answered by the first few and the fact that
	// there are more.
	maxOccurrencesPerRef = 5

	// snippetLimit is the widest line fragment reported, in bytes. Longer
	// lines are windowed around the URI rather than truncated from the left,
	// since the URI is the part the reader is looking for.
	snippetLimit = 160
)

// occurrence is one line of the asset's stored content that writes a
// reference's URI. A line naming the URI twice is one occurrence: the reader is
// being pointed at a place to edit, and the place is the line.
//
// It is the substance of the removal warning: an owner told only that "the
// content may still reference this" has to go and look, and on an HTML report
// that is a search through markup they did not write.
type occurrence struct {
	// Line is 1-indexed, counting the stored content's own lines.
	Line int `json:"line"`
	// Snippet is the fragment of that line around the URI, whitespace
	// collapsed, so the panel can show the reader what their content says.
	Snippet string `json:"snippet"`
	// Truncated is set on the last reported occurrence when the cap stopped
	// the scan, so a warning built from this list reads as "at least these"
	// rather than as the whole of them.
	Truncated bool `json:"truncated,omitempty"`
}

// scanContent reports where the asset's stored content names each reference,
// and whether the content could be read at all.
//
// It reads the STORED content, not the rewritten form a reader is served: the
// stored bytes are what an author edits and what the reference has to keep
// agreeing with.
//
// The second return is the whole reason this is not just a map. A reader who
// cannot get the content -- no blob reader, a binary asset, one too large to
// scan, or a storage fault -- gets no occurrences, and an empty map is
// indistinguishable from a document that names nothing. Those two mean
// opposite things to the person about to remove a reference, so the caller is
// told which it has: false means "we did not look", never "it is not there".
func (h *handler) scanContent(
	r *http.Request, asset *portaldomain.Asset, refs []assetrefs.Ref,
) (map[string][]occurrence, bool) {
	if h.cfg.Blobs == nil || !scannable(asset) {
		return nil, false
	}
	// Nothing to look for is a scan that ran and found nothing, which is what
	// lets an empty asset's first reference be removed without a warning.
	if len(refs) == 0 {
		return nil, true
	}
	content, _, err := h.cfg.Blobs.GetObject(r.Context(), asset.S3Bucket, asset.S3Key)
	if err != nil {
		slog.Warn("asset resource references: reading content for the occurrence scan failed",
			logKeyAssetID, logsan.SanitizeForLog(asset.ID),
			logKeyError, logsan.SanitizeForLog(err.Error()))
		return nil, false
	}
	return scanOccurrences(content, refs), true
}

// scannable reports whether an asset's stored content can usefully be scanned:
// textual, and small enough that reading it costs less than the answer is
// worth. A stored PNG has no lines and a declared URI could only appear in it
// by coincidence.
func scannable(asset *portaldomain.Asset) bool {
	return contenttype.IsTextual(asset.ContentType) &&
		asset.SizeBytes > 0 && asset.SizeBytes <= maxScanBytes
}

// scanOccurrences finds each reference's URI in the content, keyed by kind and
// target id. One pass over the lines serves every reference, since the panel
// asks about all of them at once.
func scanOccurrences(
	content []byte, refs []assetrefs.Ref,
) map[string][]occurrence {
	if len(content) == 0 {
		return nil
	}
	lines := strings.Split(string(content), "\n")
	found := make(map[string][]occurrence)
	for _, ref := range refs {
		if ref.URI == "" {
			continue
		}
		if hits := occurrencesOf(lines, ref.URI); len(hits) > 0 {
			found[refKey(ref)] = hits
		}
	}
	if len(found) == 0 {
		return nil
	}
	return found
}

// occurrencesOf lists the lines naming one URI, stopping at the cap and marking
// the last reported line when it did.
func occurrencesOf(lines []string, uri string) []occurrence {
	var hits []occurrence
	for i, line := range lines {
		at := strings.Index(line, uri)
		if at < 0 {
			continue
		}
		if len(hits) == maxOccurrencesPerRef {
			hits[len(hits)-1].Truncated = true
			return hits
		}
		hits = append(hits, occurrence{Line: i + 1, Snippet: snippet(line, at, len(uri))})
	}
	return hits
}

// snippet returns the fragment of a line around a match, with whitespace
// collapsed so markup indented four levels deep still reads as a sentence.
//
// The window is centered on the match rather than taken from the start of the
// line, because a URI written as the src of a tag on a long line would
// otherwise fall outside every fragment the reader is shown.
func snippet(line string, at, width int) string {
	if len(line) > snippetLimit {
		line = window(line, at, width)
	}
	return strings.Join(strings.Fields(line), " ")
}

// window cuts snippetLimit bytes of a line around a match, marking each side it
// cut. The cut is on byte offsets, which is where strings.Index put the match;
// collapsing whitespace afterwards leaves any split rune as it found it, so the
// fragment is shown rather than repaired.
func window(line string, at, width int) string {
	pad := max((snippetLimit-width)/2, 0)
	start := max(at-pad, 0)
	end := min(at+width+pad, len(line))
	cut := line[start:end]
	if start > 0 {
		cut = "..." + cut
	}
	if end < len(line) {
		cut += "..."
	}
	return cut
}

// maxReferencingAssets bounds the used-by answer.
//
// The bound is not about the size of the payload. Narrowing the list to the
// assets a reader may open costs a share query per asset that is not theirs,
// so an unbounded read of a globally referenced file would turn one page view
// into a query per asset in the deployment. The answer says when it was cut,
// so a reader is never shown a short list as if it were the whole one.
const maxReferencingAssets = 50

// capReached is the refusal a caller gets for referencing one thing too many.
// It names the number, so someone hitting it learns the limit rather than
// guessing at it -- the same wording rule the declaration path follows.
func capReached() string {
	return fmt.Sprintf("this asset already references the maximum of %d things",
		assetrefs.MaxRefs)
}
