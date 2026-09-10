package docread

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// A .docx, .xlsx, .pptx, .odt, .ods and .odp are each a zip archive of XML
// parts, and that is the only reason they are binary at all. Handing back the
// parts is what lets a reader work with the file: the slide XML of a deck is
// what a caller reproducing it as a template needs, and flattening it to prose
// throws away the very structure they came for.
//
// A plain .zip takes the same path. The mechanism does not change with the
// extension, and declining to open one would be an arbitrary line.

// zipMagic is the local file header every zip archive opens with. Empty
// archives open with the end-of-central-directory signature instead and carry
// nothing to render, so this signature alone is the right test.
var zipMagic = []byte{'P', 'K', 0x03, 0x04}

// Archive rendering bounds. These cap the work one archive can cause,
// independent of the caller's text budget, which bounds only the output.
const (
	// maxArchiveMembers is how many entries of an archive are inspected. A
	// document of any ordinary shape is far under this; an archive above it is
	// a container of files rather than a document, and the inventory of its
	// first entries is a more useful answer than a sweep of all of them.
	maxArchiveMembers = 2000
	// maxMemberBytes bounds one member's decompressed read. It is what keeps a
	// small archive that declares an enormous member from being expanded into
	// memory: the read is limited whatever the declaration says.
	maxMemberBytes = 1 << 20
	// tailShare is the fraction of the caller's budget reserved for the
	// inventory of members that were not rendered. The inventory is what makes
	// the omission visible, so it is never crowded out by the content. Content
	// that finishes under its share leaves the remainder to the inventory.
	tailShare = 8
	// minArchiveBudget is the smallest budget an archive rendering is worth
	// attempting on. Below it the heading alone would fill the output, so the
	// caller is better served by the bytes.
	minArchiveBudget = 512
)

// section accumulates a rendering. strings.Builder never fails, so its error
// returns are discarded once here rather than at every write in the renderer.
type section struct{ b strings.Builder }

func (s *section) write(text string)                 { _, _ = s.b.WriteString(text) }
func (s *section) printf(format string, args ...any) { _, _ = fmt.Fprintf(&s.b, format, args...) }
func (s *section) len() int                          { return s.b.Len() }
func (s *section) String() string                    { return s.b.String() }

// isZipContainer reports whether body opens with a zip local file header.
func isZipContainer(body []byte) bool {
	return bytes.HasPrefix(body, zipMagic)
}

// member is one archive entry, as read from the central directory.
type member struct {
	name string
	size uint64
}

// readArchive renders a zip container as its text-bearing members, in archive
// order, followed by an inventory of everything not rendered.
//
// A container that cannot be opened as a zip falls back to opaque rather than
// erroring: the bytes are whatever they are, and the caller's own tools get a
// better chance with them than a refusal does.
func readArchive(body []byte, limit int) Result {
	if limit < minArchiveBudget {
		return Result{Form: FormOpaque, Note: "there is not enough room to render this archive; the file is served as bytes"}
	}
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return Result{Form: FormOpaque, Note: "this archive could not be opened; the file is served as bytes"}
	}

	var out section
	out.write(archiveHeading(zr))

	// The inventory's share is held back from the content, and whatever the
	// content leaves unspent is added to it, so the total is the caller's
	// limit and no more.
	skipped, truncated := renderMembers(zr, &out, limit-limit/tailShare)
	writeInventory(&out, skipped, limit-out.len())

	return Result{Form: FormText, Text: out.String(), Truncated: truncated}
}

// renderMembers writes every text-bearing member that fits within budget and
// returns the members it did not render, plus whether the budget rather than
// the archive ended the rendering.
func renderMembers(zr *zip.Reader, out *section, budget int) (skipped []member, truncated bool) {
	for i, f := range zr.File {
		if i >= maxArchiveMembers {
			skipped = append(skipped, member{name: fmt.Sprintf("(%d further entries)", len(zr.File)-i)})
			truncated = true
			break
		}
		if f.FileInfo().IsDir() {
			continue
		}
		m := member{name: f.Name, size: f.UncompressedSize64}
		// The banner is part of the output, so it is spent from the budget
		// before the member's own text is: budgeting only the text is how the
		// total came to exceed the caller's limit by a banner per member.
		heading := memberHeading(m)
		room := budget - out.len() - len(heading) - len(cutMarker) - 1
		if room <= 0 {
			skipped = append(skipped, m)
			truncated = true
			continue
		}
		text, cut, ok := memberText(f, room)
		if !ok {
			skipped = append(skipped, m)
			continue
		}
		out.write(heading)
		out.write(text)
		if cut {
			out.write(cutMarker)
			truncated = true
		}
		out.write("\n")
	}
	return skipped, truncated
}

