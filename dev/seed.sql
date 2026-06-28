-- ACME Corporation Seed Data
--
-- Run AFTER the Go server has started once (to create the schema via migrations).
-- Usage: docker exec acme-dev-postgres psql -U platform -d mcp_platform -f /tmp/seed.sql
--
-- All inserts use ON CONFLICT upserts so this file can be re-run safely.
-- Demo data gets reset to latest; user-created data is preserved.
--
-- Seeds:
--   ~5,000 audit events over the past 7 days
--   50 fresh audit events within the last hour (new each restart)
--   8 knowledge insights in various states
--   2 knowledge changesets (1 applied, 1 rolled back)
--   6 portal assets with versions and shares
--   3 portal collections with sections, items, and a public share

-- ============================================================================
-- Audit Events (~5,000 over 7 days)
-- Delete seed events and re-insert so timestamps are always relative to NOW().
-- ============================================================================

DELETE FROM audit_logs WHERE id LIKE 'evt-%' OR id LIKE 'ak-%' OR id LIKE 'fr-%';

INSERT INTO audit_logs (
  id, request_id, session_id, user_id, user_email, persona,
  tool_name, toolkit_kind, toolkit_name, connection,
  parameters, success, error_message,
  duration_ms, response_chars, request_chars, content_blocks,
  transport, source, enrichment_applied, authorized,
  "timestamp", created_date
)
SELECT
  'evt-' || lpad(n::text, 8, '0'),
  'req-' || lpad(n::text, 8, '0'),
  'sess-' || ((n % 50) + 100),
  u.email,
  u.email,
  u.persona,
  t.tool_name,
  t.toolkit_kind,
  t.toolkit_name,
  t.connection,
  CASE t.toolkit_kind
    WHEN 'trino' THEN jsonb_build_object(
      'catalog', (ARRAY['iceberg','hive','memory'])[1 + (n % 3)],
      'schema', (ARRAY['retail','inventory','finance','analytics','staging'])[1 + (n % 5)],
      'table', (ARRAY['daily_sales','store_transactions','inventory_levels','product_catalog','regional_performance'])[1 + (n % 5)]
    )
    WHEN 'datahub' THEN jsonb_build_object(
      'query', (ARRAY['daily_sales','inventory','customer','revenue','store performance','supply chain'])[1 + (n % 6)]
    )
    WHEN 's3' THEN jsonb_build_object(
      'bucket', (ARRAY['acme-raw-transactions','acme-analytics-output','acme-ml-features','acme-report-archive'])[1 + (n % 4)],
      'prefix', (ARRAY['raw/2024/','processed/daily/','exports/regional/','ml/features/'])[1 + (n % 4)]
    )
  END,
  (n % 27) != 0,
  CASE WHEN (n % 27) = 0 THEN
    CASE t.toolkit_kind
      WHEN 'trino' THEN (ARRAY[
        'Query exceeded maximum execution time of 300s',
        'Query failed: Table does not exist',
        'Insufficient permissions on schema ''finance''',
        'Memory limit exceeded: 2.1GB of 2.0GB'
      ])[1 + (n % 4)]
      WHEN 'datahub' THEN (ARRAY[
        'Entity not found for URN',
        'Lineage depth exceeded maximum of 5 hops',
        'Search index temporarily unavailable',
        'GraphQL query timeout after 30s'
      ])[1 + (n % 4)]
      WHEN 's3' THEN (ARRAY[
        'NoSuchKey: The specified key does not exist',
        'AccessDenied: User does not have s3:GetObject permission',
        'NoSuchBucket: The specified bucket does not exist'
      ])[1 + (n % 3)]
    END
  ELSE '' END,
  CASE t.toolkit_kind
    WHEN 'trino' THEN 45 + (n * 7 % 2755)
    WHEN 'datahub' THEN 25 + (n * 3 % 325)
    WHEN 's3' THEN 15 + (n * 5 % 485)
  END,
  200 + (n * 13 % 11800),
  50 + (n * 7 % 750),
  1 + (n % 5),
  'http',
  'mcp',
  (n % 5) != 0,
  true,
  -- timestamp: business-hours weighted over past 7 days
  NOW() - (
    ((n % 7) || ' days')::interval
    + (CASE
        WHEN n % 20 < 1  THEN (n % 3)
        WHEN n % 20 < 2  THEN 6 + (n % 2)
        WHEN n % 20 < 5  THEN 8 + (n % 2)
        WHEN n % 20 < 9  THEN 9 + (n % 3)
        WHEN n % 20 < 11 THEN 11 + (n % 2)
        WHEN n % 20 < 15 THEN 13 + (n % 3)
        WHEN n % 20 < 18 THEN 14 + (n % 3)
        ELSE 17 + (n % 3)
      END || ' hours')::interval
    + ((n * 17 % 60) || ' minutes')::interval
    + ((n * 31 % 60) || ' seconds')::interval
  ),
  -- created_date: matches the date portion of timestamp
  (NOW() - ((n % 7) || ' days')::interval)::date
FROM
  generate_series(1, 5000) AS n,
  LATERAL (
    SELECT email, persona FROM (VALUES
      ('sarah.chen@example.com',       'admin',              8),
      ('marcus.johnson@example.com',   'data-engineer',     15),
      ('rachel.thompson@example.com',  'inventory-analyst', 12),
      ('david.park@example.com',       'regional-director',  6),
      ('jennifer.martinez@example.com','finance-executive',   5),
      ('kevin.wilson@example.com',     'store-manager',       7),
      ('amanda.lee@example.com',       'data-engineer',      14),
      ('carlos.rodriguez@example.com', 'regional-director',   6),
      ('emily.watson@example.com',     'inventory-analyst',  10),
      ('brian.taylor@example.com',     'finance-executive',   4),
      ('lisa.chang@example.com',       'data-engineer',      11),
      ('mike.davis@example.com',       'store-manager',       3)
    ) AS users(email, persona, weight)
    ORDER BY weight DESC
    OFFSET (n * 7 % 12)
    LIMIT 1
  ) AS u,
  LATERAL (
    SELECT tool_name, toolkit_kind, toolkit_name, connection FROM (VALUES
      ('trino_query',              'trino',   'acme-warehouse',        'acme-warehouse',         22),
      ('trino_describe_table',     'trino',   'acme-warehouse',        'acme-warehouse',          7),
      ('trino_browse',             'trino',   'acme-warehouse',        'acme-warehouse',          3),
      ('datahub_search',           'datahub', 'acme-catalog',          'acme-catalog',           12),
      ('datahub_get_entity',       'datahub', 'acme-catalog',          'acme-catalog',            6),
      ('datahub_get_schema',       'datahub', 'acme-catalog',          'acme-catalog',            3),
      ('datahub_get_lineage',      'datahub', 'acme-catalog',          'acme-catalog',            2),
      ('s3_list_objects',          's3',      'acme-data-lake',        'acme-data-lake',          4),
      ('s3_get_object',            's3',      'acme-data-lake',        'acme-data-lake',          3),
      ('s3_list_objects',          's3',      'acme-reports',          'acme-reports',             2),
      ('trino_query',              'trino',   'acme-staging',          'acme-staging',            4),
      ('datahub_search',           'datahub', 'acme-catalog-staging',  'acme-catalog-staging',    2)
    ) AS tools(tool_name, toolkit_kind, toolkit_name, connection, weight)
    ORDER BY weight DESC
    OFFSET (n * 13 % 12)
    LIMIT 1
  ) AS t;

-- ============================================================================
-- Dev API Key User Activity (apikey:admin / admin@apikey.local)
-- Generates events for the user associated with the acme-dev-key-2024 API key
-- so the Activity dashboard and My Activity page show data when logged in.
-- Uses gen_random_uuid() so each restart adds unique events spread over 7 days.
-- ============================================================================

INSERT INTO audit_logs (
  id, request_id, session_id, user_id, user_email, persona,
  tool_name, toolkit_kind, toolkit_name, connection,
  parameters, success, error_message,
  duration_ms, response_chars, request_chars, content_blocks,
  transport, source, enrichment_applied, authorized,
  "timestamp", created_date
)
SELECT
  'ak-' || lpad(n::text, 4, '0') || '-' || to_char(NOW(), 'MMDDHH24MI'),
  'rq-' || lpad(n::text, 4, '0') || '-' || to_char(NOW(), 'MMDDHH24MI'),
  'sa-' || (n % 10),
  'apikey:admin',
  'admin@apikey.local',
  'admin',
  t.tool_name,
  t.toolkit_kind,
  t.toolkit_name,
  t.connection,
  CASE t.toolkit_kind
    WHEN 'trino' THEN jsonb_build_object(
      'catalog', (ARRAY['iceberg','hive','memory'])[1 + (n % 3)],
      'schema', (ARRAY['retail','inventory','finance','analytics'])[1 + (n % 4)],
      'table', (ARRAY['daily_sales','store_transactions','inventory_levels','product_catalog'])[1 + (n % 4)]
    )
    WHEN 'datahub' THEN jsonb_build_object(
      'query', (ARRAY['daily_sales','inventory','customer','revenue','store performance'])[1 + (n % 5)]
    )
    WHEN 's3' THEN jsonb_build_object(
      'bucket', (ARRAY['acme-raw-transactions','acme-analytics-output','acme-ml-features'])[1 + (n % 3)],
      'prefix', (ARRAY['raw/2024/','processed/daily/','exports/regional/'])[1 + (n % 3)]
    )
  END,
  (n % 20) != 0,
  CASE WHEN (n % 20) = 0 THEN 'query timeout after 30s' ELSE '' END,
  20 + (n * 11 % 800),
  80 + (n * 17 % 6000),
  40 + (n * 7 % 500),
  1 + (n % 3),
  'http',
  'mcp',
  (n % 2) = 0,
  true,
  NOW() - ((n * 97 % 10080) || ' minutes')::interval - ((n * 13 % 60) || ' seconds')::interval,
  (NOW() - ((n * 97 % 10080) || ' minutes')::interval)::date
FROM
  generate_series(1, 80) AS n,
  LATERAL (
    SELECT tool_name, toolkit_kind, toolkit_name, connection FROM (VALUES
      ('trino_query',          'trino',   'acme-warehouse', 'acme-warehouse'),
      ('trino_describe_table', 'trino',   'acme-warehouse', 'acme-warehouse'),
      ('trino_browse',         'trino',   'acme-warehouse', 'acme-warehouse'),
      ('datahub_search',       'datahub', 'acme-catalog',   'acme-catalog'),
      ('datahub_get_entity',   'datahub', 'acme-catalog',   'acme-catalog'),
      ('datahub_get_schema',   'datahub', 'acme-catalog',   'acme-catalog'),
      ('s3_list_objects',      's3',      'acme-data-lake',  'acme-data-lake'),
      ('s3_get_object',        's3',      'acme-data-lake',  'acme-data-lake')
    ) AS tools(tool_name, toolkit_kind, toolkit_name, connection)
    OFFSET (n % 8)
    LIMIT 1
  ) AS t;

-- ============================================================================
-- Fresh Activity (50 new events each restart, simulating recent usage)
-- Uses gen_random_uuid() so each restart adds unique events.
-- ============================================================================

INSERT INTO audit_logs (
  id, request_id, session_id, user_id, user_email, persona,
  tool_name, toolkit_kind, toolkit_name, connection,
  parameters, success, error_message,
  duration_ms, response_chars, request_chars, content_blocks,
  transport, source, enrichment_applied, authorized,
  "timestamp", created_date
)
SELECT
  'fr-' || lpad(n::text, 4, '0') || '-' || to_char(NOW(), 'MMDDHH24MI'),
  'rq-' || lpad(n::text, 4, '0') || '-' || to_char(NOW(), 'MMDDHH24MI'),
  'sess-' || ((n % 20) + 200),
  u.email,
  u.email,
  u.persona,
  t.tool_name,
  t.toolkit_kind,
  t.toolkit_name,
  t.connection,
  CASE t.toolkit_kind
    WHEN 'trino' THEN jsonb_build_object(
      'catalog', (ARRAY['iceberg','hive','memory'])[1 + (n % 3)],
      'schema', (ARRAY['retail','inventory','finance','analytics','staging'])[1 + (n % 5)],
      'table', (ARRAY['daily_sales','store_transactions','inventory_levels','product_catalog','regional_performance'])[1 + (n % 5)]
    )
    WHEN 'datahub' THEN jsonb_build_object(
      'query', (ARRAY['daily_sales','inventory','customer','revenue','store performance','supply chain'])[1 + (n % 6)]
    )
    WHEN 's3' THEN jsonb_build_object(
      'bucket', (ARRAY['acme-raw-transactions','acme-analytics-output','acme-ml-features','acme-report-archive'])[1 + (n % 4)],
      'prefix', (ARRAY['raw/2024/','processed/daily/','exports/regional/','ml/features/'])[1 + (n % 4)]
    )
  END,
  true,
  '',
  30 + (n * 7 % 500),
  100 + (n * 13 % 5000),
  50 + (n * 7 % 400),
  1 + (n % 4),
  'http',
  'mcp',
  (n % 3) != 0,
  true,
  NOW() - ((n * 3 % 60) || ' minutes')::interval - ((n * 17 % 60) || ' seconds')::interval,
  CURRENT_DATE
FROM
  generate_series(1, 50) AS n,
  LATERAL (
    SELECT email, persona FROM (VALUES
      ('sarah.chen@example.com',       'admin'),
      ('marcus.johnson@example.com',   'data-engineer'),
      ('rachel.thompson@example.com',  'inventory-analyst'),
      ('david.park@example.com',       'regional-director'),
      ('amanda.lee@example.com',       'data-engineer'),
      ('lisa.chang@example.com',       'data-engineer')
    ) AS users(email, persona)
    OFFSET (n % 6)
    LIMIT 1
  ) AS u,
  LATERAL (
    SELECT tool_name, toolkit_kind, toolkit_name, connection FROM (VALUES
      ('trino_query',          'trino',   'acme-warehouse', 'acme-warehouse'),
      ('trino_describe_table', 'trino',   'acme-warehouse', 'acme-warehouse'),
      ('datahub_search',       'datahub', 'acme-catalog',   'acme-catalog'),
      ('datahub_get_entity',   'datahub', 'acme-catalog',   'acme-catalog'),
      ('s3_list_objects',      's3',      'acme-data-lake',  'acme-data-lake'),
      ('s3_get_object',        's3',      'acme-data-lake',  'acme-data-lake')
    ) AS tools(tool_name, toolkit_kind, toolkit_name, connection)
    OFFSET (n % 6)
    LIMIT 1
  ) AS t;

