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

-- ============================================================================
-- Knowledge Insights (8 in various states)
-- ============================================================================

INSERT INTO knowledge_insights (
  id, session_id, captured_by, persona,
  category, insight_text, confidence, entity_urns, related_columns,
  suggested_actions, status, reviewed_by, review_notes, applied_by, changeset_ref,
  created_at
) VALUES
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
ON CONFLICT (id) DO UPDATE SET
  category = EXCLUDED.category,
  insight_text = EXCLUDED.insight_text,
  confidence = EXCLUDED.confidence,
  status = EXCLUDED.status,
  reviewed_by = EXCLUDED.reviewed_by,
  review_notes = EXCLUDED.review_notes;

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
