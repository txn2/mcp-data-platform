-- The catalog the seeded knowledge pages cite (dev/seed.sql): nine datasets on
-- the dev Trino connection "acme", with their columns, descriptions, tags,
-- owners and properties, and the tags they carry. A scan of a live "acme" that
-- does not hold them marks them removed; their curation stays readable.
-- Idempotent: every row is keyed by its reference, and a re-run writes nothing
-- new.
INSERT INTO catalog_entities
    (id, kind, reference, connection_kind, connection_name, path, column_name, parent_id, name, dataset_type, data_type, ordinal, nullable)
VALUES
    ('f36cdd22-869f-5dbd-beed-4ba128fd8718', 'dataset', 'mcp:dataset:(trino,acme,iceberg.retail.daily_sales)', 'trino', 'acme', 'iceberg.retail.daily_sales', '', NULL, 'daily_sales', 'table', '', 0, TRUE),
    ('a83f84ac-dfbb-51b5-be4a-f9f1bfc4f19f', 'column', 'mcp:column:(trino,acme,iceberg.retail.daily_sales,date)', 'trino', 'acme', 'iceberg.retail.daily_sales', 'date', 'f36cdd22-869f-5dbd-beed-4ba128fd8718', 'date', '', 'date', 0, TRUE),
    ('3af534d1-baf8-5565-87b1-00a3858c2b5c', 'column', 'mcp:column:(trino,acme,iceberg.retail.daily_sales,store_id)', 'trino', 'acme', 'iceberg.retail.daily_sales', 'store_id', 'f36cdd22-869f-5dbd-beed-4ba128fd8718', 'store_id', '', 'varchar', 1, TRUE),
    ('b715c4f3-7b6b-5c38-a456-3eda62179337', 'column', 'mcp:column:(trino,acme,iceberg.retail.daily_sales,amount)', 'trino', 'acme', 'iceberg.retail.daily_sales', 'amount', 'f36cdd22-869f-5dbd-beed-4ba128fd8718', 'amount', '', 'decimal(18,2)', 2, TRUE),
    ('963333e2-3cc7-5e36-b190-17a8b2890592', 'column', 'mcp:column:(trino,acme,iceberg.retail.daily_sales,net_revenue)', 'trino', 'acme', 'iceberg.retail.daily_sales', 'net_revenue', 'f36cdd22-869f-5dbd-beed-4ba128fd8718', 'net_revenue', '', 'decimal(18,2)', 3, TRUE),
    ('68af6a46-284b-5a7e-a9ee-648d83238425', 'column', 'mcp:column:(trino,acme,iceberg.retail.daily_sales,channel)', 'trino', 'acme', 'iceberg.retail.daily_sales', 'channel', 'f36cdd22-869f-5dbd-beed-4ba128fd8718', 'channel', '', 'varchar', 4, TRUE),
    ('553b37fa-2613-51a7-8594-5ce0ad0690e4', 'dataset', 'mcp:dataset:(trino,acme,iceberg.retail.sales_mart)', 'trino', 'acme', 'iceberg.retail.sales_mart', '', NULL, 'sales_mart', 'table', '', 0, TRUE),
    ('cba1e75a-dddb-5832-89c9-90cf88215a6e', 'dataset', 'mcp:dataset:(trino,acme,iceberg.retail.refunds)', 'trino', 'acme', 'iceberg.retail.refunds', '', NULL, 'refunds', 'table', '', 0, TRUE),
    ('9bb6c056-3345-5137-bbe8-3c9cdd88552a', 'column', 'mcp:column:(trino,acme,iceberg.retail.refunds,amount)', 'trino', 'acme', 'iceberg.retail.refunds', 'amount', 'cba1e75a-dddb-5832-89c9-90cf88215a6e', 'amount', '', 'decimal(18,2)', 0, TRUE),
    ('db838fc4-f1ed-5b70-a09a-93afe755f0b3', 'column', 'mcp:column:(trino,acme,iceberg.retail.refunds,reason_code)', 'trino', 'acme', 'iceberg.retail.refunds', 'reason_code', 'cba1e75a-dddb-5832-89c9-90cf88215a6e', 'reason_code', '', 'varchar', 1, TRUE),
    ('598dd0b4-2218-53e1-95a6-5b8f2164f0fc', 'dataset', 'mcp:dataset:(trino,acme,iceberg.retail.price_adjustments)', 'trino', 'acme', 'iceberg.retail.price_adjustments', '', NULL, 'price_adjustments', 'table', '', 0, TRUE),
    ('b62c9a7d-357b-5db5-bb95-a8d216b8cad0', 'dataset', 'mcp:dataset:(trino,acme,iceberg.retail.store_transactions)', 'trino', 'acme', 'iceberg.retail.store_transactions', '', NULL, 'store_transactions', 'table', '', 0, TRUE),
    ('2b496a5e-f61c-5773-b27d-3bbdd2115766', 'dataset', 'mcp:dataset:(trino,acme,iceberg.inventory.inventory_levels)', 'trino', 'acme', 'iceberg.inventory.inventory_levels', '', NULL, 'inventory_levels', 'table', '', 0, TRUE),
    ('ae765cff-e791-540d-9f2d-3463f2f1241d', 'column', 'mcp:column:(trino,acme,iceberg.inventory.inventory_levels,sku)', 'trino', 'acme', 'iceberg.inventory.inventory_levels', 'sku', '2b496a5e-f61c-5773-b27d-3bbdd2115766', 'sku', '', 'varchar', 0, TRUE),
    ('a492a5a9-3af0-5af9-82c3-90ea45ca9202', 'column', 'mcp:column:(trino,acme,iceberg.inventory.inventory_levels,on_hand)', 'trino', 'acme', 'iceberg.inventory.inventory_levels', 'on_hand', '2b496a5e-f61c-5773-b27d-3bbdd2115766', 'on_hand', '', 'integer', 1, TRUE),
    ('424c8f06-2f8c-5c12-a998-7c72f9b93b9c', 'dataset', 'mcp:dataset:(trino,acme,iceberg.inventory.supply_chain_orders)', 'trino', 'acme', 'iceberg.inventory.supply_chain_orders', '', NULL, 'supply_chain_orders', 'table', '', 0, TRUE),
    ('0f962166-de2d-552d-b12c-a767237eba3b', 'dataset', 'mcp:dataset:(trino,acme,iceberg.analytics.customer_segments)', 'trino', 'acme', 'iceberg.analytics.customer_segments', '', NULL, 'customer_segments', 'table', '', 0, TRUE),
    ('92775c46-350f-5b53-b894-889951ea7fbd', 'column', 'mcp:column:(trino,acme,iceberg.analytics.customer_segments,customer_id)', 'trino', 'acme', 'iceberg.analytics.customer_segments', 'customer_id', '0f962166-de2d-552d-b12c-a767237eba3b', 'customer_id', '', 'varchar', 0, TRUE),
    ('e73e1690-fc99-5ef0-a74e-eb4f102fd8bf', 'column', 'mcp:column:(trino,acme,iceberg.analytics.customer_segments,is_current)', 'trino', 'acme', 'iceberg.analytics.customer_segments', 'is_current', '0f962166-de2d-552d-b12c-a767237eba3b', 'is_current', '', 'boolean', 1, TRUE),
    ('f0fd6f60-7975-5412-b69d-6f728d3782f9', 'column', 'mcp:column:(trino,acme,iceberg.analytics.customer_segments,email_masked)', 'trino', 'acme', 'iceberg.analytics.customer_segments', 'email_masked', '0f962166-de2d-552d-b12c-a767237eba3b', 'email_masked', '', 'varchar', 2, TRUE),
    ('cc269bbf-b655-576e-a172-22f18b327f17', 'dataset', 'mcp:dataset:(trino,acme,iceberg.analytics.regional_performance)', 'trino', 'acme', 'iceberg.analytics.regional_performance', '', NULL, 'regional_performance', 'table', '', 0, TRUE),
    ('f34f2fb8-0a89-5901-8295-24f79c003352', 'tag', 'mcp:tag:PII', '', '', '', '', NULL, 'PII', '', '', 0, TRUE),
    ('98d7bf26-b5e5-52d6-8588-d8e12221479c', 'tag', 'mcp:tag:analytics', '', '', '', '', NULL, 'analytics', '', '', 0, TRUE),
    ('37d4ab09-ad2d-5a17-946e-8cfc7e213d99', 'tag', 'mcp:tag:finance', '', '', '', '', NULL, 'finance', '', '', 0, TRUE),
    ('1535a842-5bb9-529d-88bb-91b32af47c2b', 'tag', 'mcp:tag:inventory', '', '', '', '', NULL, 'inventory', '', '', 0, TRUE),
    ('4ee6efe2-ba67-5d3e-929d-b81895d657b7', 'tag', 'mcp:tag:pricing', '', '', '', '', NULL, 'pricing', '', '', 0, TRUE),
    ('53f8de04-ec49-50f3-b7a1-8614fa37a6b8', 'tag', 'mcp:tag:reporting', '', '', '', '', NULL, 'reporting', '', '', 0, TRUE),
    ('224ef996-8018-5784-9333-886cd5dbcbf5', 'tag', 'mcp:tag:retail', '', '', '', '', NULL, 'retail', '', '', 0, TRUE),
    ('9bbcafc5-7e97-5560-886d-eecfba6a40f7', 'tag', 'mcp:tag:revenue', '', '', '', '', NULL, 'revenue', '', '', 0, TRUE),
    ('0031950a-a15d-56f3-995e-76d8b1bdfc09', 'tag', 'mcp:tag:supply-chain', '', '', '', '', NULL, 'supply-chain', '', '', 0, TRUE)
