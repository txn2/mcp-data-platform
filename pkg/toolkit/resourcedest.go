package toolkit

import (
	"context"
	"io"
)

// ResourceDestination is the managed resource an export lands in, addressed by
// the path it lives at rather than by the id it was given (#1663).
//
// The path is the address because identity is what an export destination has to
// survive: the same call on Monday and on Tuesday means one file with two
// versions, not two files. A caller that had to name an id would have to
// remember one, and a scheduled script's memory of its own output is exactly
// the thing that is cleared and rewritten.
//
// Every export tool that can write a portal asset can write one of these
// instead, which is why the type lives here: one destination vocabulary, one
// result shape, one implementation behind them, rather than a copy per toolkit.
type ResourceDestination struct {
	// Scope and ScopeID name the library the file is filed in. An empty scope
	// means the caller's own library, which is the one place every
	// authenticated caller may write.
	Scope   string `json:"scope,omitempty"`
	ScopeID string `json:"scope_id,omitempty"`
	// Path is the folder chain inside the library, for example
	// "datasets/media-manager". It is required: a managed resource is filed in
	// a folder, and the folder is half of the address this destination is.
	Path string `json:"path,omitempty"`
	// Filename is the file's name within that folder, and the last segment of
	// the canonical mcp:// URI. Together with the library and the path it is
	// the destination's whole address.
	Filename string `json:"filename,omitempty"`
	// ChangeSummary is what the version history shows beside the revision a
	// replacement records. Empty takes the platform's default.
	ChangeSummary string `json:"change_summary,omitempty"`

	// DisplayName, Description and Tags label the file, and are deliberately
	// NOT wire fields: every export tool already takes a name, a description
	// and tags for the asset it would otherwise write, and those are what label
	// the resource. One set of labels per call, rather than two that can
	// disagree about what the same bytes are called.
	//
	// They are recorded on a create and left alone by a replacement: a file
	// people have since renamed or re-tagged in the portal is not re-labeled
	// by the next scheduled refresh of its contents.
	DisplayName string   `json:"-"`
	Description string   `json:"-"`
	Tags        []string `json:"-"`
}

// ResourceDestinationSchema is the JSON Schema of the destination as an export
// tool publishes it, shared so the four export tools describe one capability in
// one set of words. It is a property value, to be spliced in under the name
// "resource".
//
// Closed to unknown keys, like the input schemas it sits inside: a misspelled
// "file_name" is refused by name rather than landing the file at an address
// nobody asked for.
const ResourceDestinationSchema = `{
      "type": "object",
      "additionalProperties": false,
      "required": ["path", "filename"],
      "description": "Land the response in a MANAGED RESOURCE at this path instead of creating a new portal asset. A managed resource has a stable id and mcp:// URI, a version history, and tables registered over it follow its content: exporting to the same path again records the NEXT VERSION of the same file rather than making a second one, so a scheduled refresh keeps one rolling file per source and nothing referencing it has to be re-pointed. The result carries the mcp:resource:<id> reference, the uri, the version written, and what the write did to any table registered over the file. Mutually exclusive with idempotency_key and create_public_link, which belong to an asset.",
      "properties": {
        "path": {
          "type": "string",
          "description": "Folder chain inside the library the file is filed under, for example \"datasets\" or \"datasets/media-manager/shows\". Required."
        },
        "filename": {
          "type": "string",
          "description": "The file's name in that folder, for example \"orders.csv\". Required. It is the last segment of the mcp:// URI and never changes across replacements, so give the file the extension its content deserves: a .csv landed here is registerable as a table with manage_table."
        },
        "scope": {
          "type": "string",
          "enum": ["user", "persona", "global"],
          "description": "Which library to file it in. Defaults to the caller's own (user). A persona or global library is visible to other people and takes the matching administrator role."
        },
        "scope_id": {
          "type": "string",
          "description": "The persona name for scope=persona, or the person's address for another user's library (administrators only). Omitted for scope=global and for the caller's own library."
        },
        "change_summary": {
          "type": "string",
          "description": "What the version history shows beside this revision, for example \"nightly pull 2026-09-09\"."
        }
      }
    }`

// ResourceLanding is what a landed export reports: the two names the caller
// hands to the next call, what the write did to the file's identity, and what
// it did to the tables registered over it.
//
// It leads with the reference and the URI for the reason manage_resource's own
// result does: a write whose result cannot be passed to the next call is a
// write the caller has to go looking for.
type ResourceLanding struct {
	ResourceID string `json:"resource_id"`
	// Reference is the mcp:resource:<id> form every tool that takes a
	// reference accepts.
	Reference string `json:"reference"`
	// URI is the canonical mcp:// address, which is what save_asset's
	// 'references' argument takes.
	URI         string `json:"uri"`
	Filename    string `json:"filename"`
	Scope       string `json:"scope"`
	ScopeID     string `json:"scope_id,omitempty"`
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	// Version is the version number the content was recorded as: 1 for a
	// create, and the next number for every replacement after it.
	Version int `json:"version"`
	// Created distinguishes the two halves of create-or-replace, because they
	// mean different things to the caller: a create is a new address to wire
	// something up to, a replacement is an address that already has readers.
	Created bool `json:"created"`
	// TableChanges is one sentence per table registered over the file, saying
	// it followed onto the new version or is pinned and now behind it (#1536).
	// Absent when no table is registered over the file. It reports what this
	// write did, so it is named apart from the `tables` a fetched reference
	// carries, which are the rows a caller queries (#1666).
	TableChanges []string `json:"table_changes,omitempty"`
	Message      string   `json:"message"`
}

// ResourceLander writes an export's bytes into a managed resource at a path,
// creating the file the first time and recording a new version of it every time
// after.
//
// It is two calls rather than one because an export's bytes are produced by
// asking an upstream for them, and a destination the platform would refuse must
// be refused BEFORE that request is made: a POST that cannot land anywhere has
// still been sent. Check is that refusal, and it is the same validation Land
// applies, so a destination Check accepts is one Land can write.
//
// Implemented once, by the platform's managed-resource writer. A toolkit holds
// the interface and never the implementation, so the identity a write is made
// under is derived from the request rather than supplied by the tool.
type ResourceLander interface {
	// Check validates the destination and the caller's authority to write
	// there, writing nothing. A nil error means Land will not refuse the
	// address.
	CheckResourceDestination(ctx context.Context, dest ResourceDestination) error
	// LandResource streams content into the destination, creating the resource
	// or recording a new version of the one already at that address. The
	// contentType is what the producer says the bytes are; the destination's
	// filename refines it where the producer could only say "text".
	LandResource(
		ctx context.Context, dest ResourceDestination, content io.Reader, contentType string,
	) (*ResourceLanding, error)
}