-- The Dashboard's MCP tab filters audit events by event_kind
-- ('mcp_tool_call'). Migration 000048 backfills rows present at migration
-- time, but this seed runs afterward, so set it here. Derive from
-- toolkit_kind to mirror the platform's write-time logic (#465):
-- apigateway invocations are apigateway_invoke, everything else is an MCP
-- tool call. Without this the seeded rows have a NULL event_kind and are
-- invisible to the MCP-scoped views.
UPDATE audit_logs
SET event_kind = CASE WHEN toolkit_kind = 'api' THEN 'apigateway_invoke' ELSE 'mcp_tool_call' END
WHERE event_kind IS NULL;

-- ============================================================================
-- Knowledge Insights (8 in various states)
-- ============================================================================

-- knowledge_insights was dropped in migration 31, which folded it into the
-- universal memory_records table (dimension='knowledge'). Seed memory_records
-- directly, applying the same field mapping that migration's data-migration used,
-- so the row data below can stay as-is in the VALUES list.
INSERT INTO memory_records (
  id, created_by, persona, dimension, category, content, confidence, source,
  entity_urns, related_columns, metadata, status, created_at, updated_at
)
SELECT
  v.id, v.captured_by, v.persona, 'knowledge', v.category, v.insight_text,
  v.confidence, 'user', v.entity_urns::jsonb, v.related_columns::jsonb,
  jsonb_build_object(
    'session_id', v.session_id, 'legacy_status', v.status,
    'suggested_actions', v.suggested_actions::jsonb,
    'reviewed_by', v.reviewed_by, 'review_notes', v.review_notes,
    'applied_by', v.applied_by, 'changeset_ref', v.changeset_ref),
  CASE WHEN v.status IN ('rejected', 'rolled_back') THEN 'archived'
       WHEN v.status = 'superseded' THEN 'superseded'
       ELSE 'active' END,
  v.created_at, v.created_at
FROM (VALUES
(
  'ins-001', 'sess-101', 'marcus.johnson@example.com', 'data-engineer',
  'business_context',
  'The daily_sales table in the retail schema is the primary source of truth for all revenue reporting. The revenue column uses post-discount, pre-tax amounts in USD.',
  'high',
  '["urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"]'::jsonb,
  '[{"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)", "column": "revenue", "relevance": "primary"}]'::jsonb,
  '[{"action_type": "update_description", "target": "iceberg.retail.daily_sales.revenue", "detail": "Post-discount, pre-tax revenue in USD"}]'::jsonb,
  'approved', 'sarah.chen@example.com', 'Verified with finance team - this is correct.', '', '',
  NOW() - interval '5 days'
),
(
  'ins-002', 'sess-102', 'rachel.thompson@example.com', 'inventory-analyst',
  'data_quality',
  'The inventory_levels table has NULL values in the reorder_point column for approximately 12% of SKUs, primarily in the seasonal-goods category.',
  'high',
  '["urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.inventory.inventory_levels,PROD)"]'::jsonb,
  '[{"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.inventory.inventory_levels,PROD)", "column": "reorder_point", "relevance": "primary"}]'::jsonb,
  '[{"action_type": "add_tag", "target": "iceberg.inventory.inventory_levels.reorder_point", "detail": "has_nulls_seasonal"}]'::jsonb,
  'approved', 'sarah.chen@example.com', 'Known issue - seasonal items have dynamic reorder points calculated separately.', '', '',
  NOW() - interval '4 days'
),
(
  'ins-003', 'sess-103', 'amanda.lee@example.com', 'data-engineer',
  'usage_guidance',
  'When querying regional_performance, always filter by fiscal_quarter rather than calendar_quarter. ACME fiscal year starts February 1.',
  'high',
  '["urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.analytics.regional_performance,PROD)"]'::jsonb,
  '[]'::jsonb,
  '[{"action_type": "update_description", "target": "iceberg.analytics.regional_performance", "detail": "Uses ACME fiscal calendar (FY starts Feb 1). Filter by fiscal_quarter for accurate period comparisons."}]'::jsonb,
  'applied', 'sarah.chen@example.com', 'Applied to DataHub description.', 'sarah.chen@example.com', 'cs-001',
  NOW() - interval '3 days'
),
(
  'ins-004', 'sess-104', 'lisa.chang@example.com', 'data-engineer',
  'relationship',
  'The customer_segments table is refreshed daily from the ML pipeline and feeds into regional_performance via a nightly ETL job. Segments are based on 90-day purchase history.',
  'medium',
  '["urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.analytics.customer_segments,PROD)", "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.analytics.regional_performance,PROD)"]'::jsonb,
  '[]'::jsonb,
  '[]'::jsonb,
  'pending', '', '', '', '',
  NOW() - interval '2 days'
),
(
  'ins-005', 'sess-105', 'marcus.johnson@example.com', 'data-engineer',
  'correction',
  'The store_transactions.discount_pct column is stored as a decimal (0.15 = 15%), not as a percentage integer. Several downstream reports incorrectly multiply by 100.',
  'high',
  '["urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.store_transactions,PROD)"]'::jsonb,
  '[{"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.store_transactions,PROD)", "column": "discount_pct", "relevance": "primary"}]'::jsonb,
  '[{"action_type": "update_description", "target": "iceberg.retail.store_transactions.discount_pct", "detail": "Decimal format: 0.15 means 15% discount. Do NOT multiply by 100."}]'::jsonb,
  'pending', '', '', '', '',
  NOW() - interval '1 day'
),
(
  'ins-006', 'sess-106', 'rachel.thompson@example.com', 'inventory-analyst',
  'business_context',
  'Supply chain orders with status BACKORDER should be excluded from current inventory calculations. They represent items ordered but not yet received from vendors.',
  'high',
  '["urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.inventory.supply_chain_orders,PROD)"]'::jsonb,
  '[{"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.inventory.supply_chain_orders,PROD)", "column": "status", "relevance": "primary"}]'::jsonb,
  '[]'::jsonb,
  'applied', 'sarah.chen@example.com', 'Added as a data quality rule in the enrichment layer.', 'sarah.chen@example.com', 'cs-002',
  NOW() - interval '6 days'
),
(
  'ins-007', 'sess-107', 'emily.watson@example.com', 'inventory-analyst',
  'data_quality',
  'The price_adjustments table contains duplicate entries for Black Friday 2024 promotions (Nov 29). Use DISTINCT on (store_id, sku, effective_date) to deduplicate.',
  'medium',
  '["urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.price_adjustments,PROD)"]'::jsonb,
  '[]'::jsonb,
  '[{"action_type": "add_tag", "target": "iceberg.retail.price_adjustments", "detail": "has_duplicates_bf2024"}]'::jsonb,
  'rejected', 'sarah.chen@example.com', 'Duplicates were intentional - separate promotional line items for stackable discounts.', '', '',
  NOW() - interval '5 days'
),
(
  'ins-008', 'sess-108', 'amanda.lee@example.com', 'data-engineer',
  'usage_guidance',
  'For real-time inventory queries, use the inventory.current_stock view instead of inventory_levels. The view joins with pending_receipts and in_transit tables for accurate on-hand counts.',
  'high',
  '["urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.inventory.inventory_levels,PROD)"]'::jsonb,
  '[]'::jsonb,
  '[]'::jsonb,
  'pending', '', '', '', '',
  NOW() - interval '12 hours'
)
) AS v(
  id, session_id, captured_by, persona, category, insight_text, confidence,
  entity_urns, related_columns, suggested_actions, status, reviewed_by,
  review_notes, applied_by, changeset_ref, created_at
)
ON CONFLICT (id) DO NOTHING;

-- ============================================================================
-- Knowledge Changesets (2: 1 applied, 1 rolled back)
-- ============================================================================

INSERT INTO knowledge_changesets (
  id, target_urn, change_type, previous_value, new_value,
  source_insight_ids, approved_by, applied_by,
  rolled_back, rolled_back_by,
  created_at
) VALUES
(
  'cs-001',
  'urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.analytics.regional_performance,PROD)',
  'update_description',
  '{"description": ""}'::jsonb,
  '{"description": "Uses ACME fiscal calendar (FY starts Feb 1). Filter by fiscal_quarter for accurate period comparisons."}'::jsonb,
  '["ins-003"]'::jsonb,
  'sarah.chen@example.com', 'sarah.chen@example.com',
  false, '',
  NOW() - interval '3 days'
),
(
  'cs-002',
  'urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.inventory.supply_chain_orders,PROD)',
  'add_tag',
  '{}'::jsonb,
  '{"tag": "exclude_backorders_from_inventory"}'::jsonb,
  '["ins-006"]'::jsonb,
  'sarah.chen@example.com', 'sarah.chen@example.com',
  true, 'sarah.chen@example.com',
  NOW() - interval '6 days'
)
ON CONFLICT (id) DO UPDATE SET
  change_type = EXCLUDED.change_type,
  previous_value = EXCLUDED.previous_value,
  new_value = EXCLUDED.new_value,
  rolled_back = EXCLUDED.rolled_back;

-- ============================================================================
-- Portal Assets (6 assets across different users and content types)
-- ============================================================================

INSERT INTO portal_assets (
  id, owner_id, owner_email, name, description, content_type,
  s3_bucket, s3_key, size_bytes, tags, provenance, session_id,
  current_version, created_at, updated_at
) VALUES
(
  'asset-001', 'apikey:admin', 'admin@apikey.local',
  'Weekly Revenue Dashboard',
  'Interactive dashboard showing revenue trends, top stores, and category breakdown for the current week.',
  'text/html',
  'portal-assets', 'portal/apikey:admin/asset-001/v1/content.html', 4280,
  '["dashboard", "revenue", "weekly"]'::jsonb,
  '{"tool": "save_artifact", "session_id": "sess-201"}'::jsonb,
  'sess-201', 1,
  NOW() - interval '6 days', NOW() - interval '6 days'
),
(
  'asset-002', 'apikey:admin', 'admin@apikey.local',
  'Inventory Health Report',
  'CSV export of current inventory levels with reorder alerts and stock-out risk scores.',
  'text/csv',
  'portal-assets', 'portal/apikey:admin/asset-002/v1/content.csv', 15720,
  '["inventory", "report", "csv"]'::jsonb,
  '{"tool": "save_artifact", "session_id": "sess-202"}'::jsonb,
  'sess-202', 1,
  NOW() - interval '5 days', NOW() - interval '5 days'
),
(
  'asset-003', 'apikey:admin', 'admin@apikey.local',
  'Store Performance Comparison',
  'Side-by-side comparison of top 10 stores by revenue, foot traffic, and conversion rate.',
  'text/jsx',
  'portal-assets', 'portal/apikey:admin/asset-003/v1/content.jsx', 6340,
  '["stores", "comparison", "interactive"]'::jsonb,
  '{"tool": "save_artifact", "session_id": "sess-203"}'::jsonb,
  'sess-203', 1,
  NOW() - interval '4 days', NOW() - interval '4 days'
),
(
  'asset-004', 'apikey:admin', 'admin@apikey.local',
  'Data Pipeline Architecture',
  'Mermaid diagram showing the complete data flow from source systems through the warehouse to analytics.',
  'text/markdown',
  'portal-assets', 'portal/apikey:admin/asset-004/v1/content.md', 2890,
  '["architecture", "documentation", "pipeline"]'::jsonb,
  '{"tool": "save_artifact", "session_id": "sess-204"}'::jsonb,
  'sess-204', 1,
  NOW() - interval '3 days', NOW() - interval '3 days'
),
(
  'asset-005', 'apikey:admin', 'admin@apikey.local',
  'Regional Sales Heatmap',
  'SVG heatmap visualization of sales performance by region and product category.',
  'image/svg+xml',
  'portal-assets', 'portal/apikey:admin/asset-005/v1/content.svg', 8150,
  '["visualization", "sales", "regional"]'::jsonb,
  '{"tool": "save_artifact", "session_id": "sess-205"}'::jsonb,
  'sess-205', 1,
  NOW() - interval '2 days', NOW() - interval '2 days'
),
(
  'asset-006', 'apikey:admin', 'admin@apikey.local',
  'Q3 Financial Summary',
  'Quarterly financial summary with revenue, margins, and year-over-year comparisons.',
  'text/html',
  'portal-assets', 'portal/apikey:admin/asset-006/v1/content.html', 5420,
  '["finance", "quarterly", "dashboard"]'::jsonb,
  '{"tool": "save_artifact", "session_id": "sess-206"}'::jsonb,
  'sess-206', 1,
  NOW() - interval '1 day', NOW() - interval '1 day'
)
ON CONFLICT (id) DO UPDATE SET
  name = EXCLUDED.name,
  description = EXCLUDED.description,
  content_type = EXCLUDED.content_type,
  tags = EXCLUDED.tags;

-- Asset versions (one per asset, matching the current_version=1)
INSERT INTO portal_asset_versions (
  id, asset_id, version, s3_key, s3_bucket, content_type, size_bytes,
  created_by, change_summary, created_at
) VALUES
('ver-001', 'asset-001', 1, 'portal/apikey:admin/asset-001/v1/content.html', 'portal-assets', 'text/html',     4280, 'apikey:admin', 'Initial version', NOW() - interval '6 days'),
('ver-002', 'asset-002', 1, 'portal/apikey:admin/asset-002/v1/content.csv',  'portal-assets', 'text/csv',     15720, 'apikey:admin', 'Initial version', NOW() - interval '5 days'),
('ver-003', 'asset-003', 1, 'portal/apikey:admin/asset-003/v1/content.jsx',  'portal-assets', 'text/jsx',      6340, 'apikey:admin', 'Initial version', NOW() - interval '4 days'),
('ver-004', 'asset-004', 1, 'portal/apikey:admin/asset-004/v1/content.md',   'portal-assets', 'text/markdown', 2890, 'apikey:admin', 'Initial version', NOW() - interval '3 days'),
('ver-005', 'asset-005', 1, 'portal/apikey:admin/asset-005/v1/content.svg',  'portal-assets', 'image/svg+xml', 8150, 'apikey:admin', 'Initial version', NOW() - interval '2 days'),
('ver-006', 'asset-006', 1, 'portal/apikey:admin/asset-006/v1/content.html', 'portal-assets', 'text/html',     5420, 'apikey:admin', 'Initial version', NOW() - interval '1 day')
ON CONFLICT (id) DO NOTHING;

