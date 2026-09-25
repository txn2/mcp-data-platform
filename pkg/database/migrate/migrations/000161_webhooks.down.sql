DELETE FROM table_registrations WHERE source_kind = 'webhook';
ALTER TABLE table_registrations DROP CONSTRAINT IF EXISTS table_registrations_source_kind_check;
ALTER TABLE table_registrations ADD CONSTRAINT table_registrations_source_kind_check
    CHECK (source_kind IN ('resource', 'asset'));

DROP TABLE IF EXISTS webhook_rejections;
DROP TABLE IF EXISTS webhook_request_counts;
DROP TABLE IF EXISTS webhook_windows;
DROP TABLE IF EXISTS webhook_sources;
