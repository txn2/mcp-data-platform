package knowledge

import "encoding/json"

// captureInsightSchema is the JSON Schema for the capture_insight tool input.
// Enum constraints are intentionally omitted from the schema so that the MCP
// transport layer does not reject invalid values with generic error messages.
// Instead, server-side validation in types.go produces descriptive errors
// (e.g., "invalid category 'foo': must be one of: ...").
// Valid values are listed in field descriptions so LLM clients discover them.
var captureInsightSchema = json.RawMessage(`{
  "type": "object",
  "required": ["category", "insight_text"],
  "additionalProperties": false,
  "properties": {
    "category": {
      "type": "string",
      "description": "Type of insight. Valid values: correction, business_context, data_quality, usage_guidance, relationship, enhancement"
    },
    "insight_text": {
      "type": "string",
      "description": "The insight content (10-4000 characters)",
      "minLength": 10,
      "maxLength": 4000
    },
    "confidence": {
      "type": "string",
      "description": "Confidence level (defaults to 'medium'). Valid values: high, medium, low"
    },
    "source": {
      "type": "string",
      "description": "Origin of the insight (defaults to 'user'). Valid values: user, agent_discovery, enrichment_gap"
    },
    "entity_urns": {
      "type": "array",
      "description": "DataHub entity URNs this insight relates to",
      "items": {"type": "string"},
      "maxItems": 10
    },
    "related_columns": {
      "type": "array",
      "description": "Columns related to this insight",
      "items": {
        "type": "object",
        "required": ["urn", "column", "relevance"],
        "properties": {
          "urn": {"type": "string", "description": "Dataset URN"},
          "column": {"type": "string", "description": "Column name"},
          "relevance": {"type": "string", "description": "How this column relates to the insight"}
        }
      },
      "maxItems": 20
    },
    "suggested_actions": {
      "type": "array",
      "description": "Proposed catalog changes based on this insight",
      "items": {
        "type": "object",
        "required": ["action_type", "target", "detail"],
        "properties": {
          "action_type": {
            "type": "string",
            "description": "Type of catalog change. Valid values: update_description, add_tag, remove_tag, add_glossary_term, flag_quality_issue, add_documentation, add_curated_query"
          },
          "target": {
            "type": "string",
            "description": "Where to apply the change. Use 'column:<fieldPath>' for column-level descriptions (e.g., 'column:location_type_id'). For add_documentation, this is the URL. For remove_tag, this is ignored. Leave empty for dataset-level updates"
          },
          "detail": {
            "type": "string",
            "description": "The content for the change: description text, tag name or URN (e.g., 'pii' or 'urn:li:tag:pii'), tag URN to remove, glossary term name or URN, quality issue description, documentation link description, or query name (for add_curated_query)"
          },
          "query_sql": {
            "type": "string",
            "description": "SQL statement for the curated query (required for add_curated_query)"
          },
          "query_description": {
            "type": "string",
            "description": "Optional description for the curated query (used with add_curated_query)"
          }
        }
      },
      "maxItems": 5
    }
  }
}`)

// applyKnowledgeSchema is the JSON Schema for the apply_knowledge tool input.
// See captureInsightSchema comment for why enum constraints are omitted.
var applyKnowledgeSchema = json.RawMessage(`{
  "type": "object",
  "required": ["action"],
  "additionalProperties": false,
  "properties": {
    "action": {
      "type": "string",
      "description": "The action to perform. Valid values: bulk_review, review, synthesize, apply, approve, reject"
    },
    "entity_urn": {
      "type": "string",
      "description": "Target entity URN (required for review, synthesize, apply)"
    },
    "insight_ids": {
      "type": "array",
      "description": "Insight IDs to operate on (required for approve, reject)",
      "items": {"type": "string"},
      "maxItems": 50
    },
    "changes": {
      "type": "array",
      "description": "Changes to apply (required for apply action)",
      "items": {
        "type": "object",
        "required": ["change_type", "target", "detail"],
        "properties": {
          "change_type": {
            "type": "string",
            "description": "Type of catalog change. Valid values: update_description, add_tag, remove_tag, add_glossary_term, flag_quality_issue, add_documentation, add_curated_query"
          },
          "target": {
            "type": "string",
            "description": "Where to apply the change. Use 'column:<fieldPath>' for column-level descriptions (e.g., 'column:location_type_id'). For add_documentation, this is the URL. For remove_tag, this is ignored. Leave empty for dataset-level updates"
          },
          "detail": {
            "type": "string",
            "description": "The content for the change: description text, tag name or URN (e.g., 'pii' or 'urn:li:tag:pii'), tag URN to remove (e.g., 'urn:li:tag:QualityIssue'), glossary term name or URN, quality issue description, documentation link description, or query name (for add_curated_query)"
          },
          "query_sql": {
            "type": "string",
            "description": "SQL statement for the curated query (required for add_curated_query)"
          },
          "query_description": {
            "type": "string",
            "description": "Optional description for the curated query (used with add_curated_query)"
          }
        }
      },
      "maxItems": 20
    },
    "confirm": {
      "type": "boolean",
      "description": "Set to true to confirm apply action when confirmation is required"
    },
    "review_notes": {
      "type": "string",
      "description": "Notes for approve/reject actions"
    }
  }
}`)