-- Shares (2 assets shared: one user share, one public link)
INSERT INTO portal_shares (
  id, asset_id, token, created_by, expires_at, created_at,
  shared_with_user_id, shared_with_email, permission
) VALUES
(
  'share-001', 'asset-001', 'tok-revenue-dash-public',
  'apikey:admin', NOW() + interval '30 days', NOW() - interval '5 days',
  NULL, NULL, 'viewer'
),
(
  'share-002', 'asset-002', 'tok-inventory-marcus',
  'apikey:admin', NULL, NOW() - interval '4 days',
  'apikey:admin', 'marcus.johnson@example.com', 'viewer'
)
ON CONFLICT (id) DO UPDATE SET
  expires_at = EXCLUDED.expires_at,
  permission = EXCLUDED.permission;

-- ============================================================================
-- Portal Collections (2 curated collections)
-- ============================================================================

INSERT INTO portal_collections (
  id, owner_id, owner_email, name, description, config,
  created_at, updated_at
) VALUES
(
  'coll-001', 'apikey:admin', 'admin@apikey.local',
  'Q3 2025 Executive Review',
  E'Comprehensive **quarterly business review** package prepared for the executive leadership team.\n\nThis collection brings together financial results, operational metrics, and strategic insights from Q3 2025. Each section focuses on a different aspect of business performance.\n\n> *"Data is the new oil, but only if you refine it."*\n\n### How to use this collection\n\n- Start with the **Financial Overview** for top-line results\n- Review **Operations & Inventory** for supply chain health\n- Finish with **Technical Architecture** for platform investment context',
  '{"thumbnail_size": "medium"}'::jsonb,
  NOW() - interval '2 days', NOW() - interval '2 days'
),
(
  'coll-002', 'apikey:admin', 'admin@apikey.local',
  'Regional Sales Deep Dive',
  E'A focused analysis of **regional sales performance** across all product categories and store locations.\n\nThis collection was assembled to support the upcoming regional managers meeting. It includes:\n\n1. Revenue dashboards with week-over-week trends\n2. Store-level comparisons and rankings\n3. Geographic heatmaps for visual pattern recognition\n\n---\n\n*Prepared by the Analytics team using the ACME Data Platform.*',
  '{"thumbnail_size": "large"}'::jsonb,
  NOW() - interval '1 day', NOW() - interval '1 day'
)
,
(
  'coll-003', 'apikey:admin', 'admin@apikey.local',
  '2025 Sales Insights',
  E'This is a demo collection. Bacon ipsum dolor amet ground round porchetta filet mignon turducken chicken hamburger tenderloin jowl jerky strip steak alcatra shoulder.\n\nA curated set of dashboards, reports, and analyses covering 2025 sales performance across all regions.',
  '{"thumbnail_size": "large"}'::jsonb,
  NOW() - interval '1 day', NOW() - interval '1 day'
)
ON CONFLICT (id) DO UPDATE SET
  name = EXCLUDED.name,
  description = EXCLUDED.description,
  config = EXCLUDED.config;

-- Collection sections with descriptions
INSERT INTO portal_collection_sections (
  id, collection_id, title, description, position, created_at
) VALUES
-- Collection 1: Q3 Executive Review
(
  'sec-001', 'coll-001',
  'Financial Overview',
  E'Top-line **financial results** for Q3 2025.\n\nKey metrics to watch:\n- Revenue vs. plan\n- Margin trends\n- Year-over-year growth rates',
  0, NOW() - interval '2 days'
),
(
  'sec-002', 'coll-001',
  'Operations & Inventory',
  E'Supply chain health and inventory management metrics.\n\nThis section highlights stock levels, reorder alerts, and warehouse utilization across all categories.',
  1, NOW() - interval '2 days'
),
(
  'sec-003', 'coll-001',
  'Technical Architecture',
  E'Overview of the **data platform architecture** powering these analytics.\n\n```\nSources → Ingestion → Warehouse → Serving → Dashboards\n```\n\nIncluded for context on platform investment and capabilities.',
  2, NOW() - interval '2 days'
),
-- Collection 2: Regional Sales Deep Dive
(
  'sec-004', 'coll-002',
  'Revenue Dashboards',
  E'Interactive revenue views showing **weekly trends** and category breakdowns.\n\nUse these to identify:\n- Which categories are driving growth\n- Week-over-week momentum shifts\n- Seasonal patterns emerging in Q3',
  0, NOW() - interval '1 day'
),
(
  'sec-005', 'coll-002',
  'Store Rankings & Comparisons',
  E'Head-to-head store performance analysis covering:\n\n| Metric | Description |\n|--------|-------------|\n| Revenue | Total sales volume |\n| Traffic | Foot traffic counts |\n| Conversion | Visit-to-purchase rate |\n\nStores are ranked by composite score.',
  1, NOW() - interval '1 day'
),
(
  'sec-006', 'coll-002',
  'Geographic Analysis',
  E'Visual **geographic breakdown** of sales by region and product category.\n\nThe heatmap reveals concentration patterns that are not obvious from tabular data alone.',
  2, NOW() - interval '1 day'
)
,
-- Collection 3: 2025 Sales Insights
(
  'sec-007', 'coll-003',
  'Sales Overview',
  E'Top-level revenue and performance dashboards for 2025.',
  0, NOW() - interval '1 day'
),
(
  'sec-008', 'coll-003',
  'Regional Breakdown',
  E'Regional performance analysis with geographic visualizations.',
  1, NOW() - interval '1 day'
)
ON CONFLICT (id) DO UPDATE SET
  title = EXCLUDED.title,
  description = EXCLUDED.description,
  position = EXCLUDED.position;

-- Collection items (assets assigned to sections)
INSERT INTO portal_collection_items (
  id, section_id, asset_id, position, created_at
) VALUES
-- Collection 1, Section 1: Financial Overview
('item-001', 'sec-001', 'asset-006', 0, NOW() - interval '2 days'),  -- Q3 Financial Summary
('item-002', 'sec-001', 'asset-001', 1, NOW() - interval '2 days'),  -- Weekly Revenue Dashboard
-- Collection 1, Section 2: Operations & Inventory
('item-003', 'sec-002', 'asset-002', 0, NOW() - interval '2 days'),  -- Inventory Health Report
-- Collection 1, Section 3: Technical Architecture
('item-004', 'sec-003', 'asset-004', 0, NOW() - interval '2 days'),  -- Data Pipeline Architecture
-- Collection 2, Section 1: Revenue Dashboards
('item-005', 'sec-004', 'asset-001', 0, NOW() - interval '1 day'),   -- Weekly Revenue Dashboard
('item-006', 'sec-004', 'asset-006', 1, NOW() - interval '1 day'),   -- Q3 Financial Summary
-- Collection 2, Section 2: Store Rankings
('item-007', 'sec-005', 'asset-003', 0, NOW() - interval '1 day'),   -- Store Performance Comparison
-- Collection 2, Section 3: Geographic Analysis
('item-008', 'sec-006', 'asset-005', 0, NOW() - interval '1 day'),   -- Regional Sales Heatmap
('item-009', 'sec-006', 'asset-002', 1, NOW() - interval '1 day'),   -- Inventory Health Report
-- Collection 3, Section 1: Sales Overview
('item-010', 'sec-007', 'asset-001', 0, NOW() - interval '1 day'),  -- Weekly Revenue Dashboard
('item-011', 'sec-007', 'asset-006', 1, NOW() - interval '1 day'),  -- Q3 Financial Summary
-- Collection 3, Section 2: Regional Breakdown
('item-012', 'sec-008', 'asset-005', 0, NOW() - interval '1 day'),  -- Regional Sales Heatmap
('item-013', 'sec-008', 'asset-003', 1, NOW() - interval '1 day')   -- Store Performance Comparison
ON CONFLICT (id) DO UPDATE SET
  section_id = EXCLUDED.section_id,
  asset_id = EXCLUDED.asset_id,
  position = EXCLUDED.position;

-- Share collection 1 with a public link
INSERT INTO portal_shares (
  id, collection_id, token, created_by, expires_at, created_at,
  shared_with_user_id, shared_with_email, permission
) VALUES
(
  'share-003', 'coll-001', 'tok-q3-exec-review-public',
  'apikey:admin', NOW() + interval '30 days', NOW() - interval '2 days',
  NULL, NULL, 'viewer'
)
ON CONFLICT (id) DO UPDATE SET
  expires_at = EXCLUDED.expires_at,
  permission = EXCLUDED.permission;

-- ============================================================================
-- Connection Instances (sample toolkit connections for the admin UI)
-- ============================================================================

INSERT INTO connection_instances (kind, name, config, description, created_by, updated_at) VALUES
(
  'trino', 'acme-warehouse',
  '{"host": "trino.acme.internal", "port": 443, "user": "platform_svc", "catalog": "iceberg", "ssl": true, "datahub_source_name": "trino"}'::jsonb,
  'Production Iceberg warehouse — retail, inventory, and finance data',
  'apikey:admin', NOW() - interval '30 days'
),
(
  'trino', 'acme-staging',
  '{"host": "trino-staging.acme.internal", "port": 8080, "user": "platform_svc", "catalog": "hive", "ssl": false, "datahub_source_name": "trino", "catalog_mapping": {"hive": "warehouse"}}'::jsonb,
  'Staging environment for testing queries before production',
  'apikey:admin', NOW() - interval '14 days'
),
(
  's3', 'acme-data-lake',
  '{"region": "us-east-1", "bucket_prefix": "acme-", "datahub_source_name": "s3"}'::jsonb,
  'Production data lake — raw ingestion, processed datasets, and ML features',
  'apikey:admin', NOW() - interval '30 days'
),
(
  's3', 'acme-reports',
  '{"region": "us-east-1", "bucket_prefix": "acme-reports-", "datahub_source_name": "s3"}'::jsonb,
  'Report archive — generated dashboards, exports, and scheduled reports',
  'apikey:admin', NOW() - interval '7 days'
)
ON CONFLICT (kind, name) DO UPDATE SET
  config = EXCLUDED.config,
  description = EXCLUDED.description;

-- ============================================================================
-- Prompts — seed data covering all three scopes
-- ============================================================================