ON CONFLICT (reference) DO NOTHING;

UPDATE catalog_entities SET slug = name WHERE kind = 'tag' AND slug = '';

INSERT INTO catalog_aspects (entity_id, aspect, value, version, written_by) VALUES
    ('f36cdd22-869f-5dbd-beed-4ba128fd8718', 'description', '"One row per store per day, partitioned by date in UTC. The primary source of truth for revenue reporting. Backfills land about two days late, so the most recent 48 hours are fresh but not final."'::jsonb, 1, 'seed'),
    ('f36cdd22-869f-5dbd-beed-4ba128fd8718', 'tags', '["retail","revenue"]'::jsonb, 1, 'seed'),
    ('f36cdd22-869f-5dbd-beed-4ba128fd8718', 'owners', '[{"owner":"data-eng","type":"data_owner"}]'::jsonb, 1, 'seed'),
    ('f36cdd22-869f-5dbd-beed-4ba128fd8718', 'custom_properties', '{"grain":"store/day","partitioned_by":"date","sla":"06:00 UTC"}'::jsonb, 1, 'seed'),
    ('a83f84ac-dfbb-51b5-be4a-f9f1bfc4f19f', 'description', '"Partition key, in UTC."'::jsonb, 1, 'seed'),
    ('3af534d1-baf8-5565-87b1-00a3858c2b5c', 'description', '"Store identifier. Join key to the store dimension."'::jsonb, 1, 'seed'),
    ('b715c4f3-7b6b-5c38-a456-3eda62179337', 'description', '"Gross margin before returns, NOT gross revenue. Use net_revenue for top-line reporting."'::jsonb, 1, 'seed'),
    ('963333e2-3cc7-5e36-b190-17a8b2890592', 'description', '"Gross sales minus returns, discounts, and tax. The figure used in board-level reporting."'::jsonb, 1, 'seed'),
    ('68af6a46-284b-5a7e-a9ee-648d83238425', 'description', '"Controlled vocabulary; the free-text values were retired in FY2025."'::jsonb, 1, 'seed'),
    ('553b37fa-2613-51a7-8594-5ce0ad0690e4', 'description', '"Reporting mart built from daily_sales with cleanup and conformance already applied, so downstream reporting does not repeat it. Feeds the executive dashboard."'::jsonb, 1, 'seed'),
    ('553b37fa-2613-51a7-8594-5ce0ad0690e4', 'tags', '["retail","reporting"]'::jsonb, 1, 'seed'),
    ('553b37fa-2613-51a7-8594-5ce0ad0690e4', 'owners', '[{"owner":"data-eng","type":"data_owner"}]'::jsonb, 1, 'seed'),
    ('553b37fa-2613-51a7-8594-5ce0ad0690e4', 'custom_properties', '{"upstream":"iceberg.retail.daily_sales","sla":"06:00 UTC"}'::jsonb, 1, 'seed'),
    ('cba1e75a-dddb-5832-89c9-90cf88215a6e', 'description', '"Returns and refunds. Each row carries a negative amount and a reason_code. Returns net against revenue in the reporting layer, not at ingest."'::jsonb, 1, 'seed'),
    ('cba1e75a-dddb-5832-89c9-90cf88215a6e', 'tags', '["retail","finance"]'::jsonb, 1, 'seed'),
    ('cba1e75a-dddb-5832-89c9-90cf88215a6e', 'owners', '[{"owner":"finance-eng","type":"data_owner"}]'::jsonb, 1, 'seed'),
    ('cba1e75a-dddb-5832-89c9-90cf88215a6e', 'custom_properties', '{"sign_convention":"negative amount"}'::jsonb, 1, 'seed'),
    ('9bb6c056-3345-5137-bbe8-3c9cdd88552a', 'description', '"Always negative."'::jsonb, 1, 'seed'),
    ('db838fc4-f1ed-5b70-a09a-93afe755f0b3', 'description', '"Why the item was returned."'::jsonb, 1, 'seed'),
    ('598dd0b4-2218-53e1-95a6-5b8f2164f0fc', 'description', '"Every list-price change with an effective timestamp. To get the price in effect on a date, take the most recent row at or before that date."'::jsonb, 1, 'seed'),
    ('598dd0b4-2218-53e1-95a6-5b8f2164f0fc', 'tags', '["retail","pricing"]'::jsonb, 1, 'seed'),
    ('598dd0b4-2218-53e1-95a6-5b8f2164f0fc', 'owners', '[{"owner":"data-eng","type":"data_owner"}]'::jsonb, 1, 'seed'),
    ('598dd0b4-2218-53e1-95a6-5b8f2164f0fc', 'custom_properties', '{"grain":"price change"}'::jsonb, 1, 'seed'),
    ('b62c9a7d-357b-5db5-bb95-a8d216b8cad0', 'description', '"Raw point-of-sale transactions, one row per line item. daily_sales aggregates from here; it keeps only current state, with history in the events table."'::jsonb, 1, 'seed'),
    ('b62c9a7d-357b-5db5-bb95-a8d216b8cad0', 'tags', '["retail"]'::jsonb, 1, 'seed'),
    ('b62c9a7d-357b-5db5-bb95-a8d216b8cad0', 'owners', '[{"owner":"data-eng","type":"data_owner"}]'::jsonb, 1, 'seed'),
    ('b62c9a7d-357b-5db5-bb95-a8d216b8cad0', 'custom_properties', '{"grain":"line item"}'::jsonb, 1, 'seed'),
    ('2b496a5e-f61c-5773-b27d-3bbdd2115766', 'description', '"One row per SKU per store per day. on_hand is the end-of-day position; intraday movements are not captured here."'::jsonb, 1, 'seed'),
    ('2b496a5e-f61c-5773-b27d-3bbdd2115766', 'tags', '["inventory"]'::jsonb, 1, 'seed'),
    ('2b496a5e-f61c-5773-b27d-3bbdd2115766', 'owners', '[{"owner":"supply-chain","type":"data_owner"}]'::jsonb, 1, 'seed'),
    ('2b496a5e-f61c-5773-b27d-3bbdd2115766', 'custom_properties', '{"grain":"sku/store/day"}'::jsonb, 1, 'seed'),
    ('ae765cff-e791-540d-9f2d-3463f2f1241d', 'description', '"Stock keeping unit."'::jsonb, 1, 'seed'),
    ('a492a5a9-3af0-5af9-82c3-90ea45ca9202', 'description', '"End-of-day position, not a running balance."'::jsonb, 1, 'seed'),
    ('424c8f06-2f8c-5c12-a998-7c72f9b93b9c', 'description', '"Purchase orders to suppliers, with placed and expected dates. Lead time is measured from placement to receipt, not to the expected date."'::jsonb, 1, 'seed'),
    ('424c8f06-2f8c-5c12-a998-7c72f9b93b9c', 'tags', '["inventory","supply-chain"]'::jsonb, 1, 'seed'),
    ('424c8f06-2f8c-5c12-a998-7c72f9b93b9c', 'owners', '[{"owner":"supply-chain","type":"data_owner"}]'::jsonb, 1, 'seed'),
    ('424c8f06-2f8c-5c12-a998-7c72f9b93b9c', 'custom_properties', '{"grain":"purchase order"}'::jsonb, 1, 'seed'),
    ('0f962166-de2d-552d-b12c-a767237eba3b', 'description', '"A Type-2 slowly changing dimension: filter is_current = true for the latest segment assignment. Refreshed daily from the ML pipeline and feeds regional_performance via a nightly ETL job. Masked columns carry a _masked suffix."'::jsonb, 1, 'seed'),
    ('0f962166-de2d-552d-b12c-a767237eba3b', 'tags', '["PII","analytics"]'::jsonb, 1, 'seed'),
    ('0f962166-de2d-552d-b12c-a767237eba3b', 'owners', '[{"owner":"analytics-eng","type":"data_owner"}]'::jsonb, 1, 'seed'),
    ('0f962166-de2d-552d-b12c-a767237eba3b', 'custom_properties', '{"scd":"type 2","current_flag":"is_current"}'::jsonb, 1, 'seed'),
    ('92775c46-350f-5b53-b894-889951ea7fbd', 'description', '"Join key. Route through the identity map to avoid double-counting."'::jsonb, 1, 'seed'),
    ('e73e1690-fc99-5ef0-a74e-eb4f102fd8bf', 'description', '"Type-2 current-row flag. Filter on this for the latest assignment."'::jsonb, 1, 'seed'),
    ('f0fd6f60-7975-5412-b69d-6f728d3782f9', 'description', '"Masked PII; the _masked suffix marks every masked column."'::jsonb, 1, 'seed'),
    ('cc269bbf-b655-576e-a172-22f18b327f17', 'description', '"Regional roll-up fed nightly from customer_segments. Regions follow the reporting hierarchy, not the store''s postal region."'::jsonb, 1, 'seed'),
    ('cc269bbf-b655-576e-a172-22f18b327f17', 'tags', '["analytics","reporting"]'::jsonb, 1, 'seed'),
    ('cc269bbf-b655-576e-a172-22f18b327f17', 'owners', '[{"owner":"analytics-eng","type":"data_owner"}]'::jsonb, 1, 'seed'),
    ('cc269bbf-b655-576e-a172-22f18b327f17', 'custom_properties', '{"upstream":"iceberg.analytics.customer_segments"}'::jsonb, 1, 'seed')
