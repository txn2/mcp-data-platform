-- #2651: acknowledgement latency is counted with bucket edges at 1.5, 2 and 3
-- seconds, around the timeouts senders give up at. The new counts are kept in
-- ack_buckets (15 buckets: 10, 25, 50, 100, 250, 500, 1000, 1500, 2000, 2500,
-- 3000, 5000, 10000, 30000 ms and slower). buckets keeps the 12 of the old
-- edges, which a replica of the previous release still writes during a
-- rolling deploy, so a minute may hold counts in both.
ALTER TABLE webhook_ack_latency ADD COLUMN IF NOT EXISTS ack_buckets BIGINT[] NOT NULL DEFAULT '{}';
-- A row this release writes holds its counts in ack_buckets alone.
ALTER TABLE webhook_ack_latency ALTER COLUMN buckets SET DEFAULT '{}';

-- webhook_ack_counts is a row's counts in the 15 buckets: ack_buckets, plus
-- the old 12 placed in the new bucket at the top of each old one, so an old
-- count is never read as faster than it was ((1000, 2500] lands in
-- (2000, 2500], (2500, 5000] in (3000, 5000]).
CREATE OR REPLACE FUNCTION webhook_ack_counts(old BIGINT[], fine BIGINT[]) RETURNS BIGINT[]
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
  SELECT ARRAY(
    SELECT COALESCE(fine[i], 0)
         + CASE WHEN cardinality(old) = 12
                THEN COALESCE(old[(ARRAY[1, 2, 3, 4, 5, 6, 7, NULL, NULL, 8, NULL, 9, 10, 11, 12]::int[])[i]], 0)
                ELSE 0 END
      FROM generate_series(1, 15) AS i
     ORDER BY i)
$$;