INSERT INTO prompts (name, display_name, description, content, arguments, category, scope, personas, owner_email, source, enabled)
VALUES
-- Global prompts
(
  'weekly-inventory-summary',
  'Weekly Inventory Summary',
  'Generate a weekly inventory status report across all warehouses',
  E'Generate a comprehensive weekly inventory summary.\n\n1. Query current inventory levels across all warehouses\n2. Compare against last week''s levels to identify trends\n3. Highlight items below reorder thresholds\n4. Summarize by {region} if specified, otherwise show all regions\n5. Present findings in a clear table with week-over-week changes',
  '[{"name": "region", "description": "Filter by region (optional)", "required": false}]'::jsonb,
  'reporting', 'global', '{}', 'admin@acme.example.com', 'operator', true
),
(
  'supplier-performance-review',
  'Supplier Performance Review',
  'Analyze supplier delivery and quality metrics',
  E'Review supplier performance for {supplier}.\n\n1. Look up delivery history and on-time rates\n2. Check quality inspection results and defect rates\n3. Compare pricing against contract terms\n4. Summarize trends over the past quarter\n5. Flag any suppliers at risk of SLA violation',
  '[{"name": "supplier", "description": "Supplier name or ID to review", "required": true}]'::jsonb,
  'analysis', 'global', '{}', 'admin@acme.example.com', 'operator', true
),
(
  'data-quality-check',
  'Data Quality Check',
  'Run standard data quality checks against a dataset',
  E'Perform data quality checks on {dataset}.\n\n1. Check for null values in key columns\n2. Validate data types and format consistency\n3. Look for duplicate records\n4. Verify referential integrity with related tables\n5. Report anomalies and suggest remediation',
  '[{"name": "dataset", "description": "Dataset name or URN to check", "required": true}]'::jsonb,
  'data-quality', 'global', '{}', 'admin@acme.example.com', 'operator', true
),
-- Persona-scoped prompts
(
  'regional-sales-analysis',
  'Regional Sales Analysis',
  'Analyze sales performance by region with drill-down',
  E'Analyze sales data for {region}.\n\n1. Pull current quarter sales by store and product category\n2. Compare against targets and prior year\n3. Identify top and bottom performing stores\n4. Highlight seasonal trends and anomalies\n5. Create a summary dashboard with key metrics',
  '[{"name": "region", "description": "Which region to analyze", "required": true}]'::jsonb,
  'analysis', 'persona', '{regional-director,inventory-analyst}', 'admin@acme.example.com', 'operator', true
),
(
  'pipeline-health-check',
  'Pipeline Health Check',
  'Check the health of data pipelines and flag issues',
  E'Check data pipeline health for {pipeline}.\n\n1. Verify last successful run and current schedule\n2. Check for failed or delayed jobs\n3. Validate row counts against expected thresholds\n4. Review data freshness and staleness indicators\n5. List any upstream dependencies with issues',
  '[{"name": "pipeline", "description": "Pipeline name or pattern", "required": true}]'::jsonb,
  'operations', 'persona', '{data-engineer,admin}', 'admin@acme.example.com', 'operator', true
),
(
  'financial-reconciliation',
  'Financial Reconciliation',
  'Reconcile financial data across systems',
  E'Reconcile {account_type} data between source systems.\n\n1. Pull totals from primary and secondary sources\n2. Identify discrepancies above threshold\n3. Trace mismatches to specific transactions\n4. Generate reconciliation report with variance analysis',
  '[{"name": "account_type", "description": "Account type to reconcile (AR, AP, GL)", "required": true}]'::jsonb,
  'finance', 'persona', '{finance-executive}', 'admin@acme.example.com', 'operator', true
),
-- Personal prompts
(
  'my-daily-standup',
  'My Daily Standup',
  'Quick morning data check for store operations',
  E'Run my daily standup checks:\n\n1. Show yesterday''s sales totals vs target\n2. List any inventory alerts from overnight\n3. Check if scheduled reports completed successfully\n4. Highlight anything that needs immediate attention',
  '[]'::jsonb,
  'daily', 'personal', '{}', 'alice.chen@acme.example.com', 'operator', true
),
(
  'my-ad-hoc-analysis',
  'My Ad-Hoc Analysis Template',
  'Preferred template for ad-hoc data investigations',
  E'Investigate {question} using the following approach:\n\n1. Identify relevant datasets in the catalog\n2. Check data freshness and quality\n3. Write and run exploratory queries\n4. Summarize findings with supporting data\n5. Save results as a shareable asset if noteworthy',
  '[{"name": "question", "description": "The question to investigate", "required": true}]'::jsonb,
  'analysis', 'personal', '{}', 'bob.martinez@acme.example.com', 'operator', true
),
-- Long-form personal prompts for the dev admin (acme-dev-key-2024).
-- These exercise the prompt viewer's markdown rendering and arguments panel.
(
  'incident-retro',
  'Incident Retrospective',
  'Long-form retrospective template for production incidents. Pulls audit, query, and pipeline activity for the impact window and produces a blameless writeup with timeline, contributing factors, and follow-up actions.',
  E'# Incident Retrospective — {{incident_id}}\n\nYou are writing a **blameless retrospective** for incident **{{incident_id}}**.\nImpact window: **{{start_time}} → {{end_time}}** ({{timezone}}).\n\n## 1. Summary\n\nWrite 3–5 sentences a non-technical stakeholder would understand. Cover:\n\n- What broke, from the customer''s perspective.\n- When it started, when it was detected, when it was resolved.\n- Who was paged and who actually drove the resolution.\n- Severity ({{severity}}) and customer-visible impact.\n\n## 2. Timeline\n\nBuild a strict-chronological timeline of events between **{{start_time}}** and **{{end_time}}**. For each entry, include:\n\n| Time ({{timezone}}) | Event | Source | Operator |\n| --- | --- | --- | --- |\n\nSources to pull from:\n\n1. **Audit log** — every tool call by {{primary_operator}} and any teammates joining the incident channel.\n2. **Pipeline runs** — `etl_pipelines.runs` rows whose `updated_at` falls in the window, with their status transitions.\n3. **Trino query log** — top 20 long-running queries by user during the window.\n4. **DataHub deprecation events** — anything marked deprecated/restored in the window.\n\n> Cite each entry with the underlying record (audit_event_id, query_id, run_id). No paraphrasing without a citation.\n\n## 3. Contributing factors\n\nIdentify **at most 5** contributing factors. For each:\n\n- **What** — one-sentence description.\n- **Evidence** — direct links to log lines / query IDs / dashboards.\n- **Category** — one of: configuration, capacity, deploy, data, dependency, human-process.\n- **Counterfactual** — "if X had been different, this incident would have been …".\n\nReject any factor you cannot evidence. Better to list 2 well-evidenced factors than 5 speculative ones.\n\n## 4. What went well\n\nExplicit, named callouts. Examples:\n\n- The on-call rotation paged the right person within {{page_sla}} minutes.\n- The runbook for `{{affected_pipeline}}` matched reality and was followed.\n- Customer comms went out before any external escalation.\n\n## 5. Follow-up actions\n\nProduce a table with one row per action. Every row must have an owner and a due date — no "TBD".\n\n| # | Action | Owner | Due | Category |\n| --- | --- | --- | --- | --- |\n\nCategories: **prevent** (stop recurrence), **detect** (faster signal next time), **respond** (better playbook), **recover** (faster restore), **communicate** (clearer customer messaging).\n\n## 6. Open questions\n\nAnything you couldn''t determine from the available evidence — frame as a question, not an accusation.\nThese should be resolved during the retro meeting, not left in the document.\n\n---\n\n**Constraints**\n\n- Blameless: describe systems and decisions, never individuals'' competence.\n- Every factual claim must cite a record from one of the sources above.\n- If a source has no relevant records, say so explicitly — do not infer.\n- Output the entire writeup as Markdown so it can be pasted into the incidents wiki unedited.',
  '[
    {"name": "incident_id", "description": "Internal incident identifier (e.g. INC-2026-0418-01)", "required": true},
    {"name": "start_time", "description": "When impact began, ISO-8601 with offset", "required": true},
    {"name": "end_time", "description": "When impact ended, ISO-8601 with offset", "required": true},
    {"name": "timezone", "description": "Display timezone for the timeline (e.g. America/Los_Angeles)", "required": true},
    {"name": "severity", "description": "Severity tier (SEV1, SEV2, SEV3)", "required": true},
    {"name": "primary_operator", "description": "Email of the person who drove the response", "required": true},
    {"name": "affected_pipeline", "description": "Name of the pipeline most directly impacted", "required": false},
    {"name": "page_sla", "description": "Expected paging SLA in minutes", "required": false}
  ]'::jsonb,
  'incident-response', 'personal', '{}', 'admin@example.com', 'operator', true
),
(
  'weekly-business-review',
  'Weekly Business Review',
  'Full week-over-week business review covering revenue, operations, and data quality. Produces a publish-ready Markdown report with KPIs, anomaly callouts, and a one-paragraph executive summary at the top.',
  E'# Weekly Business Review — Week ending {{week_ending}}\n\nAudience: **{{audience}}** (e.g. exec staff, ops leadership).\nRegion scope: **{{region}}** (use `all` to roll up everything).\nComparison: this week vs. **{{compare_to}}** (`prior_week`, `prior_year`, or `plan`).\n\n> Lead with the answer. The executive summary at the top must stand on its own — assume only ~30% of readers scroll past it.\n\n## Executive summary\n\nOne paragraph, **at most 4 sentences**:\n\n1. The single most important number this week and its direction.\n2. The biggest positive surprise (with magnitude).\n3. The biggest negative surprise (with magnitude and what is being done about it).\n4. What you need from the audience this week.\n\n## Revenue scorecard\n\nPull from `finance.revenue_daily` for the {{region}} region.\n\n| Metric | This week | {{compare_to}} | Δ | Δ % | Status |\n| --- | --- | --- | --- | --- | --- |\n| Net revenue | | | | | |\n| Gross margin % | | | | | |\n| Average order value | | | | | |\n| Customer acquisition cost | | | | | |\n| Same-store sales growth | | | | | |\n\nStatus is one of: 🟢 on/above plan, 🟡 within {{warn_threshold}}% of plan, 🔴 outside threshold.\n\nIf a metric crosses 🔴, attach a 1-sentence root cause hypothesis grounded in the data, plus the audit_event_id or query_id you used to verify.\n\n## Operations scorecard\n\nPull from `ops.warehouse_metrics` and `ops.fulfillment_runs`.\n\n- On-time fulfillment rate (target ≥ {{otf_target}}%).\n- Inventory days-on-hand by category (call out anything over {{doh_alert}}).\n- Stockouts: SKU count and revenue exposure.\n- Top 5 SKUs by lost-sale risk.\n\nFor each operations metric outside its target, propose **one** concrete next step, with the owner and a 7-day-or-less deadline. No "investigate further" placeholders.\n\n## Data platform health\n\nThis is the section the audience tends to skip — keep it short and only flag what matters.\n\n- Pipeline success rate this week vs. trailing 8 weeks.\n- Any tables flagged `deprecated` in DataHub that still received queries.\n- Top 3 longest-running recurring queries (candidates for tuning).\n- Any audit log gaps (missing days, unusual operator activity).\n\n## Anomalies\n\nFor every metric where the week-over-week delta exceeds {{anomaly_sigma}} standard deviations of its trailing 12-week distribution, produce one block:\n\n```\nmetric:      <name>\nthis week:   <value>\ntrailing μ:  <value> (σ = <value>)\ndelta:       <value> (<σ count>σ)\nhypothesis:  <1-sentence explanation, grounded in the data>\nevidence:    <query_id or audit_event_id>\naction:      <owner — what — by when>\n```\n\nReject any block where you cannot fill in **evidence**. A real anomaly with no evidence is worse than no callout.\n\n## Asks for the audience\n\nA bulleted list. Each item is one decision you need from this audience this week, phrased as a question with options. If there are none, write "No asks this week."\n\n---\n\n**Style rules**\n\n- Round numbers to the precision a human would speak (`$4.2M`, not `$4,237,891.40`).\n- Percentages to one decimal (`+12.3%`, not `+12.34567%`).\n- All directional comparisons explicit (`+`/`-` signs, ✅/⚠️ for status).\n- No buzzwords (`leverage`, `synergy`, `mission-critical`). State the thing.\n- Output the entire report as a single Markdown document, ready to paste.',
  '[
    {"name": "week_ending", "description": "Sunday of the report week (YYYY-MM-DD)", "required": true},
    {"name": "region", "description": "Region code, or ''all'' for global rollup", "required": true},
    {"name": "audience", "description": "Audience descriptor (e.g. ''exec staff'', ''ops leadership'')", "required": true},
    {"name": "compare_to", "description": "Comparison baseline: prior_week, prior_year, or plan", "required": true},
    {"name": "warn_threshold", "description": "Yellow-zone band as a percent (default 5)", "required": false},
    {"name": "otf_target", "description": "On-time fulfillment target as a percent", "required": false},
    {"name": "doh_alert", "description": "Days-on-hand threshold that triggers a callout", "required": false},
    {"name": "anomaly_sigma", "description": "Sigma threshold for the anomalies section (default 2)", "required": false}
  ]'::jsonb,
  'reporting', 'personal', '{}', 'admin@example.com', 'operator', true
)
-- No column target: migration 59 (#558) replaced the plain unique(name) with two
-- partial unique indexes (shared vs personal), which a single ON CONFLICT (name)
-- can no longer match. Untargeted DO NOTHING catches either, keeping the seed
-- idempotent across both scopes.
ON CONFLICT DO NOTHING;

-- Seeded prompts insert as 'draft' (migration 59's default). Mark them approved
-- so they are served over MCP and picked up by semantic discovery (#557), which
-- only indexes approved prompts. Mirrors the 59 backfill of pre-existing rows.
UPDATE prompts
   SET status = 'approved', approved_at = NOW(), approved_by = 'admin@acme.example.com'
 WHERE source = 'operator' AND status = 'draft';

-- Knowledge pages (#634, migration 000070): canonical business/domain knowledge.
-- The dev seed previously created every content type EXCEPT these, so the
-- Knowledge hub's "Knowledge Pages" tab came up empty against the real backend.
--
-- These are a curated, tightly interlinked set (#709): each page is a realistic
-- multi-paragraph knowledge page whose references are woven INLINE into the prose
-- (cross-page links, dataset URNs, the warehouse connection, and one deliberately
-- broken ref), not dumped in a trailing list. The inline mentions form a connected
-- graph so the reference panel, inbound backlinks, and wiki-style click-through all
-- have material to traverse. NOTE: a direct SQL insert bypasses the handler's
-- inline-ref reconcile, so the matching knowledge_page_entity_refs rows below are
-- written by hand and MUST stay in step with the inline mentions in each body.
-- Embeddings are left NULL; the indexer/reconciler fills them, and content search
-- falls back to lexical until then. Idempotent.
--
-- This set intentionally diverges from the MSW mock (ui/src/mocks/data/
-- knowledgePages.ts), which still carries the older, larger fixture for the
-- component tests; sync it separately if the two need to match again.
INSERT INTO portal_knowledge_pages
  (id, slug, title, summary, body, tags, created_by, created_email, updated_by, current_version, created_at, updated_at)
VALUES
  ('kp-seed-1', 'fiscal-calendar', 'Fiscal Calendar',
   'The fiscal year starts in February, so every finance comparison is offset a month from the civil calendar. This page is the source of truth for fiscal quarter and week boundaries.',
   $md$# Fiscal Calendar

Our fiscal year starts in **February**, not January, so every period-over-period comparison in finance reporting is offset a month from the civil calendar. The four quarters are Q1 (February-April), Q2 (May-July), Q3 (August-October), and Q4 (November-January).

The fiscal quarter is the default time grain for revenue reporting. When the [Revenue Definition](mcp:knowledge_page:kp-seed-2) or [Net Revenue Definition](mcp:knowledge_page:kp-seed-9) page refers to "the current quarter", it means the fiscal quarter defined here. The same tagging is applied at load time to [daily_sales](urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)), so a row can be rolled up to its fiscal period without a date-math join.

Two gotchas catch people every year. January belongs to the **previous** fiscal year's Q4, and fiscal-week numbering resets in February rather than at the civil new year. (Historically the calendar dimension was sourced from the [retired warehouse connection](mcp:connection:(trino,warehouse)); it now ships with the platform, so that link no longer resolves.)

For the canonical definitions of the metrics that sit on top of these periods, see the [Glossary: Core Terms](mcp:knowledge_page:kp-seed-28).
$md$,
   '["finance","calendar"]'::jsonb, 'sarah.chen@example.com', 'sarah.chen@example.com', 'sarah.chen@example.com', 2, '2026-06-01T10:00:00Z', '2026-06-10T12:00:00Z'),

  ('kp-seed-2', 'revenue-definition', 'Revenue Definition',
   'The amount column is gross margin before returns, not gross revenue. Use net_revenue for any top-line or board-level figure.',
   $md$# Revenue Definition

The `amount` column on the sales fact is **gross margin before returns**, not gross revenue. It is the most misread field in the warehouse: it already nets cost of goods, but it does **not** net returns, discounts, or tax.

For any top-line or board-level number, use net_revenue instead, defined on the [Net Revenue Definition](mcp:knowledge_page:kp-seed-9) page. The two differ by exactly the returns described in [Returns and Refunds Logic](mcp:knowledge_page:kp-seed-5), which post in a later layer rather than at ingest.

The field lives on [daily_sales](urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)) at store-day grain. When a report and the dashboard disagree, it is almost always because one used `amount` where it meant net_revenue. The [Glossary: Core Terms](mcp:knowledge_page:kp-seed-28) page is the tie-breaker when a metric name is ambiguous.
$md$,
   '["finance","metrics"]'::jsonb, 'sarah.chen@example.com', 'sarah.chen@example.com', 'sarah.chen@example.com', 1, '2026-06-05T09:00:00Z', '2026-06-18T14:30:00Z'),

  ('kp-seed-3', 'customer-pii-handling', 'Customer PII Handling',
   'email, phone, and address are PII. They must never be joined into a shared mart unmasked, and unmasked access is gated and logged.',
   $md$# Customer PII Handling

`email`, `phone`, and `address` are classified **PII**. They may exist in raw landing tables, but they must never be joined into a shared mart in the clear, and they must never be exported without masking.

The mechanics of masking, who may read the raw values, and how access is logged are covered in the [PII Masking Policy](mcp:knowledge_page:kp-seed-18). In short: masked columns are safe to use everywhere; the raw values require the `pii_reader` role and every read is recorded for compliance review.

These columns originate on [customer_segments](urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.analytics.customer_segments,PROD)). If you need to segment or join on a customer without touching PII, use the resolved `customer_key`, never the raw email.
$md$,
   '["governance","pii"]'::jsonb, 'marcus.webb@example.com', 'marcus.webb@example.com', 'marcus.webb@example.com', 3, '2026-05-20T08:00:00Z', '2026-06-20T16:00:00Z'),

  ('kp-seed-4', 'daily-sales-table-guide', 'daily_sales Table Guide',
   'daily_sales is one row per store per day, partitioned by date. Backfills land two days late, so the last 48 hours are never final.',
   $md$# daily_sales Table Guide

[daily_sales](urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)) is the base fact for retail reporting: **one row per store per day**, partitioned by `date`, served from the [acme-warehouse](mcp:connection:(trino,acme-warehouse)) connection.

The single most important gotcha is freshness. Backfills and late-arriving corrections land up to **two days late**, so the last 48 hours are provisional. Do not publish a final number that depends on the trailing two days; the timing rules are spelled out in [Lineage and Freshness SLAs](mcp:knowledge_page:kp-seed-8).

For curated reporting do not query this table directly. The [sales_mart Schema](mcp:knowledge_page:kp-seed-17) page describes the conformed star schema that sits on top of it and is what dashboards should read.
$md$,
   '["data-quality","retail"]'::jsonb, 'sarah.chen@example.com', 'sarah.chen@example.com', 'priya.nair@example.com', 4, '2026-04-12T11:00:00Z', '2026-06-22T09:15:00Z'),

  ('kp-seed-5', 'returns-and-refunds', 'Returns and Refunds Logic',
   'Returns post to the refunds table with a negative amount and a reason_code. They net against revenue in the reporting layer, not at ingest.',
   $md$# Returns and Refunds Logic

A return posts a row to [refunds](urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.refunds,PROD)) with a **negative** `amount` and a `reason_code`. Crucially, the return is **not** applied at ingest: the original sale stays whole, and the offset is applied later in the reporting layer.

This is why gross figures and net figures diverge. The gross `amount` in [Revenue Definition](mcp:knowledge_page:kp-seed-2) never sees the return, while the [Net Revenue Definition](mcp:knowledge_page:kp-seed-9) subtracts it. If you sum `amount` across sales and refunds yourself you will double-count timing, so prefer the net measure.

Returns can post in a later fiscal period than the original sale. Reporting attributes the return to the period it posts in, not the period of the sale.
$md$,
   '["finance","metrics","data-quality"]'::jsonb, 'marcus.webb@example.com', 'marcus.webb@example.com', 'marcus.webb@example.com', 2, '2026-05-02T13:00:00Z', '2026-06-12T10:45:00Z'),

  ('kp-seed-8', 'lineage-and-freshness-slas', 'Lineage and Freshness SLAs',
   'daily_sales feeds sales_mart feeds the executive dashboard. Marts refresh by 06:00 UTC; page the on-call if the dashboard is stale past 08:00.',
   $md$# Lineage and Freshness SLAs

The reporting chain is short and load-bearing: [daily_sales](urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)) feeds the curated [sales_mart Schema](mcp:knowledge_page:kp-seed-17), which feeds the executive dashboard. A delay anywhere upstream surfaces at the very end.

