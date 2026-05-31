-- Issue #515: unify knowledge-insight ownership on user email.
--
-- capture_insight historically stored the OIDC subject (or "apikey:<name>")
-- in knowledge_insights.captured_by, which the 000031 migration carried into
-- memory_records.created_by. memory_manage and the portal both scope by email
-- (memory_records.created_by is documented as "user email"), so insight rows
-- keyed by subject never matched their owner in the portal: the owner saw an
-- empty knowledge list.
--
-- Going forward capture_insight writes the email. This backfills existing
-- knowledge-dimension rows whose created_by is a non-email identifier (no "@")
-- to the email last seen for that subject in audit_logs. The original value is
-- stashed in metadata.legacy_created_by so the change is reversible. Rows with
-- no audit mapping are left unchanged (no worse than before); rows already
-- keyed by email are skipped by the NOT LIKE '%@%' guard.
UPDATE memory_records m
SET created_by = sub.user_email,
    metadata = jsonb_set(m.metadata, '{legacy_created_by}', to_jsonb(m.created_by), true)
FROM (
    SELECT DISTINCT ON (user_id) user_id, user_email
    FROM audit_logs
    WHERE user_id IS NOT NULL AND user_id <> ''
      AND user_email IS NOT NULL AND user_email <> ''
    ORDER BY user_id, timestamp DESC
) sub
WHERE m.dimension = 'knowledge'
  AND m.created_by = sub.user_id
  AND m.created_by NOT LIKE '%@%';
