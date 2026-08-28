package portal

import (
	"encoding/json"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// withPatchProperties returns base with the shared textpatch grammar merged
// into its properties, so the patch, locate, and navigation arguments are
// literally the same schema every tool that adopts the grammar advertises.
//
// It panics on a malformed base schema or a name the base already defines, both
// build-time authoring errors: the schemas are package-level constants, so a
// failure here means the binary would advertise a broken or divergent tool.
func withPatchProperties(base json.RawMessage) json.RawMessage {
	var schema map[string]any
	if err := json.Unmarshal(base, &schema); err != nil {
		panic(fmt.Sprintf("portal: manage_asset schema is not valid JSON: %v", err))
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		panic("portal: manage_asset schema has no properties object")
	}
	textpatch.AddProperties(props)
	merged, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("portal: manage_asset schema does not re-marshal: %v", err))
	}
	return merged
}

// saveAssetSchema is the JSON Schema for the save_asset tool input.
var saveAssetSchema = json.RawMessage(`{
  "type": "object",
  "required": ["name", "content", "content_type"],
  "additionalProperties": false,
  "properties": {
    "name": {
      "type": "string",
      "description": "Display name for the asset (max 255 chars)",
      "maxLength": 255
    },
    "content": {
      "type": "string",
      "description": "The asset content (JSX, HTML, SVG, Markdown, etc.)"
    },
    "content_type": {
      "type": "string",
      "description": "MIME type the asset is stored under. One of: application/json, application/octet-stream, application/sql, application/x-ndjson, application/xml, application/yaml, image/svg+xml, text/css, text/csv, text/html, text/javascript, text/jsx, text/markdown, text/plain, text/tab-separated-values, text/x-python. Anything else is refused: content arrives here as a string, so binary families (PDF, images, audio, video) belong in a managed resource instead."
    },
    "description": {
      "type": "string",
      "description": "Optional description of the asset (max 2000 chars)",
      "maxLength": 2000
    },
    "tags": {
      "type": "array",
      "description": "Optional tags for categorization (max 20 tags, each max 100 chars)",
      "items": {"type": "string", "maxLength": 100},
      "maxItems": 20
    },
    "sources": {
      "type": "array",
      "description": "The calls this asset was built from, as the call_id (or mcp:call:<id> reference) each query and API invocation returns. Give these when you know exactly which calls produced the content; they replace the default, which is every data call you made since your last save or export in this session. Only your own calls can be cited.",
      "items": {"type": "string"},
      "maxItems": 100
    },
    "references": {
      "type": "array",
      "description": "What this content names: a managed resource by its mcp:// URI, or another saved asset by its mcp:asset:<id> reference. Write the reference itself where the file belongs in your markup (<img src=\"mcp://global/brand/logo.png\">, fetch(\"mcp:asset:ast_7c1e\")) and list it here; every viewing surface rewrites it to a working URL as it serves, and the stored content keeps the reference. Do not embed a logo, photograph, design element or data table in the markup when it is already a managed resource or an asset. A referenced asset resolves to its CURRENT content on every load, which is how a report reads a data file another job refreshes. Only something you can read may be declared, and declaring it lets everyone this asset is shared with load it through this asset, including anyone holding a public link.",
      "items": {"type": "string"},
      "maxItems": 20
    }
  }
}`)

// manageAssetSchema is the JSON Schema for the manage_asset tool input: the
// asset and collection arguments below, plus the shared content-editing grammar
// spliced in from pkg/textpatch so it reads identically on manage_prompt.
var manageAssetSchema = withPatchProperties(manageAssetSchemaBase)