Freshness is governed by two pages. [Freshness SLA Tiers](mcp:knowledge_page:kp-seed-20) sets the tier each dataset is held to, and [ETL Refresh Windows](mcp:knowledge_page:kp-seed-16) sets the clock the loads run on. The contract: marts refresh by **06:00 UTC**, and the on-call is paged if the dashboard is still stale past 08:00.

Because backfills into the base table are two days late (see the [daily_sales Table Guide](mcp:knowledge_page:kp-seed-4)), "fresh" and "final" are not the same thing. A mart can be fresh as of its last run yet still be revised when a late partition lands.
$md$,
   '["lineage","sla","data-quality"]'::jsonb, 'priya.nair@example.com', 'priya.nair@example.com', 'priya.nair@example.com', 2, '2026-05-15T07:00:00Z', '2026-06-19T08:00:00Z'),

  ('kp-seed-9', 'net-revenue-definition', 'Net Revenue Definition',
   'net_revenue is gross sales minus returns, discounts, and tax. It is the only revenue figure used in board-level reporting, so always lead with it rather than gross.',
   $md$# Net Revenue Definition

net_revenue is gross sales **minus returns, discounts, and tax**. It is the only revenue figure used in board-level reporting, so always lead with it rather than gross.

It is built on top of two other definitions. The gross input is the `amount` field from [Revenue Definition](mcp:knowledge_page:kp-seed-2), and the returns it subtracts are exactly those described in [Returns and Refunds Logic](mcp:knowledge_page:kp-seed-5). Because returns net in the reporting layer, net_revenue for a period can move after the period closes.

The measure is computed from [daily_sales](urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)) on the [acme-warehouse](mcp:connection:(trino,acme-warehouse)) connection. When a stakeholder simply says "revenue", they mean this; the [Glossary: Core Terms](mcp:knowledge_page:kp-seed-28) page records that mapping so the word is unambiguous in reporting.
$md$,
   '["finance","revenue","metrics","reporting"]'::jsonb, 'sarah.chen@example.com', 'sarah.chen@example.com', 'marcus.johnson@example.com', 1, NOW() - interval '5 days', NOW() - interval '1 days'),

  ('kp-seed-16', 'etl-refresh-windows', 'ETL Refresh Windows',
   'Core marts refresh nightly between 02:00 and 04:00 UTC. Querying mid-refresh can return a partial partition, so prefer reads after 04:30 UTC.',
   $md$# ETL Refresh Windows

Core marts refresh nightly between **02:00 and 04:00 UTC** on the [acme-warehouse](mcp:connection:(trino,acme-warehouse)) connection. A query that lands mid-refresh can read a half-written partition, so for anything that must be correct, prefer reads after **04:30 UTC**.

These windows are how the platform meets the [Freshness SLA Tiers](mcp:knowledge_page:kp-seed-20): a gold dataset gets an extra intraday pass, while silver and bronze ride the nightly window only. The end-to-end timing, including the 06:00 UTC mart deadline, is in [Lineage and Freshness SLAs](mcp:knowledge_page:kp-seed-8).

The reporting consumer of these loads is [sales_mart Schema](mcp:knowledge_page:kp-seed-17). If a dashboard number looks wrong first thing in the morning, check whether its mart had finished its window before you read it.
$md$,
   '["etl","freshness","sla","observability"]'::jsonb, 'admin@example.com', 'admin@example.com', 'priya.nair@example.com', 4, NOW() - interval '12 days', NOW() - interval '3 days'),

  ('kp-seed-17', 'sales-mart-schema', 'sales_mart Schema',
   'sales_mart is the curated star schema for sales reporting: one fact table and four conformed dimensions. Dashboards should read this, never the base table.',
   $md$# sales_mart Schema

[sales_mart](urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.sales_mart,PROD)) is the curated star schema for sales reporting: one fact table (`sales_fact`) and four conformed dimensions (`date`, `store`, `product`, `customer`).

It is built from the base [daily_sales Table Guide](mcp:knowledge_page:kp-seed-4) table, with the cleanup and conformance applied so that reporting does not have to. Its place in the pipeline and its refresh deadline are described in [Lineage and Freshness SLAs](mcp:knowledge_page:kp-seed-8).

Use the measures here rather than recomputing from raw rows; the canonical names (revenue, margin, attach rate) resolve through the [Glossary: Core Terms](mcp:knowledge_page:kp-seed-28) page so the same word means the same thing on every dashboard.
$md$,
   '["schema","reporting","lineage","dashboards"]'::jsonb, 'sarah.chen@example.com', 'sarah.chen@example.com', 'marcus.johnson@example.com', 1, NOW() - interval '13 days', NOW() - interval '4 days'),

  ('kp-seed-18', 'pii-masking-policy', 'PII Masking Policy',
   'Email, phone, and address must be masked in any shared mart. Unmasked access requires the pii_reader role and is logged for compliance review.',
   $md$# PII Masking Policy

Email, phone, and address must be **masked** in any shared mart. The masked form (a stable hash) is safe to join and group on; the raw value is restricted.

This is the enforcement side of [Customer PII Handling](mcp:knowledge_page:kp-seed-3): that page says which columns are PII, this one says what you may do with them. Reading the unmasked values requires the `pii_reader` role, and every such read is logged for compliance review.

On [customer_segments](urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.analytics.customer_segments,PROD)) the masked columns carry a `_masked` suffix. If a query needs to join customers across datasets, join on the resolved `customer_key`, which is not PII, rather than on email.
$md$,
   '["pii","governance","security","compliance"]'::jsonb, 'marcus.webb@example.com', 'marcus.webb@example.com', 'rachel.thompson@example.com', 2, NOW() - interval '14 days', NOW() - interval '5 days'),

  ('kp-seed-20', 'freshness-sla-tiers', 'Freshness SLA Tiers',
   'Datasets are assigned a freshness tier: gold refreshes hourly, silver daily, bronze weekly. The tier sets the alert threshold the on-call pages against.',
   $md$# Freshness SLA Tiers

Every dataset is assigned a freshness **tier**: gold refreshes hourly, silver daily, bronze weekly. The tier is a promise to consumers and it sets the threshold the on-call is paged against.

The tier is delivered by the loads in [ETL Refresh Windows](mcp:knowledge_page:kp-seed-16) and enforced end to end by [Lineage and Freshness SLAs](mcp:knowledge_page:kp-seed-8). A dataset cannot be promoted to gold unless its source on the [acme-warehouse](mcp:connection:(trino,acme-warehouse)) connection can actually sustain an hourly load.

Pick the lowest tier that meets the real need. Gold is expensive and noisy; most reporting tables are correctly silver, refreshed once in the nightly window.
$md$,
   '["freshness","sla","observability","data-quality"]'::jsonb, 'marcus.johnson@example.com', 'marcus.johnson@example.com', 'amanda.lee@example.com', 4, NOW() - interval '16 days', NOW() - interval '2 days'),

  ('kp-seed-28', 'glossary-core-terms', 'Glossary: Core Terms',
   'Canonical definitions for the terms used across reporting: revenue, margin, attach rate, and active customer. When a metric is ambiguous, this page is the tie-breaker.',
   $md$# Glossary: Core Terms

This page is the tie-breaker when a reporting term is ambiguous. It does not redefine the metrics, it points at the one page that owns each.

**Revenue.** Unqualified, "revenue" means net revenue, defined on [Net Revenue Definition](mcp:knowledge_page:kp-seed-9). The gross `amount` field, which is margin before returns, is defined separately on [Revenue Definition](mcp:knowledge_page:kp-seed-2); never report it as revenue.

**Returns.** A return is a negative-amount row whose accounting is described in [Returns and Refunds Logic](mcp:knowledge_page:kp-seed-5). Returns net in the reporting layer, so a closed period can still move.

**Margin** is revenue minus cost of goods. **Attach rate** is the share of orders that include an accessory line. **Active customer** is one with a purchase in the trailing fiscal quarter; the period is the fiscal quarter, not the civil one.
$md$,
   '["glossary","metrics","reporting","governance"]'::jsonb, 'marcus.johnson@example.com', 'marcus.johnson@example.com', 'amanda.lee@example.com', 4, NOW() - interval '24 days', NOW() - interval '5 days')
-- Refresh the curated bodies on a re-seed instead of skipping them: a dev DB that
-- was seeded from the older fixture reuses these kp-seed-* ids, so DO NOTHING would
-- silently keep the stale pages. (Pages dropped from this set still linger until a
-- volume reset via `make dev-down && make dev`.)
ON CONFLICT (id) DO UPDATE SET
  slug = EXCLUDED.slug,
  title = EXCLUDED.title,
  summary = EXCLUDED.summary,
  body = EXCLUDED.body,
  tags = EXCLUDED.tags,
  updated_by = EXCLUDED.updated_by,
  current_version = EXCLUDED.current_version,
  updated_at = EXCLUDED.updated_at;

