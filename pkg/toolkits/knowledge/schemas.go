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
            "description": "Type of catalog change. Valid values: update_description, add_tag, remove_tag, add_glossary_term, flag_quality_issue, add_documentation, add_curated_query, set_structured_property, remove_structured_property, raise_incident, resolve_incident, add_context_document, update_context_document, remove_context_document, add_prompt"
          },
          "target": {
            "type": "string",
            "description": "Where to apply the change. Use 'column:<fieldPath>' for column-level descriptions. For add_documentation, this is the URL. For set_structured_property/remove_structured_property, this is the property qualified name or URN. For raise_incident, this is the incident title. For resolve_incident, this is the incident URN. For add_context_document, this is the document title. For update_context_document, this is the document ID. For remove_context_document, this is the document ID. For add_prompt, this is the prompt name. For remove_tag, this is ignored. Leave empty for dataset-level updates"
          },
          "detail": {
            "type": "string",
            "description": "The content for the change: description text, tag name/URN, glossary term name/URN, quality issue description, documentation link description, query name (for add_curated_query), property value or JSON array of values (for set_structured_property), removal reason (for remove_structured_property), optional description (for raise_incident), resolution message (for resolve_incident), or document content (for add_context_document/update_context_document)"
          },
          "query_sql": {
            "type": "string",
            "description": "SQL statement (required for add_curated_query). For update_context_document, this is the new title"
          },
          "query_description": {
            "type": "string",
            "description": "Optional description for add_curated_query. For add_context_document/update_context_document, this is the document category"
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
            "description": "Type of catalog change. Valid values: update_description, add_tag, remove_tag, add_glossary_term, flag_quality_issue, add_documentation, add_curated_query, set_structured_property, remove_structured_property, raise_incident, resolve_incident, add_context_document, update_context_document, remove_context_document, add_prompt. update_description supports datasets, dashboards, charts, dataFlows, dataJobs, containers, dataProducts, domains, glossaryTerms, glossaryNodes. Column-level descriptions and add_curated_query are dataset-only. add_context_document/update_context_document work on datasets, glossaryTerms, glossaryNodes, containers. remove_context_document works on all entity types. Structured properties and incidents require DataHub 1.4.x. add_prompt creates a platform prompt (target=name, detail=content)."
          },
          "target": {
            "type": "string",
            "description": "Where to apply the change. Use 'column:<fieldPath>' for column-level descriptions (dataset-only). For add_documentation, this is the URL. For set_structured_property/remove_structured_property, this is the property qualified name or URN. For raise_incident, this is the incident title. For resolve_incident, this is the incident URN. For add_context_document, this is the document title. For update_context_document/remove_context_document, this is the document ID. For add_prompt, this is the prompt name. For remove_tag, this is ignored. Leave empty for entity-level updates"
          },
          "detail": {
            "type": "string",
            "description": "The content for the change: description text, tag name/URN, glossary term name/URN, quality issue description, documentation link description, query name (for add_curated_query), property value or JSON array (for set_structured_property), removal reason (for remove_structured_property), optional description (for raise_incident), resolution message (for resolve_incident), or document content (for add_context_document/update_context_document)"
          },
          "query_sql": {
            "type": "string",
            "description": "SQL statement (required for add_curated_query). For update_context_document, this is the new title"
          },
          "query_description": {
            "type": "string",
            "description": "Optional description for add_curated_query. For add_context_document/update_context_document, this is the document category"
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