// memberText reads a member and returns its content when the content is text,
// bounded by budget and by maxMemberBytes. cut reports that the bound, rather
// than the end of the member, ended the read -- which the caller marks in the
// output, because a part of a document silently cut mid-element reads as a
// malformed part rather than a bounded one. ok is false for a member that is
// not text, that could not be opened, or that is empty.
func memberText(f *zip.File, budget int) (text string, cut, ok bool) {
	// budget is positive by construction: renderMembers skips a member it has
	// no room for before reaching here, so there is no second guard.
	read := min(budget, maxMemberBytes)
	rc, err := f.Open()
	if err != nil {
		return "", false, false
	}
	defer func() { _ = rc.Close() }()

	// One byte past the bound, deliberately. The limit is what decides how
	// much of a member is decompressed, so an entry that declares a small size
	// and expands to a large one is cut here rather than held whole -- and
	// reading one byte more than the bound is what tells a member that ended
	// there from one that was cut. The member's declared size is not consulted
	// at all, since a zip's own header is exactly what an oversized member
	// would be lying in.
	body, err := io.ReadAll(io.LimitReader(rc, int64(read)+1))
	if err != nil || len(body) == 0 {
		return "", false, false
	}
	if cut = len(body) > read; cut {
		body = body[:read]
	}
	if !contenttype.IsTextual(contenttype.DetectFile("", f.Name, body)) {
		return "", false, false
	}
	return string(body), cut, true
}

// cutMarker closes a member whose read was ended by the budget rather than by
// the end of the member.
const cutMarker = "\n[this part was cut short here]"

// memberHeading is the one-line banner that names a member and its
// uncompressed size, so the reader can tell where one part of the document
// ends and the next begins.
func memberHeading(m member) string {
	return fmt.Sprintf("\n===== %s (%d bytes) =====\n", m.name, m.size)
}

// archiveHeading names what kind of document the archive is and how many
// entries it holds, so a reader knows before the first part whether it is
// looking at a presentation, a spreadsheet or a plain container.
func archiveHeading(zr *zip.Reader) string {
	return fmt.Sprintf("%s, %d entries. Its text parts follow, each under its own banner.\n",
		archiveKind(zr), len(zr.File))
}

// archiveKind names the document family from the archive's own structure
// rather than from a declared media type. OOXML declares itself with a
// [Content_Types].xml part and files its body under one of three roots; ODF
// declares itself with a stored `mimetype` member. Anything else is a plain
// container.
func archiveKind(zr *zip.Reader) string {
	names := make(map[string]bool, len(zr.File))
	for i, f := range zr.File {
		if i >= maxArchiveMembers {
			break
		}
		names[f.Name] = true
	}
	if names["[Content_Types].xml"] {
		for _, root := range ooxmlRoots {
			if hasPrefixedMember(zr, root.prefix) {
				return root.kind
			}
		}
		return "An Office Open XML document (a zip of XML parts)"
	}
	if names["mimetype"] {
		return "An OpenDocument file (a zip of XML parts)"
	}
	return "A zip archive"
}

// ooxmlRoots maps the directory an OOXML body lives under to the document
// family it names. It is a slice rather than a map so an archive carrying two
// of these roots -- an embedded workbook inside a deck -- is named the same
// way on every read; ranging a map would have made the answer depend on the
// iteration order.
var ooxmlRoots = []struct{ prefix, kind string }{
	{"ppt/", "A PowerPoint presentation (a zip of XML parts)"},
	{"word/", "A Word document (a zip of XML parts)"},
	{"xl/", "An Excel workbook (a zip of XML parts)"},
}

// hasPrefixedMember reports whether any member sits under prefix.
func hasPrefixedMember(zr *zip.Reader, prefix string) bool {
	for i, f := range zr.File {
		if i >= maxArchiveMembers {
			return false
		}
		if strings.HasPrefix(f.Name, prefix) {
			return true
		}
	}
	return false
}

// writeInventory appends the list of members that were not rendered, with
// their sizes, bounded by budget. The list is what makes an omission
// actionable: a reader that can see `ppt/media/image3.png` knows the deck has
// a picture there, where a reader shown only the XML would conclude it does
// not.
//
// It is ordered largest first rather than in archive order, because the list
// itself can be cut: the entries that carry the most of what was left out are
// the ones that must survive the cut.
func writeInventory(out *section, skipped []member, budget int) {
	if len(skipped) == 0 || budget <= 0 {
		return
	}
	sort.SliceStable(skipped, func(i, j int) bool { return skipped[i].size > skipped[j].size })

	var tail section
	tail.printf("\nNot rendered above (%d entries), because they are not text or the budget ran out:\n", len(skipped))
	if tail.len() > budget {
		return
	}
	shown := 0
	for _, m := range skipped {
		line := fmt.Sprintf("  %s (%d bytes)\n", m.name, m.size)
		// The "and N more" line is written from inside the loop's budget, not
		// on top of it, so the last entry admitted can never push the total
		// past the caller's limit.
		more := fmt.Sprintf("  ... and %d more\n", len(skipped)-shown-1)
		if tail.len()+len(line)+len(more) > budget {
			break
		}
		tail.write(line)
		shown++
	}
	if shown < len(skipped) {
		tail.printf("  ... and %d more\n", len(skipped)-shown)
	}
	out.write(tail.String())
}
