---
description: REST API reference for knowledge management. Endpoints for listing, filtering, approving, and rejecting insights. Changeset management and rollback.
---

# Admin API

The Admin REST API provides HTTP endpoints for managing knowledge insights and changesets outside the MCP protocol. Use it for building dashboards, integrating with existing governance tools, or scripting batch operations.

## Authentication

All endpoints require admin authentication via API key. Pass the key as either:

- `X-API-Key: <key>` header
- `Authorization: Bearer <key>` header

The key must resolve to a user with the `admin` role. Requests without valid credentials receive `401 Unauthorized`. Requests with a valid key but no admin role receive `403 Forbidden`.

## Insight Endpoints

### List Insights

```
GET /api/v1/admin/knowledge/insights
```

Returns a paginated list of insights with optional filtering.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `status` | string | Filter by status: `pending`, `approved`, `rejected`, `applied`, `superseded`, `rolled_back` |
| `category` | string | Filter by category: `correction`, `business_context`, `data_quality`, `usage_guidance`, `relationship`, `enhancement` |
| `entity_urn` | string | Filter by related entity URN |
| `captured_by` | string | Filter by the user who captured the insight |
| `confidence` | string | Filter by confidence level: `high`, `medium`, `low` |
| `since` | RFC 3339 | Filter insights created after this timestamp |
| `until` | RFC 3339 | Filter insights created before this timestamp |
| `page` | integer | Page number, 1-based (default: 1) |
| `per_page` | integer | Results per page (default: 20, max: 100) |

**Example:**

```bash
curl -s "https://mcp.example.com/api/v1/admin/knowledge/insights?status=pending&category=correction" \
  -H "Authorization: Bearer $ADMIN_API_KEY" | jq
```

**Response:**

```json
{
  "insights": [
    {
      "id": "a1b2c3d4e5f67890a1b2c3d4e5f67890",
      "created_at": "2025-01-15T14:30:00Z",
      "session_id": "sess_abc123",
      "captured_by": "analyst@example.com",
      "persona": "analyst",
      "category": "correction",
      "insight_text": "The amount column represents gross margin before returns, not revenue.",
      "confidence": "high",
      "entity_urns": [
        "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.orders,PROD)"
      ],
      "related_columns": [],
      "suggested_actions": [
        {
          "action_type": "update_description",
          "target": "amount",
          "detail": "Gross margin before returns"
        }
      ],
      "status": "pending"
    }
  ],
  "total": 1,
  "page": 1,
  "per_page": 20
}
```

### Get Insight

```
GET /api/v1/admin/knowledge/insights/{id}
```

Returns a single insight by ID.

**Example:**

```bash
curl -s "https://mcp.example.com/api/v1/admin/knowledge/insights/a1b2c3d4e5f67890a1b2c3d4e5f67890" \
  -H "Authorization: Bearer $ADMIN_API_KEY" | jq
```

### Update Insight

```
PUT /api/v1/admin/knowledge/insights/{id}
```

Update the text, category, or confidence of an insight. Only `pending` insights can be edited.

**Request Body:**

```json
{
  "insight_text": "Updated description of the insight",
  "category": "business_context",
  "confidence": "high"
}
```

All fields are optional. Only provided fields are updated.

**Example:**

```bash
curl -X PUT "https://mcp.example.com/api/v1/admin/knowledge/insights/a1b2c3d4e5f67890" \
  -H "Authorization: Bearer $ADMIN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"confidence": "high"}'
```

### Update Insight Status

```
PUT /api/v1/admin/knowledge/insights/{id}/status
```

Approve or reject an insight.

**Request Body:**

```json
{
  "status": "approved",
  "review_notes": "Verified with data engineering team"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `status` | string | Yes | Target status: `approved` or `rejected` |
| `review_notes` | string | No | Notes explaining the decision |

**Valid Transitions:**

| From | To |
|------|----|
| `pending` | `approved` |
| `pending` | `rejected` |
| `pending` | `superseded` |

**Example:**

```bash
curl -X PUT "https://mcp.example.com/api/v1/admin/knowledge/insights/a1b2c3d4e5f67890/status" \
  -H "Authorization: Bearer $ADMIN_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"status": "approved", "review_notes": "Confirmed by data team"}'
```

### Get Insight Statistics

```
GET /api/v1/admin/knowledge/insights/stats
```

Returns aggregate statistics about insights.

**Example:**

```bash
curl -s "https://mcp.example.com/api/v1/admin/knowledge/insights/stats" \
  -H "Authorization: Bearer $ADMIN_API_KEY" | jq
```

**Response:**

```json
{
  "total_pending": 7,
  "by_entity": [
    {
      "entity_urn": "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.orders,PROD)",
      "count": 3,
      "categories": ["correction", "business_context"],
      "latest_at": "2025-01-15T14:30:00Z"
    }
  ],
  "by_category": {
    "correction": 5,
    "business_context": 3,
    "data_quality": 2
  },
  "by_confidence": {
    "high": 4,
    "medium": 5,
    "low": 1
  },
  "by_status": {
    "pending": 7,
    "approved": 3,
    "applied": 12,
    "rejected": 2
  }
}
```

## Changeset Endpoints

### List Changesets

```
GET /api/v1/admin/knowledge/changesets
```

Returns a paginated list of changesets with optional filtering.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `entity_urn` | string | Filter by target entity URN |
| `applied_by` | string | Filter by the user who applied the changes |
| `rolled_back` | boolean | Filter by rollback status (`true` or `false`) |
| `since` | RFC 3339 | Filter changesets created after this timestamp |
| `until` | RFC 3339 | Filter changesets created before this timestamp |
| `page` | integer | Page number, 1-based (default: 1) |
| `per_page` | integer | Results per page (default: 20, max: 100) |

**Example:**

```bash
curl -s "https://mcp.example.com/api/v1/admin/knowledge/changesets?rolled_back=false" \
  -H "Authorization: Bearer $ADMIN_API_KEY" | jq
