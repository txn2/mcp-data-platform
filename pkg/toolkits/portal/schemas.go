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
      "description": "Action to perform. Asset actions: list, get, update, delete, list_versions, revert, search. Content actions: patch, locate, get_content, outline, stats, diff. Collection actions: create_collection, list_collections, get_collection, update_collection, delete_collection, set_sections. (Human feedback on assets is handled by the separate manage_feedback tool.)"
    },
    "asset_id": {
      "type": "string",
      "description": "Asset ID (required for get, update, delete, list_versions, revert)"
    },
    "content": {
      "type": "string",
      "description": "New content (for update action only — replaces S3 object)"
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