-- Inline entity references for the curated pages, mirroring the inline mentions in
-- each body above so inbound backlinks form and the reference graph is navigable in
-- both directions (#709). source='inline' so a handler-side reconcile would rebuild
-- the same set from the body. The one deliberately broken ref (kp-seed-1's retired
-- warehouse connection) is intentionally NOT listed here: it has no existing target,
-- so it renders struck-through in the body and never appears in the Related panel.
INSERT INTO knowledge_page_entity_refs
  (id, page_id, target_type, ref_page_id, connection_kind, connection_name, entity_urn, source, created_by)
VALUES
  ('kpref-1-1', 'kp-seed-1', 'knowledge_page', 'kp-seed-2', NULL, NULL, NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-1-2', 'kp-seed-1', 'knowledge_page', 'kp-seed-9', NULL, NULL, NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-1-3', 'kp-seed-1', 'knowledge_page', 'kp-seed-28', NULL, NULL, NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-1-4', 'kp-seed-1', 'datahub', NULL, NULL, NULL, 'urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)', 'inline', 'sarah.chen@example.com'),
  ('kpref-2-1', 'kp-seed-2', 'knowledge_page', 'kp-seed-9', NULL, NULL, NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-2-2', 'kp-seed-2', 'knowledge_page', 'kp-seed-5', NULL, NULL, NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-2-3', 'kp-seed-2', 'knowledge_page', 'kp-seed-28', NULL, NULL, NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-2-4', 'kp-seed-2', 'datahub', NULL, NULL, NULL, 'urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)', 'inline', 'sarah.chen@example.com'),
  ('kpref-3-1', 'kp-seed-3', 'knowledge_page', 'kp-seed-18', NULL, NULL, NULL, 'inline', 'marcus.webb@example.com'),
  ('kpref-3-2', 'kp-seed-3', 'datahub', NULL, NULL, NULL, 'urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.analytics.customer_segments,PROD)', 'inline', 'marcus.webb@example.com'),
  ('kpref-4-1', 'kp-seed-4', 'knowledge_page', 'kp-seed-17', NULL, NULL, NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-4-2', 'kp-seed-4', 'knowledge_page', 'kp-seed-8', NULL, NULL, NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-4-3', 'kp-seed-4', 'connection', NULL, 'trino', 'acme-warehouse', NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-4-4', 'kp-seed-4', 'datahub', NULL, NULL, NULL, 'urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)', 'inline', 'sarah.chen@example.com'),
  ('kpref-5-1', 'kp-seed-5', 'knowledge_page', 'kp-seed-9', NULL, NULL, NULL, 'inline', 'marcus.webb@example.com'),
  ('kpref-5-2', 'kp-seed-5', 'knowledge_page', 'kp-seed-2', NULL, NULL, NULL, 'inline', 'marcus.webb@example.com'),
  ('kpref-5-3', 'kp-seed-5', 'datahub', NULL, NULL, NULL, 'urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.refunds,PROD)', 'inline', 'marcus.webb@example.com'),
  ('kpref-8-1', 'kp-seed-8', 'knowledge_page', 'kp-seed-4', NULL, NULL, NULL, 'inline', 'priya.nair@example.com'),
  ('kpref-8-2', 'kp-seed-8', 'knowledge_page', 'kp-seed-17', NULL, NULL, NULL, 'inline', 'priya.nair@example.com'),
  ('kpref-8-3', 'kp-seed-8', 'knowledge_page', 'kp-seed-20', NULL, NULL, NULL, 'inline', 'priya.nair@example.com'),
  ('kpref-8-4', 'kp-seed-8', 'knowledge_page', 'kp-seed-16', NULL, NULL, NULL, 'inline', 'priya.nair@example.com'),
  ('kpref-8-5', 'kp-seed-8', 'datahub', NULL, NULL, NULL, 'urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)', 'inline', 'priya.nair@example.com'),
  ('kpref-9-1', 'kp-seed-9', 'knowledge_page', 'kp-seed-2', NULL, NULL, NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-9-2', 'kp-seed-9', 'knowledge_page', 'kp-seed-5', NULL, NULL, NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-9-3', 'kp-seed-9', 'knowledge_page', 'kp-seed-28', NULL, NULL, NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-9-4', 'kp-seed-9', 'connection', NULL, 'trino', 'acme-warehouse', NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-9-5', 'kp-seed-9', 'datahub', NULL, NULL, NULL, 'urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)', 'inline', 'sarah.chen@example.com'),
  ('kpref-16-1', 'kp-seed-16', 'knowledge_page', 'kp-seed-20', NULL, NULL, NULL, 'inline', 'admin@example.com'),
  ('kpref-16-2', 'kp-seed-16', 'knowledge_page', 'kp-seed-8', NULL, NULL, NULL, 'inline', 'admin@example.com'),
  ('kpref-16-3', 'kp-seed-16', 'knowledge_page', 'kp-seed-17', NULL, NULL, NULL, 'inline', 'admin@example.com'),
  ('kpref-16-4', 'kp-seed-16', 'connection', NULL, 'trino', 'acme-warehouse', NULL, 'inline', 'admin@example.com'),
  ('kpref-17-1', 'kp-seed-17', 'knowledge_page', 'kp-seed-4', NULL, NULL, NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-17-2', 'kp-seed-17', 'knowledge_page', 'kp-seed-8', NULL, NULL, NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-17-3', 'kp-seed-17', 'knowledge_page', 'kp-seed-28', NULL, NULL, NULL, 'inline', 'sarah.chen@example.com'),
  ('kpref-17-4', 'kp-seed-17', 'datahub', NULL, NULL, NULL, 'urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.sales_mart,PROD)', 'inline', 'sarah.chen@example.com'),
  ('kpref-18-1', 'kp-seed-18', 'knowledge_page', 'kp-seed-3', NULL, NULL, NULL, 'inline', 'marcus.webb@example.com'),
  ('kpref-18-2', 'kp-seed-18', 'datahub', NULL, NULL, NULL, 'urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.analytics.customer_segments,PROD)', 'inline', 'marcus.webb@example.com'),
  ('kpref-20-1', 'kp-seed-20', 'knowledge_page', 'kp-seed-16', NULL, NULL, NULL, 'inline', 'marcus.johnson@example.com'),
  ('kpref-20-2', 'kp-seed-20', 'knowledge_page', 'kp-seed-8', NULL, NULL, NULL, 'inline', 'marcus.johnson@example.com'),
  ('kpref-20-3', 'kp-seed-20', 'connection', NULL, 'trino', 'acme-warehouse', NULL, 'inline', 'marcus.johnson@example.com'),
  ('kpref-28-1', 'kp-seed-28', 'knowledge_page', 'kp-seed-9', NULL, NULL, NULL, 'inline', 'marcus.johnson@example.com'),
  ('kpref-28-2', 'kp-seed-28', 'knowledge_page', 'kp-seed-2', NULL, NULL, NULL, 'inline', 'marcus.johnson@example.com'),
  ('kpref-28-3', 'kp-seed-28', 'knowledge_page', 'kp-seed-5', NULL, NULL, NULL, 'inline', 'marcus.johnson@example.com')
-- Refresh ref rows on re-seed too, so an updated graph replaces a stale one for
-- reused kpref-* ids rather than being skipped.
ON CONFLICT (id) DO UPDATE SET
  page_id = EXCLUDED.page_id,
  target_type = EXCLUDED.target_type,
  ref_page_id = EXCLUDED.ref_page_id,
  connection_kind = EXCLUDED.connection_kind,
  connection_name = EXCLUDED.connection_name,
  entity_urn = EXCLUDED.entity_urn,
  source = EXCLUDED.source;

-- ============================================================================
-- Live memory for the dev admin (admin@example.com == acme-dev-key-2024).
-- The Memory tab is self-scoped (created_by = caller email), so these must be
-- owned by admin@example.com to populate it. dimension 'preference' ->
-- personal_preference, 'event' -> episodic_event; these are LIVE memory (no
-- insight_status overlay), distinct from the reviewable insights below.
-- ============================================================================
INSERT INTO memory_records
  (id, created_by, persona, dimension, sink_class, category, content, confidence, source,
   entity_urns, related_columns, metadata, status, created_at, updated_at, last_verified)
VALUES
  ('mem-live-01', 'admin@example.com', 'admin', 'preference', 'personal_preference', 'usage_guidance',
   'Default query results to GROUP BY district_id rather than individual stores unless a specific store is named.',
   'high', 'user', '[]'::jsonb, '[]'::jsonb, '{}'::jsonb, 'active',
   NOW() - interval '14 days', NOW() - interval '14 days', NOW() - interval '2 days'),
  ('mem-live-02', 'admin@example.com', 'admin', 'preference', 'personal_preference', 'usage_guidance',
   'Prefer ISO date formatting (YYYY-MM-DD) and UTC in all exported reports.',
   'high', 'user', '[]'::jsonb, '[]'::jsonb, '{}'::jsonb, 'active',
   NOW() - interval '12 days', NOW() - interval '12 days', NULL),
  ('mem-live-03', 'admin@example.com', 'admin', 'preference', 'personal_preference', 'enhancement',
   'When summarizing revenue, lead with net_revenue and show gross only on request.',
   'medium', 'user', '[]'::jsonb, '[]'::jsonb, '{}'::jsonb, 'active',
   NOW() - interval '9 days', NOW() - interval '9 days', NULL),
  ('mem-live-04', 'admin@example.com', 'admin', 'preference', 'personal_preference', 'usage_guidance',
   'Keep SQL examples to <= 20 lines and prefer CTEs over nested subqueries for readability.',
   'medium', 'user', '[]'::jsonb, '[]'::jsonb, '{}'::jsonb, 'active',
   NOW() - interval '7 days', NOW() - interval '7 days', NULL),
  ('mem-live-05', 'admin@example.com', 'admin', 'event', 'episodic_event', 'business_context',
   'On 2026-06-10 finance confirmed daily_sales.amount excludes returns; flagged for a knowledge page.',
   'high', 'agent_discovery', '["urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"]'::jsonb,
   '[]'::jsonb, '{}'::jsonb, 'active',
   NOW() - interval '13 days', NOW() - interval '13 days', NULL),
  ('mem-live-06', 'admin@example.com', 'admin', 'event', 'episodic_event', 'data_quality',
   'Ran a backfill of warehouse_transfers facility_id on 2026-06-15; legacy WH-NNN rows now mapped.',
   'medium', 'agent_discovery', '[]'::jsonb, '[]'::jsonb, '{}'::jsonb, 'active',
   NOW() - interval '8 days', NOW() - interval '8 days', NULL),
  ('mem-live-07', 'admin@example.com', 'admin', 'event', 'episodic_event', 'usage_guidance',
   'Investigated a slow exec_dashboard refresh on 2026-06-19; root cause was a stale sales_mart partition.',
   'medium', 'agent_discovery', '[]'::jsonb, '[]'::jsonb, '{}'::jsonb, 'active',
   NOW() - interval '4 days', NOW() - interval '4 days', NULL),
  ('mem-live-08', 'admin@example.com', 'admin', 'preference', 'personal_preference', 'enhancement',
   'Surface open feedback counts before drilling into a knowledge page so review work is visible first.',
   'low', 'user', '[]'::jsonb, '[]'::jsonb, '{}'::jsonb, 'active',
   NOW() - interval '3 days', NOW() - interval '3 days', NULL)
ON CONFLICT (id) DO NOTHING;

-- ============================================================================
-- Additional reviewable insights. The existing ins-001..008 are captured by
-- other users (the cross-user Review queue). These add admin-owned insights so
-- the "My Insights" tab is non-empty, plus a spread of insight_status
-- (pending / approved / applied) and sink classes for status-filter screenshots.
-- ============================================================================
INSERT INTO memory_records
  (id, created_by, persona, dimension, sink_class, category, content, confidence, source,
   entity_urns, related_columns, metadata, status, created_at, updated_at, last_verified)
VALUES
  ('ins-admin-01', 'admin@example.com', 'admin', 'knowledge', 'business_knowledge', 'business_context',
   'Customer loyalty tiers were renamed in Q3 FY2024: Bronze/Silver/Gold -> Explorer/Enthusiast/Champion.',
   'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active',
   NOW() - interval '3 days', NOW() - interval '3 days', NULL),
  ('ins-admin-02', 'admin@example.com', 'admin', 'knowledge', 'business_knowledge', 'correction',
   'The fiscal year starts in February, not January; align all YoY comparisons to the fiscal calendar.',
   'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active',
   NOW() - interval '2 days', NOW() - interval '2 days', NULL),
  ('ins-admin-03', 'admin@example.com', 'admin', 'knowledge', 'schema_entity', 'data_quality',
   'daily_sales is one row per store per day, partitioned by date; backfills land ~2 days late.',
   'high', 'user', '["urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"]'::jsonb,
   '[{"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)", "column": "date", "relevance": "partition_key"}]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active',
   NOW() - interval '6 days', NOW() - interval '4 days', NULL),
  ('ins-admin-04', 'admin@example.com', 'admin', 'knowledge', 'operational_rule', 'usage_guidance',
   'Never query the regional_performance view during its 02:00-02:30 UTC nightly refresh window.',
   'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active',
   NOW() - interval '7 days', NOW() - interval '5 days', NULL),
  ('ins-admin-05', 'admin@example.com', 'admin', 'knowledge', 'business_knowledge', 'business_context',
   'Returns net against revenue in the reporting layer, not at ingest; they post to refunds with a negative amount.',
   'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com',
                      'applied_by', 'admin@example.com', 'changeset_ref', 'cs-seed-01'), 'active',
   NOW() - interval '10 days', NOW() - interval '6 days', NULL),
  ('ins-admin-06', 'admin@example.com', 'admin', 'knowledge', 'schema_entity', 'correction',
   'customer_segments is a Type-2 SCD; filter is_current = true for the latest segment assignment.',
   'high', 'user', '["urn:li:dataset:(urn:li:dataPlatform:trino,retail.customer_segments,PROD)"]'::jsonb,
   '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com',
                      'applied_by', 'admin@example.com', 'changeset_ref', 'cs-seed-02'), 'active',
   NOW() - interval '11 days', NOW() - interval '8 days', NULL)
ON CONFLICT (id) DO NOTHING;

-- Bulk knowledge-dimension insights so the admin review queue has a pending
-- count spanning more than one page (pending scattered behind approved/applied),
-- making the #706 count/pagination fix observable.
INSERT INTO memory_records
  (id, created_by, persona, dimension, sink_class, category, content, confidence, source,
   entity_urns, related_columns, metadata, status, created_at, updated_at, last_verified)
