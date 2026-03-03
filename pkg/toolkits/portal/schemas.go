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
      "description": "Action to perform. Valid values: list, get, update, delete"
    },
    "asset_id": {
      "type": "string",
      "description": "Asset ID (required for get, update, delete)"
    },
    "content": {
      "type": "string",
      "description": "New content (for update action only — replaces S3 object)"
    },
    "name": {
      "type": "string",
      "description": "New name (for update action)"
    },
    "description": {
      "type": "string",
      "description": "New description (for update action)"
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
      "description": "Max results for list action (default 50, max 200)"
    }
  }
}`)