ON CONFLICT (entity_id, aspect) DO NOTHING;

-- A table tagged PII that the dev Trino holds, so a query against it runs:
-- the Trino toolkit asks for consent before reading a table the catalog tags
-- PII (#2052). Its rows live in Trino's memory catalog, which starts empty, so
-- the criterion that reads it creates them; the tag is the catalog's.
INSERT INTO catalog_entities
    (id, kind, reference, connection_kind, connection_name, path, column_name, parent_id, name, dataset_type, data_type, ordinal, nullable)
VALUES
    ('44768b24-8270-590a-85ad-bd0fadecaa6b', 'dataset', 'mcp:dataset:(trino,acme,memory.default.customers)', 'trino', 'acme', 'memory.default.customers', '', NULL, 'customers', 'table', '', 0, TRUE)
ON CONFLICT (reference) DO NOTHING;

INSERT INTO catalog_entities
    (id, kind, reference, connection_kind, connection_name, path, column_name, parent_id, name, dataset_type, data_type, ordinal, nullable)
SELECT 'e458dbf5-217e-550e-b66f-5a4a96c95236', 'column', 'mcp:column:(trino,acme,memory.default.customers,email)', 'trino', 'acme', 'memory.default.customers', 'email', id, 'email', '', 'varchar', 0, TRUE
FROM catalog_entities WHERE reference = 'mcp:dataset:(trino,acme,memory.default.customers)'
ON CONFLICT (reference) DO NOTHING;

INSERT INTO catalog_aspects (entity_id, aspect, value, version, written_by)
SELECT id, 'tags', '["PII"]'::jsonb, 1, 'seed' FROM catalog_entities WHERE reference = 'mcp:dataset:(trino,acme,memory.default.customers)'
ON CONFLICT (entity_id, aspect) DO NOTHING;

INSERT INTO catalog_aspect_history (entity_id, aspect, version, value, written_by)
SELECT a.entity_id, a.aspect, a.version, a.value, a.written_by FROM catalog_aspects a
WHERE a.written_by = 'seed'
  AND NOT EXISTS (SELECT 1 FROM catalog_aspect_history h WHERE h.entity_id = a.entity_id AND h.aspect = a.aspect);