VALUES
  ('ins-bulk-001', 'sarah.chen@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'business_context',
   'The daily_sales is partitioned by date in UTC and backfills land two days late.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '1 days', NOW() - interval '1 days', NULL),
  ('ins-bulk-002', 'marcus.webb@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'correction',
   'The sales_mart excludes tax and returns from its revenue figure.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '2 days', NOW() - interval '2 days', NULL),
  ('ins-bulk-003', 'priya.nair@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'data_quality',
   'The inventory_snapshot must be joined through the identity map to avoid double-counting.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '3 days', NOW() - interval '3 days', NULL),
  ('ins-bulk-004', 'marcus.johnson@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'usage_guidance',
   'The price_history refreshes nightly and should not be read mid-window.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'rejected', 'reviewed_by', 'admin@example.com', 'review_notes', 'By design per the data contract.'), 'archived', NOW() - interval '4 days', NOW() - interval '4 days', NULL),
  ('ins-bulk-005', 'rachel.thompson@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'enhancement',
   'The returns uses a controlled vocabulary that retired free-text values in FY2025.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '5 days', NOW() - interval '5 days', NULL),
  ('ins-bulk-006', 'david.park@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'business_context',
   'The customer dimension measures lead time in business days, excluding put-away.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '6 days', NOW() - interval '6 days', NULL),
  ('ins-bulk-007', 'amanda.lee@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'correction',
   'The order_events keeps only the current state, with history in the events table.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '7 days', NOW() - interval '7 days', NULL),
  ('ins-bulk-008', 'admin@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'data_quality',
   'The exec_dashboard is the certified source of truth for board reporting.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'rejected', 'reviewed_by', 'admin@example.com', 'review_notes', 'By design per the data contract.'), 'archived', NOW() - interval '8 days', NOW() - interval '8 days', NULL),
  ('ins-bulk-009', 'sarah.chen@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'usage_guidance',
   'The supplier_lead_time has a deprecated column scheduled for removal next quarter.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '9 days', NOW() - interval '9 days', NULL),
  ('ins-bulk-010', 'marcus.webb@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'enhancement',
   'The loyalty tiers should be filtered by date to avoid a full partition scan.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '10 days', NOW() - interval '10 days', NULL),
  ('ins-bulk-011', 'priya.nair@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'business_context',
   'The promotions is partitioned by date in UTC and backfills land two days late.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '11 days', NOW() - interval '11 days', NULL),
  ('ins-bulk-012', 'marcus.johnson@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'correction',
   'The shipping zones excludes tax and returns from its revenue figure.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'rejected', 'reviewed_by', 'admin@example.com', 'review_notes', 'By design per the data contract.'), 'archived', NOW() - interval '12 days', NOW() - interval '12 days', NULL),
  ('ins-bulk-013', 'rachel.thompson@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'data_quality',
   'The net_revenue must be joined through the identity map to avoid double-counting.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '13 days', NOW() - interval '13 days', NULL),
  ('ins-bulk-014', 'david.park@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'usage_guidance',
   'The store_calendar refreshes nightly and should not be read mid-window.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '14 days', NOW() - interval '14 days', NULL),
  ('ins-bulk-015', 'amanda.lee@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'enhancement',
   'The daily_sales uses a controlled vocabulary that retired free-text values in FY2025.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '15 days', NOW() - interval '15 days', NULL),
  ('ins-bulk-016', 'admin@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'business_context',
   'The sales_mart measures lead time in business days, excluding put-away.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'rejected', 'reviewed_by', 'admin@example.com', 'review_notes', 'By design per the data contract.'), 'archived', NOW() - interval '16 days', NOW() - interval '16 days', NULL),
  ('ins-bulk-017', 'sarah.chen@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'correction',
   'The inventory_snapshot keeps only the current state, with history in the events table.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '17 days', NOW() - interval '17 days', NULL),
  ('ins-bulk-018', 'marcus.webb@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'data_quality',
   'The price_history is the certified source of truth for board reporting.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '18 days', NOW() - interval '18 days', NULL),
  ('ins-bulk-019', 'priya.nair@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'usage_guidance',
   'The returns has a deprecated column scheduled for removal next quarter.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '19 days', NOW() - interval '19 days', NULL),
  ('ins-bulk-020', 'marcus.johnson@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'enhancement',
   'The customer dimension should be filtered by date to avoid a full partition scan.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'rejected', 'reviewed_by', 'admin@example.com', 'review_notes', 'By design per the data contract.'), 'archived', NOW() - interval '20 days', NOW() - interval '20 days', NULL),
  ('ins-bulk-021', 'rachel.thompson@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'business_context',
   'The order_events is partitioned by date in UTC and backfills land two days late.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '21 days', NOW() - interval '21 days', NULL),
  ('ins-bulk-022', 'david.park@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'correction',
   'The exec_dashboard excludes tax and returns from its revenue figure.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '22 days', NOW() - interval '22 days', NULL),
  ('ins-bulk-023', 'amanda.lee@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'data_quality',
   'The supplier_lead_time must be joined through the identity map to avoid double-counting.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '23 days', NOW() - interval '23 days', NULL),
  ('ins-bulk-024', 'admin@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'usage_guidance',
   'The loyalty tiers refreshes nightly and should not be read mid-window.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'rejected', 'reviewed_by', 'admin@example.com', 'review_notes', 'By design per the data contract.'), 'archived', NOW() - interval '24 days', NOW() - interval '24 days', NULL),
  ('ins-bulk-025', 'sarah.chen@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'enhancement',
   'The promotions uses a controlled vocabulary that retired free-text values in FY2025.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '25 days', NOW() - interval '25 days', NULL),
  ('ins-bulk-026', 'marcus.webb@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'business_context',
   'The shipping zones measures lead time in business days, excluding put-away.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '26 days', NOW() - interval '26 days', NULL),
  ('ins-bulk-027', 'priya.nair@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'correction',
   'The net_revenue keeps only the current state, with history in the events table.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '27 days', NOW() - interval '27 days', NULL),
  ('ins-bulk-028', 'marcus.johnson@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'data_quality',
   'The store_calendar is the certified source of truth for board reporting.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'rejected', 'reviewed_by', 'admin@example.com', 'review_notes', 'By design per the data contract.'), 'archived', NOW() - interval '28 days', NOW() - interval '28 days', NULL),
  ('ins-bulk-029', 'rachel.thompson@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'usage_guidance',
   'The daily_sales has a deprecated column scheduled for removal next quarter.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '1 days', NOW() - interval '1 days', NULL),
  ('ins-bulk-030', 'david.park@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'enhancement',
   'The sales_mart should be filtered by date to avoid a full partition scan.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '2 days', NOW() - interval '2 days', NULL),
  ('ins-bulk-031', 'amanda.lee@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'business_context',
   'The inventory_snapshot is partitioned by date in UTC and backfills land two days late.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '3 days', NOW() - interval '3 days', NULL),
  ('ins-bulk-032', 'admin@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'correction',
   'The price_history excludes tax and returns from its revenue figure.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'rejected', 'reviewed_by', 'admin@example.com', 'review_notes', 'By design per the data contract.'), 'archived', NOW() - interval '4 days', NOW() - interval '4 days', NULL),
  ('ins-bulk-033', 'sarah.chen@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'data_quality',
   'The returns must be joined through the identity map to avoid double-counting.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '5 days', NOW() - interval '5 days', NULL),
  ('ins-bulk-034', 'marcus.webb@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'usage_guidance',
   'The customer dimension refreshes nightly and should not be read mid-window.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '6 days', NOW() - interval '6 days', NULL),
  ('ins-bulk-035', 'priya.nair@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'enhancement',
   'The order_events uses a controlled vocabulary that retired free-text values in FY2025.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '7 days', NOW() - interval '7 days', NULL),
  ('ins-bulk-036', 'marcus.johnson@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'business_context',
   'The exec_dashboard measures lead time in business days, excluding put-away.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'rejected', 'reviewed_by', 'admin@example.com', 'review_notes', 'By design per the data contract.'), 'archived', NOW() - interval '8 days', NOW() - interval '8 days', NULL),
  ('ins-bulk-037', 'rachel.thompson@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'correction',
   'The supplier_lead_time keeps only the current state, with history in the events table.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '9 days', NOW() - interval '9 days', NULL),
  ('ins-bulk-038', 'david.park@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'data_quality',
   'The loyalty tiers is the certified source of truth for board reporting.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '10 days', NOW() - interval '10 days', NULL),
  ('ins-bulk-039', 'amanda.lee@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'usage_guidance',
   'The promotions has a deprecated column scheduled for removal next quarter.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '11 days', NOW() - interval '11 days', NULL),
  ('ins-bulk-040', 'admin@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'enhancement',
   'The shipping zones should be filtered by date to avoid a full partition scan.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'rejected', 'reviewed_by', 'admin@example.com', 'review_notes', 'By design per the data contract.'), 'archived', NOW() - interval '12 days', NOW() - interval '12 days', NULL),
  ('ins-bulk-041', 'sarah.chen@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'business_context',
   'The net_revenue is partitioned by date in UTC and backfills land two days late.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '13 days', NOW() - interval '13 days', NULL),
  ('ins-bulk-042', 'marcus.webb@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'correction',
   'The store_calendar excludes tax and returns from its revenue figure.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '14 days', NOW() - interval '14 days', NULL),
  ('ins-bulk-043', 'priya.nair@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'data_quality',
   'The daily_sales must be joined through the identity map to avoid double-counting.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '15 days', NOW() - interval '15 days', NULL),
  ('ins-bulk-044', 'marcus.johnson@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'usage_guidance',
   'The sales_mart refreshes nightly and should not be read mid-window.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '16 days', NOW() - interval '16 days', NULL),
  ('ins-bulk-045', 'rachel.thompson@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'enhancement',
   'The inventory_snapshot uses a controlled vocabulary that retired free-text values in FY2025.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '17 days', NOW() - interval '17 days', NULL),
  ('ins-bulk-046', 'david.park@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'business_context',
   'The price_history measures lead time in business days, excluding put-away.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '18 days', NOW() - interval '18 days', NULL),
  ('ins-bulk-047', 'amanda.lee@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'correction',
   'The returns keeps only the current state, with history in the events table.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '19 days', NOW() - interval '19 days', NULL),
  ('ins-bulk-048', 'admin@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'data_quality',
   'The customer dimension is the certified source of truth for board reporting.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '20 days', NOW() - interval '20 days', NULL),
  ('ins-bulk-049', 'sarah.chen@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'usage_guidance',
   'The order_events has a deprecated column scheduled for removal next quarter.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '21 days', NOW() - interval '21 days', NULL),
  ('ins-bulk-050', 'marcus.webb@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'enhancement',
   'The exec_dashboard should be filtered by date to avoid a full partition scan.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '22 days', NOW() - interval '22 days', NULL),
  ('ins-bulk-051', 'priya.nair@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'business_context',
   'The supplier_lead_time is partitioned by date in UTC and backfills land two days late.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '23 days', NOW() - interval '23 days', NULL),
  ('ins-bulk-052', 'marcus.johnson@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'correction',
   'The loyalty tiers excludes tax and returns from its revenue figure.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '24 days', NOW() - interval '24 days', NULL),
  ('ins-bulk-053', 'rachel.thompson@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'data_quality',
   'The promotions must be joined through the identity map to avoid double-counting.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '25 days', NOW() - interval '25 days', NULL),
  ('ins-bulk-054', 'david.park@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'usage_guidance',
   'The shipping zones refreshes nightly and should not be read mid-window.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '26 days', NOW() - interval '26 days', NULL),
  ('ins-bulk-055', 'amanda.lee@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'enhancement',
   'The net_revenue uses a controlled vocabulary that retired free-text values in FY2025.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '27 days', NOW() - interval '27 days', NULL),
  ('ins-bulk-056', 'admin@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'business_context',
   'The store_calendar measures lead time in business days, excluding put-away.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '28 days', NOW() - interval '28 days', NULL),
  ('ins-bulk-057', 'sarah.chen@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'correction',
   'The daily_sales keeps only the current state, with history in the events table.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '1 days', NOW() - interval '1 days', NULL),
  ('ins-bulk-058', 'marcus.webb@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'data_quality',
   'The sales_mart is the certified source of truth for board reporting.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '2 days', NOW() - interval '2 days', NULL),
  ('ins-bulk-059', 'priya.nair@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'usage_guidance',
   'The inventory_snapshot has a deprecated column scheduled for removal next quarter.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '3 days', NOW() - interval '3 days', NULL),
  ('ins-bulk-060', 'marcus.johnson@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'enhancement',
   'The price_history should be filtered by date to avoid a full partition scan.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '4 days', NOW() - interval '4 days', NULL),
  ('ins-bulk-061', 'rachel.thompson@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'business_context',
   'The returns is partitioned by date in UTC and backfills land two days late.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '5 days', NOW() - interval '5 days', NULL),
  ('ins-bulk-062', 'david.park@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'correction',
   'The customer dimension excludes tax and returns from its revenue figure.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '6 days', NOW() - interval '6 days', NULL),
  ('ins-bulk-063', 'amanda.lee@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'data_quality',
   'The order_events must be joined through the identity map to avoid double-counting.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '7 days', NOW() - interval '7 days', NULL),
  ('ins-bulk-064', 'admin@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'usage_guidance',
   'The exec_dashboard refreshes nightly and should not be read mid-window.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '8 days', NOW() - interval '8 days', NULL),
  ('ins-bulk-065', 'sarah.chen@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'enhancement',
   'The supplier_lead_time uses a controlled vocabulary that retired free-text values in FY2025.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '9 days', NOW() - interval '9 days', NULL),
  ('ins-bulk-066', 'marcus.webb@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'business_context',
   'The loyalty tiers measures lead time in business days, excluding put-away.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '10 days', NOW() - interval '10 days', NULL),
  ('ins-bulk-067', 'priya.nair@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'correction',
   'The promotions keeps only the current state, with history in the events table.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '11 days', NOW() - interval '11 days', NULL),
  ('ins-bulk-068', 'marcus.johnson@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'data_quality',
   'The shipping zones is the certified source of truth for board reporting.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '12 days', NOW() - interval '12 days', NULL),
  ('ins-bulk-069', 'rachel.thompson@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'usage_guidance',
   'The net_revenue has a deprecated column scheduled for removal next quarter.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '13 days', NOW() - interval '13 days', NULL),
  ('ins-bulk-070', 'david.park@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'enhancement',
   'The store_calendar should be filtered by date to avoid a full partition scan.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '14 days', NOW() - interval '14 days', NULL),
  ('ins-bulk-071', 'amanda.lee@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'business_context',
   'The daily_sales is partitioned by date in UTC and backfills land two days late.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '15 days', NOW() - interval '15 days', NULL),
  ('ins-bulk-072', 'admin@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'correction',
   'The sales_mart excludes tax and returns from its revenue figure.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '16 days', NOW() - interval '16 days', NULL),
  ('ins-bulk-073', 'sarah.chen@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'data_quality',
   'The inventory_snapshot must be joined through the identity map to avoid double-counting.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '17 days', NOW() - interval '17 days', NULL),
  ('ins-bulk-074', 'marcus.webb@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'usage_guidance',
   'The price_history refreshes nightly and should not be read mid-window.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '18 days', NOW() - interval '18 days', NULL),
  ('ins-bulk-075', 'priya.nair@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'enhancement',
   'The returns uses a controlled vocabulary that retired free-text values in FY2025.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '19 days', NOW() - interval '19 days', NULL),
  ('ins-bulk-076', 'marcus.johnson@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'business_context',
   'The customer dimension measures lead time in business days, excluding put-away.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '20 days', NOW() - interval '20 days', NULL),
  ('ins-bulk-077', 'rachel.thompson@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'correction',
   'The order_events keeps only the current state, with history in the events table.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '21 days', NOW() - interval '21 days', NULL),
  ('ins-bulk-078', 'david.park@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'data_quality',
   'The exec_dashboard is the certified source of truth for board reporting.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '22 days', NOW() - interval '22 days', NULL),
  ('ins-bulk-079', 'amanda.lee@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'usage_guidance',
   'The supplier_lead_time has a deprecated column scheduled for removal next quarter.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '23 days', NOW() - interval '23 days', NULL),
  ('ins-bulk-080', 'admin@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'enhancement',
   'The loyalty tiers should be filtered by date to avoid a full partition scan.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '24 days', NOW() - interval '24 days', NULL),
  ('ins-bulk-081', 'sarah.chen@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'business_context',
   'The promotions is partitioned by date in UTC and backfills land two days late.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '25 days', NOW() - interval '25 days', NULL),
  ('ins-bulk-082', 'marcus.webb@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'correction',
   'The shipping zones excludes tax and returns from its revenue figure.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '26 days', NOW() - interval '26 days', NULL),
  ('ins-bulk-083', 'priya.nair@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'data_quality',
   'The net_revenue must be joined through the identity map to avoid double-counting.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '27 days', NOW() - interval '27 days', NULL),
  ('ins-bulk-084', 'marcus.johnson@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'usage_guidance',
   'The store_calendar refreshes nightly and should not be read mid-window.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '28 days', NOW() - interval '28 days', NULL),
  ('ins-bulk-085', 'rachel.thompson@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'enhancement',
   'The daily_sales uses a controlled vocabulary that retired free-text values in FY2025.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'applied', 'reviewed_by', 'admin@example.com', 'applied_by', 'admin@example.com', 'changeset_ref', 'cs-bulk'), 'active', NOW() - interval '1 days', NOW() - interval '1 days', NULL),
  ('ins-bulk-086', 'david.park@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'business_context',
   'The sales_mart measures lead time in business days, excluding put-away.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '2 days', NOW() - interval '2 days', NULL),
  ('ins-bulk-087', 'amanda.lee@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'correction',
   'The inventory_snapshot keeps only the current state, with history in the events table.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '3 days', NOW() - interval '3 days', NULL),
  ('ins-bulk-088', 'admin@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'data_quality',
   'The price_history is the certified source of truth for board reporting.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '4 days', NOW() - interval '4 days', NULL),
  ('ins-bulk-089', 'sarah.chen@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'usage_guidance',
   'The returns has a deprecated column scheduled for removal next quarter.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '5 days', NOW() - interval '5 days', NULL),
  ('ins-bulk-090', 'marcus.webb@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'enhancement',
   'The customer dimension should be filtered by date to avoid a full partition scan.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '6 days', NOW() - interval '6 days', NULL),
  ('ins-bulk-091', 'priya.nair@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'business_context',
   'The order_events is partitioned by date in UTC and backfills land two days late.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '7 days', NOW() - interval '7 days', NULL),
  ('ins-bulk-092', 'marcus.johnson@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'correction',
   'The exec_dashboard excludes tax and returns from its revenue figure.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '8 days', NOW() - interval '8 days', NULL),
  ('ins-bulk-093', 'rachel.thompson@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'data_quality',
   'The supplier_lead_time must be joined through the identity map to avoid double-counting.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '9 days', NOW() - interval '9 days', NULL),
  ('ins-bulk-094', 'david.park@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'usage_guidance',
   'The loyalty tiers refreshes nightly and should not be read mid-window.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'pending'), 'active', NOW() - interval '10 days', NOW() - interval '10 days', NULL),
  ('ins-bulk-095', 'amanda.lee@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'enhancement',
   'The promotions uses a controlled vocabulary that retired free-text values in FY2025.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '11 days', NOW() - interval '11 days', NULL),
  ('ins-bulk-096', 'admin@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'business_context',
   'The shipping zones measures lead time in business days, excluding put-away.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '12 days', NOW() - interval '12 days', NULL),
  ('ins-bulk-097', 'sarah.chen@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'correction',
   'The net_revenue keeps only the current state, with history in the events table.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '13 days', NOW() - interval '13 days', NULL),
  ('ins-bulk-098', 'marcus.webb@example.com', 'data-engineer', 'knowledge', 'schema_entity', 'data_quality',
   'The store_calendar is the certified source of truth for board reporting.', 'medium', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '14 days', NOW() - interval '14 days', NULL),
  ('ins-bulk-099', 'priya.nair@example.com', 'data-engineer', 'knowledge', 'operational_rule', 'usage_guidance',
   'The daily_sales has a deprecated column scheduled for removal next quarter.', 'low', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '15 days', NOW() - interval '15 days', NULL),
  ('ins-bulk-100', 'marcus.johnson@example.com', 'data-engineer', 'knowledge', 'business_knowledge', 'enhancement',
   'The sales_mart should be filtered by date to avoid a full partition scan.', 'high', 'user', '[]'::jsonb, '[]'::jsonb,
   jsonb_build_object('insight_status', 'approved', 'reviewed_by', 'admin@example.com'), 'active', NOW() - interval '16 days', NOW() - interval '16 days', NULL)
ON CONFLICT (id) DO NOTHING;

-- ============================================================================
-- Feedback threads (#600/#662). The dev seed had none, so the Feedback hub,
-- worklists, per-item badges, and the knowledge-page feedback panel were all
-- empty against the real backend. Threads target seeded assets (asset-001..006),
-- collections (coll-001..003), knowledge pages (kp-seed-*), and the standalone
-- channel; one is resolved and linked to a captured insight to exercise the
-- thread -> insight knowledge chain. author_id mirrors the email (no FK).
-- ============================================================================
INSERT INTO portal_threads
  (id, kind, target_type, asset_id, collection_id, prompt_id, knowledge_page_id, anchor, target_version,
   title, author_id, author_email, status, requires_resolution, validation_state, insight_id, created_at, updated_at)
VALUES
  ('thr-seed-01', 'correction', 'asset', 'asset-001', NULL, NULL, NULL,
   '{"type":"text_quote","exact":"monthly active users"}'::jsonb, 1,
   'We do not use that term', 'dana.sme@example.com', 'dana.sme@example.com', 'open', TRUE, 'none', NULL,
   NOW() - interval '6 days', NOW() - interval '5 days'),
  ('thr-seed-02', 'question', 'asset', 'asset-002', NULL, NULL, NULL, NULL, NULL,
   'Which source feeds the revenue column?', 'admin@example.com', 'admin@example.com', 'answered', FALSE, 'none', NULL,
   NOW() - interval '7 days', NOW() - interval '6 days'),
  ('thr-seed-03', 'rating', 'asset', 'asset-003', NULL, NULL, NULL, NULL, NULL,
   'Clear and useful', 'rachel.thompson@example.com', 'rachel.thompson@example.com', 'resolved', FALSE, 'none', NULL,
   NOW() - interval '9 days', NOW() - interval '8 days'),
  ('thr-seed-04', 'suggestion', 'collection', NULL, 'coll-001', NULL, NULL, NULL, NULL,
   'Add a glossary section up front', 'dana.sme@example.com', 'dana.sme@example.com', 'open', FALSE, 'none', NULL,
   NOW() - interval '5 days', NOW() - interval '5 days'),
  ('thr-seed-05', 'comment', 'collection', NULL, 'coll-002', NULL, NULL, NULL, NULL,
   'The ordering of sections could follow the analysis flow', 'admin@example.com', 'admin@example.com', 'open', FALSE, 'none', NULL,
   NOW() - interval '4 days', NOW() - interval '4 days'),
  ('thr-seed-06', 'correction', 'knowledge_page', NULL, NULL, NULL, 'kp-seed-1',
   '{"type":"text_quote","exact":"fiscal year starts in"}'::jsonb, NULL,
   'Fiscal year start is wrong', 'dana.sme@example.com', 'dana.sme@example.com', 'open', FALSE, 'none', NULL,
   NOW() - interval '3 days', NOW() - interval '3 days'),
  ('thr-seed-07', 'question', 'knowledge_page', NULL, NULL, NULL, 'kp-seed-3', NULL, NULL,
   'Does this cover phone numbers in the events stream?', 'rachel.thompson@example.com', 'rachel.thompson@example.com', 'open', FALSE, 'none', NULL,
   NOW() - interval '2 days', NOW() - interval '2 days'),
  ('thr-seed-08', 'correction', 'knowledge_page', NULL, NULL, NULL, 'kp-seed-2', NULL, NULL,
   'Net vs gross needs a concrete example', 'dana.sme@example.com', 'dana.sme@example.com', 'resolved', TRUE, 'none', 'ins-admin-05',
   NOW() - interval '8 days', NOW() - interval '4 days'),
  ('thr-seed-09', 'approval', 'asset', 'asset-004', NULL, NULL, NULL, NULL, NULL,
   'Signed off for the Q4 review', 'david.park@example.com', 'david.park@example.com', 'resolved', FALSE, 'none', NULL,
   NOW() - interval '10 days', NOW() - interval '9 days'),
  ('thr-seed-10', 'comment', 'standalone', NULL, NULL, NULL, NULL, NULL, NULL,
   'The Monday refresh landed Tuesday again this week', 'dana.sme@example.com', 'dana.sme@example.com', 'open', FALSE, 'none', NULL,
   NOW() - interval '5 days', NOW() - interval '5 days'),
  ('thr-seed-11', 'question', 'standalone', NULL, NULL, NULL, NULL, NULL, NULL,
   'Where do I request a new catalog connection?', 'rachel.thompson@example.com', 'rachel.thompson@example.com', 'answered', FALSE, 'none', NULL,
   NOW() - interval '6 days', NOW() - interval '5 days'),
  ('thr-seed-12', 'suggestion', 'asset', 'asset-005', NULL, NULL, NULL, NULL, NULL,
   'A short caption would help non-analysts', 'admin@example.com', 'admin@example.com', 'open', FALSE, 'none', NULL,
   NOW() - interval '1 day', NOW() - interval '1 day')
ON CONFLICT (id) DO NOTHING;

INSERT INTO portal_thread_events
  (id, thread_id, event_type, author_id, author_email, body, rating, parent_event_id, metadata, created_at)
VALUES
  ('evt-seed-01a', 'thr-seed-01', 'comment', 'dana.sme@example.com', 'dana.sme@example.com',
   'We call these active practitioners, not monthly active users.', NULL, NULL, '{}'::jsonb, NOW() - interval '6 days'),
  ('evt-seed-01b', 'thr-seed-01', 'comment', 'admin@example.com', 'admin@example.com',
   'Good catch, updating the dashboard copy.', NULL, NULL, '{}'::jsonb, NOW() - interval '5 days'),
  ('evt-seed-02a', 'thr-seed-02', 'comment', 'admin@example.com', 'admin@example.com',
   'Which source feeds the revenue column?', NULL, NULL, '{}'::jsonb, NOW() - interval '7 days'),
  ('evt-seed-02b', 'thr-seed-02', 'comment', 'marcus.johnson@example.com', 'marcus.johnson@example.com',
   'It is the finance mart, refreshed nightly.', NULL, NULL, '{}'::jsonb, NOW() - interval '6 days'),
  ('evt-seed-03a', 'thr-seed-03', 'rating', 'rachel.thompson@example.com', 'rachel.thompson@example.com',
   'Clear and useful', 5, NULL, '{}'::jsonb, NOW() - interval '9 days'),
  ('evt-seed-04a', 'thr-seed-04', 'comment', 'dana.sme@example.com', 'dana.sme@example.com',
   'A glossary up front would help new readers.', NULL, NULL, '{}'::jsonb, NOW() - interval '5 days'),
  ('evt-seed-05a', 'thr-seed-05', 'comment', 'admin@example.com', 'admin@example.com',
   'The ordering of sections could follow the analysis flow.', NULL, NULL, '{}'::jsonb, NOW() - interval '4 days'),
  ('evt-seed-06a', 'thr-seed-06', 'comment', 'dana.sme@example.com', 'dana.sme@example.com',
   'This page says the fiscal year starts in January, but it starts in February.', NULL, NULL, '{}'::jsonb, NOW() - interval '3 days'),
  ('evt-seed-07a', 'thr-seed-07', 'comment', 'rachel.thompson@example.com', 'rachel.thompson@example.com',
   'Does the PII guidance cover phone numbers landing in the events stream?', NULL, NULL, '{}'::jsonb, NOW() - interval '2 days'),
  ('evt-seed-08a', 'thr-seed-08', 'comment', 'dana.sme@example.com', 'dana.sme@example.com',
   'Net vs gross is ambiguous without a concrete example.', NULL, NULL, '{}'::jsonb, NOW() - interval '8 days'),
  ('evt-seed-08b', 'thr-seed-08', 'insight_linked', 'admin@example.com', 'admin@example.com',
   NULL, NULL, NULL, jsonb_build_object('insight_id', 'ins-admin-05'), NOW() - interval '4 days'),
  ('evt-seed-09a', 'thr-seed-09', 'approval', 'david.park@example.com', 'david.park@example.com',
   'Signed off for the Q4 review.', NULL, NULL, '{}'::jsonb, NOW() - interval '10 days'),
  ('evt-seed-10a', 'thr-seed-10', 'comment', 'dana.sme@example.com', 'dana.sme@example.com',
   'The Monday refresh landed Tuesday again this week.', NULL, NULL, '{}'::jsonb, NOW() - interval '5 days'),
  ('evt-seed-11a', 'thr-seed-11', 'comment', 'rachel.thompson@example.com', 'rachel.thompson@example.com',
   'Where do I request a new catalog connection?', NULL, NULL, '{}'::jsonb, NOW() - interval '6 days'),
  ('evt-seed-11b', 'thr-seed-11', 'comment', 'admin@example.com', 'admin@example.com',
   'Open a request from Connections > New, or ask an admin.', NULL, NULL, '{}'::jsonb, NOW() - interval '5 days'),
  ('evt-seed-12a', 'thr-seed-12', 'comment', 'admin@example.com', 'admin@example.com',
   'A short caption would help non-analysts read this at a glance.', NULL, NULL, '{}'::jsonb, NOW() - interval '1 day')
ON CONFLICT (id) DO NOTHING;