// manageAssetSchemaBase holds the manage_asset arguments this toolkit owns.
var manageAssetSchemaBase = json.RawMessage(`{
  "type": "object",
  "required": ["action"],
  "additionalProperties": false,
  "properties": {
    "action": {
      "type": "string",
      "description": "Action to perform. Asset actions: list, get, update, delete, list_versions, revert, search. Content actions: patch, locate, get_content, outline, stats, diff. Sharing actions: share, list_shares, revoke_share. Collection actions: create_collection, list_collections, get_collection, update_collection, delete_collection, set_sections. (Human feedback on assets is handled by the separate manage_feedback tool.)"
    },
    "asset_id": {
      "type": "string",
      "description": "Asset ID (required for get, update, delete, list_versions, revert, share, list_shares)"
    },
    "recipient": {
      "type": "string",
      "description": "Who to share the asset with (share action): an email address, or a person's name resolved against the platform's user directory. A name that matches nobody, or more than one person, is reported with the candidates and no share is created. Omit it to create a link instead of addressing a person."
    },
    "permission": {
      "type": "string",
      "enum": ["viewer", "editor"],
      "description": "What the recipient of a share may do (share action). Defaults to viewer. Only a share addressed to a person can be an editor; a link is always viewer."
    },
    "access_mode": {
      "type": "string",
      "enum": ["authenticated", "public"],
      "description": "Who a link share admits (share action, when no recipient is named): 'authenticated' (the default) opens for any signed-in platform user and lasts until revoked; 'public' opens for anyone holding the URL without signing in and requires expires_in."
    },
    "expires_in": {
      "type": "string",
      "description": "How long a public link lasts, as a duration ('24h', '30m'). Required for access_mode public and refused for every other share, which ends on revocation rather than on a clock."
    },
    "share_id": {
      "type": "string",
      "description": "Share ID to revoke (required for revoke_share). Call list_shares to see the shares on an asset."
    },
    "content": {
      "type": "string",
      "description": "New content (for update action only — replaces S3 object)"
    },
    "max_versions": {
      "type": ["integer", "null"],
      "minimum": 0,
      "description": "How many versions this asset keeps (update action). Omit to leave the setting alone, send null to go back to the deployment default, 0 to keep every version, or N to keep the newest N. Older versions are deleted with their stored content when a new version pushes them past the cap — set this on an asset a scheduled script rewrites, which would otherwise accumulate a version per run forever."
    },
    "name": {
      "type": "string",
      "description": "Name (for update, create_collection, update_collection)"
    },
    "description": {
      "type": "string",
      "description": "Description (for update, create_collection, update_collection)"
    },
    "tags": {
      "type": "array",
      "description": "New tags (for update action)",
      "items": {"type": "string"},
      "maxItems": 20
    },
    "content_type": {
      "type": "string",
      "description": "New content type (for update action, only when replacing content). Accepts the same types as save_asset; omit it to keep the type the asset already carries."
    },
    "change_summary": {
      "type": "string",
      "description": "Human-readable summary of the change, recorded as the new version's change summary (update and patch actions). Defaults to a generated summary for a patch."
    },
    "sources": {
      "type": "array",
      "description": "The calls behind this edit, as the call_id (or mcp:call:<id> reference) each query and API invocation returns. Recorded as a new provenance capture alongside the ones earlier versions carry (update and patch actions). Omit to capture every data call you made since your last save or export in this session. Only your own calls can be cited.",
      "items": {"type": "string"},
      "maxItems": 100
    },
    "references": {
      "type": "array",
      "description": "What this content names: a managed resource by its mcp:// URI, or another saved asset by its mcp:asset:<id> reference (update and patch actions). Replaces whatever the asset referenced before: omit it to leave the references alone, pass an empty list to remove them all. Write the reference itself where the file belongs in your markup and list it here; every viewing surface rewrites it to a working URL as it serves, and the stored content keeps the reference. A referenced asset resolves to its CURRENT content on every load, which is how a report reads a data file another job refreshes. Only something you can read may be declared, and declaring it lets everyone this asset is shared with load it through this asset, including anyone holding a public link.",
      "items": {"type": "string"},
      "maxItems": 20
    },
    "limit": {
      "type": "integer",
      "description": "Max results for list/list_versions/list_collections (default 50, max 200)"
    },
    "version": {
      "type": "integer",
      "description": "Version number (required for revert action)"
    },
    "collection_id": {
      "type": "string",
      "description": "Collection ID (required for get_collection, update_collection, delete_collection, set_sections)"
    },
    "search": {
      "type": "string",
      "description": "Substring filter for list_collections"
    },
    "query": {
      "type": "string",
      "description": "Free-text relevance query for the 'search' action. Ranks your saved assets by semantic + keyword similarity within your own assets."
    },
    "offset": {
      "type": "integer",
      "description": "Offset for paginated results (list_collections)"
    },
    "sections": {
      "type": "array",
      "description": "Sections with asset references (for create_collection and set_sections)",
      "items": {
        "type": "object",
        "required": ["title", "items"],
        "additionalProperties": false,
        "properties": {
          "title": {
            "type": "string",
            "description": "Section title"
          },
          "description": {
            "type": "string",
            "description": "Optional section description"
          },
          "items": {
            "type": "array",
            "description": "Assets in this section",
            "items": {
              "type": "object",
              "required": ["asset_id"],
              "additionalProperties": false,
              "properties": {
                "asset_id": {
                  "type": "string",
                  "description": "ID of the asset to include"
                }
              }
            }
          }
        }
      }
    }
  }
}`)

