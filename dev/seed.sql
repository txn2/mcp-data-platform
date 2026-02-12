-- ACME Corporation Seed Data
--
-- Run AFTER the Go server has started once (to create the schema via migrations).
-- Usage: psql -h localhost -U platform -d mcp_platform -f dev/seed.sql
--
-- This seeds ~5,000 audit events over the past 7 days, 8 knowledge insights,
-- and 2 knowledge changesets with ACME Corporation retail data.

-- ============================================================================
-- Audit Events (~5,000 over 7 days)
-- ============================================================================

-- Helper: weighted random selection via generate_series
-- Uses a deterministic approach with modular arithmetic for reproducibility.

INSERT INTO audit_logs (
  request_id, session_id, user_id, user_email, persona,
  tool_name, toolkit_kind, toolkit_name, connection,
  parameters, success, error_message,
  duration_ms, response_chars, request_chars, content_blocks,
  transport, source, enrichment_applied, authorized,
  created_at
)
SELECT
  'req-' || lpad(n::text, 8, '0'),
  'sess-' || ((n % 50) + 100),
  u.email,
  u.email,
  u.persona,
  t.tool_name,
  t.toolkit_kind,
  t.toolkit_name,
  t.connection,
  -- Realistic parameters as JSONB
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
  -- 96.3% success rate
  (n % 27) != 0,
  -- Error messages for failures
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
  -- Duration varies by tool type
  CASE t.toolkit_kind
    WHEN 'trino' THEN 45 + (n * 7 % 2755)
    WHEN 'datahub' THEN 25 + (n * 3 % 325)
    WHEN 's3' THEN 15 + (n * 5 % 485)
  END,
  200 + (n * 13 % 11800),  -- response_chars
  50 + (n * 7 % 750),      -- request_chars
  1 + (n % 5),             -- content_blocks
  'http',
  'mcp',
  (n % 5) != 0,            -- 80% enrichment rate
  true,
  -- Business-hours weighted timestamps over past 7 days
  -- Peak at 9-11am and 2-4pm, minimal overnight
  NOW() - (
    -- Day offset (0-6 days ago)
    ((n % 7) || ' days')::interval
    -- Hour offset: weighted toward business hours
    + (CASE
        WHEN n % 20 < 1  THEN (n % 3)       -- 5%: midnight-2am
        WHEN n % 20 < 2  THEN 6 + (n % 2)   -- 5%: 6-7am
        WHEN n % 20 < 5  THEN 8 + (n % 2)   -- 15%: 8-9am
        WHEN n % 20 < 9  THEN 9 + (n % 3)   -- 20%: 9-11am
        WHEN n % 20 < 11 THEN 11 + (n % 2)  -- 10%: 11am-12pm
        WHEN n % 20 < 15 THEN 13 + (n % 3)  -- 20%: 1-3pm
        WHEN n % 20 < 18 THEN 14 + (n % 3)  -- 15%: 2-4pm
        ELSE 17 + (n % 3)                    -- 10%: 5-7pm
      END || ' hours')::interval
    + ((n * 17 % 60) || ' minutes')::interval
    + ((n * 31 % 60) || ' seconds')::interval
  )
