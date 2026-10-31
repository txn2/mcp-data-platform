-- Lineage-inheritance premise probe (issue #2379): a mirror of the curated
-- orders table, same columns and rows, as a downstream copy would be. Apply
-- after setup.sql.
DROP TABLE IF EXISTS memory.bench.orders_mirror;
CREATE TABLE memory.bench.orders_mirror AS SELECT * FROM memory.bench.orders;
