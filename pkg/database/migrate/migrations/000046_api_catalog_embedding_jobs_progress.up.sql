-- 000046: api_catalog_embedding_jobs.embedded_so_far
--
-- Adds the per-job in-flight progress counter the worker bumps at
-- chunk boundaries during a long embed pass. Before this column the
-- only progress signal a UI could read was COUNT(*) on
-- api_catalog_operation_embeddings, which sits at 0 until the
-- single worker transaction commits at the end of the run (the
-- catalog store does DELETE ... ; INSERT ... ; COMMIT atomically
-- to preserve the all-or-nothing rewrite semantic across a model
-- swap). For a spec whose embedding pass takes minutes against a
-- CPU-only embedder, the operator saw "indexing 0/N" the entire
-- run and then a one-tick snap to "N/N" at completion (#430).
--
-- The counter is separate from embedding_count specifically so the
-- atomicity of the final write stays intact. The worker writes
-- embedded_so_far at every chunk boundary so the catalog status
-- endpoint can render "running, 64/164" distinct from "queued,
-- 0/164". The final UPSERT still happens in one transaction, and
-- once it commits embedding_count == operation_count and this
-- counter is irrelevant; reconciler / reaper logic does not depend
-- on the value.
--
-- DEFAULT 0 so the column is populated for any pre-existing
-- pending rows (a reconciler-enqueued backlog from before the
-- upgrade). NOT NULL makes the worker's UPDATE path a simple SET
-- without a coalesce.

ALTER TABLE api_catalog_embedding_jobs
    ADD COLUMN IF NOT EXISTS embedded_so_far INTEGER NOT NULL DEFAULT 0;