FROM
  generate_series(1, 5000) AS n,
  -- Weighted user selection: data engineers are most active
  LATERAL (
    SELECT email, persona FROM (VALUES
      ('sarah.chen@acme-corp.com',       'admin',              8),
      ('marcus.johnson@acme-corp.com',   'data-engineer',     15),
      ('rachel.thompson@acme-corp.com',  'inventory-analyst', 12),
      ('david.park@acme-corp.com',       'regional-director',  6),
      ('jennifer.martinez@acme-corp.com','finance-executive',   5),
      ('kevin.wilson@acme-corp.com',     'store-manager',       7),
      ('amanda.lee@acme-corp.com',       'data-engineer',      14),
      ('carlos.rodriguez@acme-corp.com', 'regional-director',   6),
      ('emily.watson@acme-corp.com',     'inventory-analyst',  10),
      ('brian.taylor@acme-corp.com',     'finance-executive',   4),
      ('lisa.chang@acme-corp.com',       'data-engineer',      11),
      ('mike.davis@acme-corp.com',       'store-manager',       3)
    ) AS users(email, persona, weight)
    ORDER BY weight DESC
    OFFSET (n * 7 % 12)
    LIMIT 1
  ) AS u,
  -- Weighted tool selection: trino_query is most common
  LATERAL (
    SELECT tool_name, toolkit_kind, toolkit_name, connection FROM (VALUES
      ('trino_query',              'trino',   'acme-warehouse',        'acme-warehouse',         22),
      ('trino_describe_table',     'trino',   'acme-warehouse',        'acme-warehouse',          7),
      ('trino_list_tables',        'trino',   'acme-warehouse',        'acme-warehouse',          3),
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
-- Knowledge Insights (8 in various states)
-- ============================================================================

INSERT INTO knowledge_insights (
  category, insight_text, confidence, entity_urns, related_columns,
  suggested_actions, status, review_notes, created_at
) VALUES
(
  'business_context',
  'The daily_sales table in the retail schema is the primary source of truth for all revenue reporting. The revenue column uses post-discount, pre-tax amounts in USD.',
  'high',
  ARRAY['urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)'],
  '[{"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)", "column": "revenue", "relevance": "primary"}]'::jsonb,
  '[{"action_type": "update_description", "target": "iceberg.retail.daily_sales.revenue", "detail": "Post-discount, pre-tax revenue in USD"}]'::jsonb,
  'approved',
  'Verified with finance team - this is correct.',
  NOW() - interval '5 days'
),
(
  'data_quality',
  'The inventory_levels table has NULL values in the reorder_point column for approximately 12% of SKUs, primarily in the seasonal-goods category.',
  'high',
  ARRAY['urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.inventory.inventory_levels,PROD)'],
  '[{"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.inventory.inventory_levels,PROD)", "column": "reorder_point", "relevance": "primary"}]'::jsonb,
  '[{"action_type": "add_tag", "target": "iceberg.inventory.inventory_levels.reorder_point", "detail": "has_nulls_seasonal"}]'::jsonb,
  'approved',
  'Known issue - seasonal items have dynamic reorder points calculated separately.',
  NOW() - interval '4 days'
),
(
  'usage_tip',
  'When querying regional_performance, always filter by fiscal_quarter rather than calendar_quarter. ACME fiscal year starts February 1.',
  'high',
  ARRAY['urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.analytics.regional_performance,PROD)'],
  '[]'::jsonb,
  '[{"action_type": "update_description", "target": "iceberg.analytics.regional_performance", "detail": "Uses ACME fiscal calendar (FY starts Feb 1). Filter by fiscal_quarter for accurate period comparisons."}]'::jsonb,
  'applied',
  'Applied to DataHub description.',
  NOW() - interval '3 days'
),
(
  'relationship',
  'The customer_segments table is refreshed daily from the ML pipeline and feeds into regional_performance via a nightly ETL job. Segments are based on 90-day purchase history.',
  'medium',
  ARRAY[
    'urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.analytics.customer_segments,PROD)',
    'urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.analytics.regional_performance,PROD)'
  ],
  '[]'::jsonb,
  '[]'::jsonb,
  'pending',
  NULL,
  NOW() - interval '2 days'
),
(
  'correction',
  'The store_transactions.discount_pct column is stored as a decimal (0.15 = 15%), not as a percentage integer. Several downstream reports incorrectly multiply by 100.',
  'high',
  ARRAY['urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.store_transactions,PROD)'],
  '[{"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.store_transactions,PROD)", "column": "discount_pct", "relevance": "primary"}]'::jsonb,
  '[{"action_type": "update_description", "target": "iceberg.retail.store_transactions.discount_pct", "detail": "Decimal format: 0.15 means 15% discount. Do NOT multiply by 100."}]'::jsonb,
  'pending',
  NULL,
  NOW() - interval '1 day'
),
(
  'business_context',
  'Supply chain orders with status BACKORDER should be excluded from current inventory calculations. They represent items ordered but not yet received from vendors.',
  'high',
  ARRAY['urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.inventory.supply_chain_orders,PROD)'],
  '[{"urn": "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.inventory.supply_chain_orders,PROD)", "column": "status", "relevance": "primary"}]'::jsonb,
  '[]'::jsonb,
  'applied',
  'Added as a data quality rule in the enrichment layer.',
  NOW() - interval '6 days'
),
(
  'data_quality',
  'The price_adjustments table contains duplicate entries for Black Friday 2024 promotions (Nov 29). Use DISTINCT on (store_id, sku, effective_date) to deduplicate.',
  'medium',
  ARRAY['urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.price_adjustments,PROD)'],
  '[]'::jsonb,
  '[{"action_type": "add_tag", "target": "iceberg.retail.price_adjustments", "detail": "has_duplicates_bf2024"}]'::jsonb,
  'rejected',
  'Duplicates were intentional - separate promotional line items for stackable discounts.',
  NOW() - interval '5 days'
),
(
  'usage_tip',
  'For real-time inventory queries, use the inventory.current_stock view instead of inventory_levels. The view joins with pending_receipts and in_transit tables for accurate on-hand counts.',
  'high',
  ARRAY['urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.inventory.inventory_levels,PROD)'],
  '[]'::jsonb,
  '[]'::jsonb,
  'pending',
  NULL,
  NOW() - interval '12 hours'
);

-- ============================================================================
-- Knowledge Changesets (2: 1 applied, 1 rolled back)
-- ============================================================================

INSERT INTO knowledge_changesets (
  insight_ids, changes, status, applied_by, applied_at, created_at
) VALUES
(
  -- Applied changeset for the fiscal calendar insight
  (SELECT ARRAY[id] FROM knowledge_insights WHERE insight_text LIKE '%fiscal_quarter%' LIMIT 1),
  '[{"change_type": "update_description", "target": "iceberg.analytics.regional_performance", "detail": "Uses ACME fiscal calendar (FY starts Feb 1). Filter by fiscal_quarter for accurate period comparisons."}]'::jsonb,
  'applied',
  'sarah.chen@acme-corp.com',
  NOW() - interval '3 days',
  NOW() - interval '3 days'
),
(
  -- Rolled-back changeset for the supply chain insight
  (SELECT ARRAY[id] FROM knowledge_insights WHERE insight_text LIKE '%BACKORDER%' LIMIT 1),
  '[{"change_type": "add_tag", "target": "iceberg.inventory.supply_chain_orders", "detail": "exclude_backorders_from_inventory"}]'::jsonb,
  'rolled_back',
  'sarah.chen@acme-corp.com',
  NOW() - interval '5 days',
  NOW() - interval '6 days'
);
