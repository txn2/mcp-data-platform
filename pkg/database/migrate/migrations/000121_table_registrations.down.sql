-- Reverse 000121. The record of every registration is discarded; the external
-- tables themselves stay in Trino, because the platform never owned the data
-- they point at and dropping them here would be a write to another system on
-- the strength of a schema rollback. An operator reversing this migration
-- drops what is left in the scratch schema by hand.
DROP INDEX IF EXISTS table_registrations_source_idx;
DROP INDEX IF EXISTS table_registrations_name_key;
DROP TABLE IF EXISTS table_registrations;