// manageFeedbackSchema is the JSON Schema for the manage_feedback tool input.
var manageFeedbackSchema = json.RawMessage(`{
  "type": "object",
  "required": ["action"],
  "additionalProperties": false,
  "properties": {
    "action": {
      "type": "string",
      "description": "Action to perform. list (with NO target = your pending feedback across the assets and collections you own or can edit AND the general channel, newest first, excluding your own threads, plus threads awaiting your validation; with a target = threads on that one asset/collection/prompt or the standalone channel). get (one thread + its timeline). reply (post a comment). resolve (mark resolved). request_validation (route a validation request to the thread author). respond_validation (the thread author records validated/disputed via validation_result)."
    },
    "asset_id": {
      "type": "string",
      "description": "Scope list to one asset, or unused for thread-id actions"
    },
    "collection_id": {
      "type": "string",
      "description": "Scope list to one collection"
    },
    "prompt_id": {
      "type": "string",
      "description": "Scope list to one prompt"
    },
    "target_type": {
      "type": "string",
      "description": "Use 'standalone' to scope list to the general channel (feedback not tied to an asset, collection, or prompt)"
    },
    "thread_id": {
      "type": "string",
      "description": "Feedback thread ID (required for get, reply, resolve, request_validation, respond_validation)"
    },
    "body": {
      "type": "string",
      "description": "Reply text (required for reply)"
    },
    "status": {
      "type": "string",
      "description": "Filter a targeted list by thread status (open, answered, resolved, wont_fix, acknowledged)"
    },
    "validation_state": {
      "type": "string",
      "description": "Filter a targeted list by validation state (none, pending, validated, disputed)"
    },
    "requires_resolution": {
      "type": "boolean",
      "description": "Filter a targeted list to threads that do (true) or do not (false) require resolution"
    },
    "validation_result": {
      "type": "string",
      "enum": ["validated", "disputed"],
      "description": "For respond_validation: the author's outcome on a validation request"
    },
    "validation_reason": {
      "type": "string",
      "description": "For respond_validation: optional reason recorded on the validation_result event (useful when disputed)"
    },
    "limit": {
      "type": "integer",
      "description": "Max results (default 50, max 200)"
    },
    "offset": {
      "type": "integer",
      "description": "Offset for paginated results"
    }
  }
}`)

// manageTableSchema is the JSON Schema for the manage_table tool input
// (#1428). It is keyed by reference rather than by an id, which is what lets
// one tool serve every kind of stored file: the kind travels inside the
// reference, so there is no per-kind argument and no second tool.
var manageTableSchema = json.RawMessage(`{
  "type": "object",
  "required": ["action"],
  "additionalProperties": false,
  "properties": {
    "action": {
      "type": "string",
      "enum": ["register", "list", "unregister"],
      "description": "What to do: register the file as a table, list the tables already registered over it, or unregister one."
    },
    "reference": {
      "type": "string",
      "description": "The stored file to act on, named by the reference a search hit or fetch document carries: mcp:resource:<id> for uploaded reference material, mcp:asset:<id> for a saved asset. Pass it verbatim. Required for register and list; unregister takes registration_id instead, since a registration already knows its file."
    },
    "connection": {
      "type": "string",
      "description": "Trino connection whose scratch schema the table is created in (required for register). Call list_connections to see the connections you can reach; only a connection an administrator has given a scratch catalog and schema can hold a table."
    },
    "table_name": {
      "type": "string",
      "description": "Name for the registered table (register). Optional: the default is a slug of the file's name. Either way the name is prefixed with your persona, because the scratch schema is shared by everyone granted the connection."
    },
    "registration_id": {
      "type": "string",
      "description": "Registration to drop (required for unregister). Call action=list to see the tables registered over a file."
    },
    "repair": {
      "type": "boolean",
      "description": "For register: save a corrected version of the file and register that, when the file cannot be read as a table the way it is stored. A CSV whose lines end in a carriage return rather than a newline, one with a line break inside a cell, or one whose bytes are a legacy code page rather than UTF-8, is refused without this and the refusal says what is wrong with it. With it, a corrected version is written through the file's own version history -- the uploaded bytes stay as the version before it and the correction is revertible -- and the result says what changed. A file in a wide encoding (UTF-16, UTF-32) is refused whether or not this is set: it has to be re-exported as UTF-8 CSV."
    }
  }
}`)

