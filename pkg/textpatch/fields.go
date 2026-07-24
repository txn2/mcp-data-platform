package textpatch

// Response field names. The verbs answer identically on every tool that adopts
// the grammar, so the wire keys live here beside the argument schema rather
// than being re-declared per toolkit, where they could drift apart.
const (
	FieldSizeBytes = "size_bytes"
	FieldLines     = "lines"
	FieldSections  = "sections"
	FieldLandmarks = "landmarks"
	FieldHash      = "hash"
	FieldContent   = "content"
	FieldSection   = "section"
	FieldCount     = "count"
	FieldMatches   = "matches"
	FieldTruncated = "truncated"
	FieldEdits     = "edits"
	FieldDiff      = "diff"
)

// The Fields builders below render the body-derived half of each verb's
// response. A toolkit merges its own identity keys (an asset id and content
// type, a prompt name and status) into the returned map, so the shape that
// describes the document is defined once for every kind.

// OutlineFields renders the heading tree plus the document's size and line
// count. The section list is never nil, so it serializes as [] not null. On HTML
// syntax it also reports the addressable landmarks (elements carrying an id or a
// data-* marker), which is the whole answer for a JSX dashboard with no headings.
func OutlineFields(body string, syntax Syntax) map[string]any {
	fields := map[string]any{
		FieldSizeBytes: len(body),
		FieldLines:     CountLines(body),
		FieldSections:  Outline(body, syntax),
	}
	if syntax == SyntaxHTML {
		fields[FieldLandmarks] = htmlLandmarks(body)
	}
	return fields
}

// StatsFields renders size, line count, and content hash, with none of the body.
func StatsFields(body string) map[string]any {
	stats := DocStats(body)
	return map[string]any{
		FieldSizeBytes: stats.SizeBytes,
		FieldLines:     stats.Lines,
		FieldHash:      stats.Hash,
	}
}

// ContentFields renders a requested span of the document alongside the whole
// document's size and line count, so a caller reading one section still learns
// how much it did not read. The resolved heading is reported only for a section
// read.
func ContentFields(body string, req ContentRequest) (map[string]any, error) {
	text, sec, err := Content(body, req)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{
		FieldSizeBytes: len(body),
		FieldLines:     CountLines(body),
		FieldContent:   text,
	}
	if sec.Heading != "" {
		fields[FieldSection] = sec
	}
	return fields, nil
}

// LocateFields renders every match of an anchor with the total count, which is
// the number that tells an agent whether the anchor is safe to patch with.
func LocateFields(body string, q LocateQuery, opts Options) (map[string]any, error) {
	found, err := Locate(body, q, opts)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		FieldCount:     found.Count,
		FieldMatches:   found.Matches,
		FieldTruncated: found.Truncated,
	}, nil
}

// PatchFields renders the outcome of an applied or dry-run patch: the per-edit
// report and a unified diff of the changed hunks, never the new body.
func PatchFields(res Result) map[string]any {
	return map[string]any{
		FieldEdits:     res.Edits,
		FieldDiff:      res.Diff,
		FieldSizeBytes: res.SizeBytes,
		FieldLines:     res.Lines,
	}
}