```

**Response:**

```json
{
  "changesets": [
    {
      "id": "cs_x1y2z3a4b5c6d7e8f9a0b1c2d3e4f5a6",
      "created_at": "2025-01-15T16:00:00Z",
      "target_urn": "urn:li:dataset:(urn:li:dataPlatform:trino,hive.sales.orders,PROD)",
      "change_type": "update_description",
      "previous_value": {
        "description": "Order records",
        "tags": ["financial"],
        "glossary_terms": [],
        "owners": ["Data Platform Team"]
      },
      "new_value": {
        "change_0": {
          "change_type": "update_description",
          "target": "entity",
          "detail": "Order records with gross margin amounts (before returns)"
        }
      },
      "source_insight_ids": ["a1b2c3d4e5f67890"],
      "applied_by": "admin@example.com",
      "rolled_back": false
    }
  ],
  "total": 1,
  "page": 1,
  "per_page": 20
}
```

### Get Changeset

```
GET /api/v1/admin/knowledge/changesets/{id}
```

Returns a single changeset by ID.

**Example:**

```bash
curl -s "https://mcp.example.com/api/v1/admin/knowledge/changesets/cs_x1y2z3a4b5c6" \
  -H "Authorization: Bearer $ADMIN_API_KEY" | jq
```

### Rollback Changeset

```
POST /api/v1/admin/knowledge/changesets/{id}/rollback
```

Reverts a changeset by restoring the `previous_value` metadata to the DataHub entity.

**Example:**

```bash
curl -X POST "https://mcp.example.com/api/v1/admin/knowledge/changesets/cs_x1y2z3a4b5c6/rollback" \
  -H "Authorization: Bearer $ADMIN_API_KEY"
```

**Response:**

```json
{
  "changeset_id": "cs_x1y2z3a4b5c6",
  "rolled_back": true,
  "message": "Changeset rolled back. Previous metadata restored."
}
```

A changeset can only be rolled back once. Attempting to roll back an already-rolled-back changeset returns an error.

## Error Responses

All endpoints return errors in a consistent format:

```json
{
  "error": "insight not found",
  "code": "NOT_FOUND"
}
```

| HTTP Status | Code | Description |
|-------------|------|-------------|
| `400` | `BAD_REQUEST` | Invalid request parameters or body |
| `401` | `UNAUTHORIZED` | Missing or invalid authentication |
| `403` | `FORBIDDEN` | Authenticated but not authorized (not admin) |
| `404` | `NOT_FOUND` | Resource not found |
| `409` | `CONFLICT` | Invalid status transition or already rolled back |
| `500` | `INTERNAL_ERROR` | Server error |

## Database Schema

Knowledge capture uses two PostgreSQL tables, created by migrations 000006, 000007, and 000008.

### knowledge_insights

| Column | Type | Description |
|--------|------|-------------|
| `id` | TEXT | Primary key (cryptographic random hex) |
| `created_at` | TIMESTAMPTZ | When the insight was captured |
| `session_id` | TEXT | MCP session that produced the insight |
| `captured_by` | TEXT | User who shared the knowledge |
| `persona` | TEXT | Active persona at capture time |
| `category` | TEXT | Insight category |
| `insight_text` | TEXT | The domain knowledge content |
| `confidence` | TEXT | Confidence level (high, medium, low) |
| `entity_urns` | JSONB | Related DataHub entity URNs |
| `related_columns` | JSONB | Related columns |
| `suggested_actions` | JSONB | Proposed catalog changes |
| `status` | TEXT | Current lifecycle status |
| `reviewed_by` | TEXT | Who reviewed the insight |
| `reviewed_at` | TIMESTAMPTZ | When it was reviewed |
| `review_notes` | TEXT | Reviewer comments |
| `applied_by` | TEXT | Who applied the insight |
| `applied_at` | TIMESTAMPTZ | When it was applied |
| `changeset_ref` | TEXT | Link to the changeset |

### knowledge_changesets

| Column | Type | Description |
|--------|------|-------------|
| `id` | TEXT | Primary key (cryptographic random hex) |
| `created_at` | TIMESTAMPTZ | When changes were applied |
| `target_urn` | TEXT | DataHub entity that was modified |
| `change_type` | TEXT | Type of changes applied |
| `previous_value` | JSONB | Metadata before changes (for rollback) |
| `new_value` | JSONB | Changes applied |
| `source_insight_ids` | JSONB | Insights that produced this changeset |
| `approved_by` | TEXT | Who approved the changes |
| `applied_by` | TEXT | Who applied the changes |
| `rolled_back` | BOOLEAN | Whether changes were reverted |
| `rolled_back_by` | TEXT | Who reverted the changes |
| `rolled_back_at` | TIMESTAMPTZ | When changes were reverted |
