package knowledge

import "encoding/json"

// applyKnowledgeSchema is the JSON Schema for the apply_knowledge tool input.
// Enum constraints are omitted; server-side validation produces descriptive errors.
var applyKnowledgeSchema = json.RawMessage(`{
  "type": "object",
  "required": ["action"],
  "additionalProperties": false,
  "properties": {
    "action": {
      "type": "string",
      "description": "The action to perform. Valid values: bulk_review, review, synthesize, apply, approve, reject, rollback, list_changesets. To see the whole review queue, call bulk_review with itemize:true (the search tool is relevance-ranked and cannot list it completely)."
    },
    "itemize": {
      "type": "boolean",
      "description": "With action=bulk_review, also return the pending insights themselves (each with id, captured_by, sink_class, category, confidence, status, entity_urns, created_at), windowed by offset/limit. This is how you enumerate the global review queue."
    },
    "limit": {
      "type": "integer",
      "description": "Page size for itemized bulk_review (default 20, max 100)."
    },
    "offset": {
      "type": "integer",
      "description": "Page start for itemized bulk_review; pass the next_offset from the previous response to continue paging."
    },
    "entity_urn": {
      "type": "string",
      "description": "Target entity URN (required for review, synthesize, apply, list_changesets; optional for rollback to validate the changeset belongs to this entity)"
    },
    "changeset_id": {
      "type": "string",
      "description": "Changeset to revert (required for rollback action). Obtain it from a prior apply response or the list_changesets action."
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
      "description": "Set to true to confirm the apply or rollback action when confirmation is required"
    },
    "review_notes": {
      "type": "string",
      "description": "Notes for approve/reject actions"
    },
    "sink": {
      "type": "string",
      "description": "Apply target for the apply action: 'datahub' (default) applies the 'changes' to the catalog entity; 'knowledge_page' promotes a business_knowledge or operational_rule capture to a canonical portal knowledge page using the 'page' object. schema_entity insights go to datahub; business_knowledge and operational_rule go to a knowledge page."
    },
    "page": {
      "type": "object",
      "description": "Curated page payload for sink=knowledge_page. The page is found-or-created by slug (so repeated promotions on the same slug consolidate into one living page), and the promotion is recorded as a changeset that can be rolled back.",
      "properties": {
        "slug": {"type": "string", "description": "Stable topic slug; find-or-create key (required)"},
        "title": {"type": "string", "description": "Page title (required)"},
        "summary": {"type": "string", "description": "One-line summary (optional)"},
        "body": {"type": "string", "description": "Markdown body (required)"},
        "tags": {"type": "array", "items": {"type": "string"}, "description": "Tags; the origin sink-class is added automatically so operational rules stay filterable"}
      }
    }
  }
}`)
