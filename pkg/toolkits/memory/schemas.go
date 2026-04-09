package memory

import "encoding/json"

// memoryManageSchema is the JSON Schema for the memory_manage tool input.
//
//nolint:gochecknoglobals // MCP tool schema must be a package-level var
var memoryManageSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "command": {
      "type": "string",
      "description": "Operation: remember, update, forget, list, review_stale. Call without a command to see available commands."
    },
    "content": {
      "type": "string",
      "description": "Memory content text. Required for 'remember'. Min 10, max 4000 characters."
    },
    "id": {
      "type": "string",
      "description": "Memory record ID. Required for 'update' and 'forget'."
    },
    "dimension": {
      "type": "string",
      "description": "LOCOMO dimension: knowledge, event, entity, relationship, preference. Defaults to 'knowledge'."
    },
    "category": {
      "type": "string",
      "description": "Category: correction, business_context, data_quality, usage_guidance, relationship, enhancement, general."
    },
    "confidence": {
      "type": "string",
      "description": "Confidence level: high, medium, low. Defaults to 'medium'."
    },
    "source": {
      "type": "string",
      "description": "Source: user, agent_discovery, enrichment_gap, automation, lineage_event. Defaults to 'user'."
    },
    "entity_urns": {
      "type": "array",
      "items": {"type": "string"},
      "description": "DataHub entity URNs this memory relates to. Max 10."
    },
    "metadata": {
      "type": "object",
      "description": "Arbitrary metadata (e.g., suggested_actions, superseded_by)."
    },
    "filter_dimension": {
      "type": "string",
      "description": "Filter by dimension for 'list'."
    },
    "filter_category": {
      "type": "string",
      "description": "Filter by category for 'list'."
    },
    "filter_status": {
      "type": "string",
      "description": "Filter by status for 'list'. Default: 'active'."
    },
    "filter_entity_urn": {
      "type": "string",
      "description": "Filter by entity URN for 'list'."
    },
    "limit": {
      "type": "integer",
      "description": "Page size for 'list' (default 20, max 100)."
    },
    "offset": {
      "type": "integer",
      "description": "Offset for pagination in 'list'."
    }
  }
}`)

// memoryRecallSchema is the JSON Schema for the memory_recall tool input.
//
//nolint:gochecknoglobals // MCP tool schema must be a package-level var
var memoryRecallSchema = json.RawMessage(`{
  "type": "object",
  "required": ["query"],
  "properties": {
    "query": {
      "type": "string",
      "description": "Natural language query for semantic search. Used by 'semantic' and 'auto' strategies."
    },
    "strategy": {
      "type": "string",
      "description": "Retrieval strategy: entity, semantic, graph, auto. Defaults to 'auto'."
    },
    "entity_urns": {
      "type": "array",
      "items": {"type": "string"},
      "description": "DataHub URNs for 'entity' and 'graph' strategies."
    },
    "dimension": {
      "type": "string",
      "description": "Filter by LOCOMO dimension."
    },
    "include_stale": {
      "type": "boolean",
      "description": "Include stale memories in results. Defaults to false."
    },
    "limit": {
      "type": "integer",
      "description": "Maximum results (default 10, max 50)."
    }
  }
}`)
