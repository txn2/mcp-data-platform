package portal

import "encoding/json"

// saveArtifactSchema is the JSON Schema for the save_artifact tool input.
var saveArtifactSchema = json.RawMessage(`{
  "type": "object",
  "required": ["name", "content", "content_type"],
  "additionalProperties": false,
  "properties": {
    "name": {
      "type": "string",
      "description": "Display name for the artifact (max 255 chars)",
      "maxLength": 255
    },
    "content": {
      "type": "string",
      "description": "The artifact content (JSX, HTML, SVG, Markdown, etc.)"
    },
    "content_type": {
      "type": "string",
      "description": "MIME type: text/html, text/jsx, image/svg+xml, text/markdown, application/json, text/csv"
    },
    "description": {
      "type": "string",
      "description": "Optional description of the artifact (max 2000 chars)",
      "maxLength": 2000
    },
    "tags": {
      "type": "array",
      "description": "Optional tags for categorization (max 20 tags, each max 100 chars)",
      "items": {"type": "string", "maxLength": 100},
      "maxItems": 20
    }
  }
}`)

// manageArtifactSchema is the JSON Schema for the manage_artifact tool input.
var manageArtifactSchema = json.RawMessage(`{
  "type": "object",
  "required": ["action"],
  "additionalProperties": false,
  "properties": {
    "action": {
      "type": "string",
      "description": "Action to perform. Asset actions: list, get, update, delete, list_versions, revert, search. Collection actions: create_collection, list_collections, get_collection, update_collection, delete_collection, set_sections. Feedback actions: list_threads (scope by asset_id/collection_id/prompt_id or target_type=standalone; optional status/validation_state/requires_resolution filters), get_thread, reply_thread, resolve_thread, request_validation"
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
      "description": "New content type (for update action, only when replacing content)"
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
    },
    "prompt_id": {
      "type": "string",
      "description": "Prompt ID target for list_threads (feedback on a prompt)"
    },
    "target_type": {
      "type": "string",
      "description": "Thread target scope for list_threads. Use 'standalone' for general feedback not tied to an artifact"
    },
    "thread_id": {
      "type": "string",
      "description": "Feedback thread ID (required for get_thread, reply_thread, resolve_thread, request_validation)"
    },
    "body": {
      "type": "string",
      "description": "Reply text (required for reply_thread)"
    },
    "status": {
      "type": "string",
      "description": "Filter list_threads by thread status (open, answered, resolved, wont_fix, acknowledged)"
    },
    "validation_state": {
      "type": "string",
      "description": "Filter list_threads by validation state (none, pending, validated, disputed)"
    },
    "requires_resolution": {
      "type": "boolean",
      "description": "Filter list_threads to threads that do (true) or do not (false) require resolution"
    }
  }
}`)
