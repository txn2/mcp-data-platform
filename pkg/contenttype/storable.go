package contenttype

import (
	"maps"
	"slices"
)

// storableTextTypes are the media types a write path may store when the content
// reaches it as a string. Three doors carry content that way — the REST
// inline-create endpoint, the save_asset tool and the manage_asset content
// update — and all three consult this one set, because an allowlist enforced at
// one door out of three is not a control.
//
// The set is textual by construction: a JSON request body or tool argument
// cannot carry arbitrary bytes, so a family that only exists as binary has no
// way through these doors and no business being declared at them.
//
// It is security-relevant in one specific way: application/xhtml+xml is absent,
// so the family a browser renders natively with script cannot be stored under a
// declaration alone. Containment for the families that are here (HTML, JSX,
// SVG, JavaScript, XML) comes from blobserve, which serves every response under
// a sandbox CSP and hands the scriptable document families to the browser as
// attachments.
//
// Membership is exact rather than prefix-based: text/* is not a safe wildcard,
// because it admits every unregistered text/x-* type a caller cares to invent.
// Aliases still work, because the predicate normalizes before the lookup.
var storableTextTypes = map[string]bool{
	Markdown:          true,
	PlainText:         true,
	HTML:              true,
	JSX:               true,
	CSV:               true,
	TSV:               true,
	SVG:               true,
	JSON:              true,
	NDJSON:            true,
	XML:               true,
	YAML:              true,
	JavaScript:        true,
	OctetStream:       true,
	"application/sql": true,
	"text/x-python":   true,
	"text/css":        true,
}

// IsStorableText reports whether a declared media type may be stored by a write
// path that carries its content as a string.
func IsStorableText(ct string) bool {
	return storableTextTypes[Normalize(ct)]
}

// StorableTextTypes returns the canonical types IsStorableText accepts, sorted,
// so a rejected write can name what it would have taken instead.
func StorableTextTypes() []string {
	return slices.Sorted(maps.Keys(storableTextTypes))
}
