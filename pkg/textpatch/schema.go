package textpatch

import (
	"encoding/json"
	"fmt"
)

// PropertiesJSON is the JSON Schema fragment for the patch, search, and
// navigation arguments. Every tool that adopts the grammar splices these exact
// properties into its own input schema, so the grammar an agent reads on
// manage_asset is literally the grammar it reads on manage_prompt.
const PropertiesJSON = `{
  "edits": {
    "type": "array",
    "description": "Ordered edits to apply (patch action). Each edit is anchored on text, never on a line number, and edits apply in order against the evolving body, so a later edit can anchor on text an earlier one introduced. If any edit fails to resolve, the whole call is refused and nothing is written.",
    "maxItems": 100,
    "items": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "op": {
          "type": "string",
          "enum": ["replace", "insert_before", "insert_after", "replace_section", "move_section", "append", "prepend"],
          "description": "Operation. Defaults to 'replace' when omitted."
        },
        "find": {
          "type": "string",
          "description": "Literal anchor text. Must match exactly one span unless 'occurrence' is set. Used by replace, insert_before, insert_after."
        },
        "pattern": {
          "type": "string",
          "description": "Regular expression (Go RE2) anchor, as an alternative to 'find'. $1-style capture references are expanded in 'replace'."
        },
        "replace": {
          "type": "string",
          "description": "Replacement for the matched span (op=replace). An empty string deletes the match."
        },
        "text": {
          "type": "string",
          "description": "Text to insert or to become the section body (insert_before, insert_after, replace_section, append, prepend)."
        },
        "section": {
          "type": "string",
          "description": "Markdown heading naming a section ('## Methodology', or a 'Report > Methodology' path when headings repeat). Required by replace_section and move_section; on the anchored operations it scopes the anchor search to that one section."
        },
        "occurrence": {
          "type": "string",
          "description": "Which match to act on when the anchor is not unique: 'first', 'last', 'all', or a 1-based index. Omit to require a unique anchor."
        },
        "before": {
          "type": "string",
          "description": "move_section destination: place the section before this heading."
        },
        "after": {
          "type": "string",
          "description": "move_section destination: place the section after this heading."
        },
        "position": {
          "type": "string",
          "enum": ["start", "end"],
          "description": "move_section destination: the start or end of the document."
        }
      }
    }
  },
  "base_version": {
    "type": "integer",
    "description": "Version this patch was composed against (patch action). Optional; when supplied a mismatch with the current version is refused with PATCH_STALE_BASE and nothing is written."
  },
  "dry_run": {
    "type": "boolean",
    "description": "Resolve every edit and return the same per-edit report and diff without writing anything (patch action)."
  },
  "find": {
    "type": "string",
    "description": "Literal text to search for (locate action)."
  },
  "pattern": {
    "type": "string",
    "description": "Regular expression to search for (locate action), as an alternative to 'find'."
  },
  "section": {
    "type": "string",
    "description": "Markdown heading naming a section: scopes 'locate' to one section, or selects the span 'get_content' returns."
  },
  "line_start": {
    "type": "integer",
    "description": "First line to read, 1-based (get_content action)."
  },
  "line_end": {
    "type": "integer",
    "description": "Last line to read, inclusive (get_content action). Omit to read to the end."
  },
  "context_bytes": {
    "type": "integer",
    "description": "How much surrounding text each locate match reports (default 160)."
  },
  "from_version": {
    "type": "integer",
    "description": "Older side of a 'diff' comparison."
  },
  "to_version": {
    "type": "integer",
    "description": "Newer side of a 'diff' comparison. Defaults to the current version."
  }
}`

// PropertiesMap returns PropertiesJSON decoded, for tools whose input schema is
// built as a Go map rather than raw JSON. It panics on a malformed constant,
// which is a build-time authoring error rather than a runtime condition.
func PropertiesMap() map[string]any {
	var props map[string]any
	if err := json.Unmarshal([]byte(PropertiesJSON), &props); err != nil {
		panic(fmt.Sprintf("textpatch: PropertiesJSON is not valid JSON: %v", err))
	}
	return props
}

// AddProperties splices the shared grammar into a tool's schema properties.
//
// A name collision panics rather than resolving silently: the whole point of
// the shared fragment is that the grammar reads identically on every tool, so a
// tool redefining one of these names is an authoring error that must surface at
// build time, not a divergence to be tolerated at runtime.
func AddProperties(props map[string]any) {
	for name, prop := range PropertiesMap() {
		if _, exists := props[name]; exists {
			panic(fmt.Sprintf("textpatch: tool schema redefines shared property %q", name))
		}
		props[name] = prop
	}
}

// VerbsDescription is the shared steering text every tool that adopts the
// grammar appends to its own description, so the rule reads identically
// wherever an agent meets it.
const VerbsDescription = "To change part of existing content, use 'patch' with anchored edits; do not read the whole " +
	"body back and resend it. To find the part to change, use 'locate' (literal or regex search reporting line " +
	"numbers, enclosing section, and a copyable context window) or 'outline' (heading tree with sizes). " +
	"'get_content' reads the whole body, one section, or a line range; 'stats' reports size, line count, version, " +
	"and content hash without any body; 'diff' compares two versions. Anchors are text, never line numbers: an " +
	"anchor that matches nothing or matches ambiguously refuses the whole call and writes nothing. The 'content' " +
	"argument remains the way to do a genuine full rewrite."