// manageResourceSchema is the JSON Schema for the manage_resource tool input
// (#1487). Content arrives in one of two fields rather than one field with an
// encoding flag, so a caller cannot declare an encoding the bytes are not in.
var manageResourceSchema = json.RawMessage(`{
  "type": "object",
  "required": ["action"],
  "additionalProperties": false,
  "properties": {
    "action": {
      "type": "string",
      "enum": ["create", "replace_content"],
      "description": "What to do: file new content as a managed resource, or write new content over an existing one."
    },
    "reference": {
      "type": "string",
      "description": "The managed resource to write over (required for replace_content), named by the mcp:resource:<id> reference a search hit, a fetch document, or a create reported. Pass it verbatim."
    },
    "content": {
      "type": "string",
      "description": "The file as text: CSV, JSON, Markdown, SVG. Pass this or content_base64, not both."
    },
    "content_base64": {
      "type": "string",
      "description": "The file as base64-encoded bytes, for a binary file such as a PNG or a PDF. Pass this or content, not both."
    },
    "content_type": {
      "type": "string",
      "description": "Media type the bytes are, for example image/svg+xml, text/markdown, text/html, text/csv or image/png. REQUIRED for create, and not detected for you: SVG, HTML, JSX and Markdown all read as plain text to a byte sniffer, and a file stored as text/plain is served under nosniff, which stops a browser rendering it as an image or a document. replace_content keeps the type the resource already carries when you omit it, so send one there only to change what family the file is; a file stored under a generic type (text/plain or application/octet-stream) is re-detected from its bytes instead, which is the way back for one written before the declaration was required. Fetch mcp:knowledge_page:platform-content-types-for-stored-files for the types this platform stores."
    },
    "filename": {
      "type": "string",
      "description": "Name of the file (required for create), for example weather-daily.csv. It is normalized to lowercase with spaces replaced, and it becomes part of the resource's permanent mcp:// uri. replace_content ignores it: a replacement never renames the file, because the name is embedded in every reference to it."
    },
    "display_name": {
      "type": "string",
      "description": "Human-readable name shown in the resource library (required for create)."
    },
    "category": {
      "type": "string",
      "description": "The shelf the file sits on in the library (required for create), for example datasets, runbooks or templates. Lowercase letters, digits and hyphens, starting with a letter."
    },
    "description": {
      "type": "string",
      "description": "What the file is and what reads it (required for create). It is what a person browsing the library and a search hit both show, so a file with no description is one nobody can place."
    },
    "tags": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Tags for filtering in the library (create). Lowercase letters, digits and hyphens."
    },
    "scope": {
      "type": "string",
      "enum": ["user", "persona", "global"],
      "description": "Who the resource is visible to (create). Defaults to user, your own scope, which every signed-in caller may write. persona needs administrator authority over that persona and global needs platform administrator; a refusal names the scope rather than the file."
    },
    "scope_id": {
      "type": "string",
      "description": "The persona name for scope=persona, or the user for scope=user (create). Defaults to you for scope=user; must be empty for scope=global."
    },
    "change_summary": {
      "type": "string",
      "description": "Why the content changed (replace_content). It is what the file's version history shows beside this revision, so a person reading the history sees the reason without having to find the run that made it."
    }
  }
}`)
