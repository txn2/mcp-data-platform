-- 000137: a collection records what created it (#1579)
--
-- content_producers (000135) answers "what wrote this file" for assets and
-- managed resources. A portal collection was left out because nothing needed to
-- read it that way: a collection's owner_id was the only scope its listing had.
--
-- That owner id is not an identity for a managed-script run. A collection a run
-- creates records the run's principal, script:<name>, and idx_scripts_name_owner
-- (000119) is UNIQUE (owner_email, name), so a name is unique only within its
-- OWNER: two people who each keep a daily-sales present one owner id, and a run
-- of one person's script enumerated the collections the other person's script
-- created. The producer id is the script's own uuid, which is unique, survives a
-- rename, and survives a transfer -- none of which the identifiers on the row
-- do, since nothing rewrites a collection's owner columns after the insert.
--
-- The CHECK is the only thing that changes. Every column, index and key already
-- serves the new kind: collection ids are their own id space, which is exactly
-- why target_kind is part of the primary key.
ALTER TABLE content_producers DROP CONSTRAINT IF EXISTS content_producers_target_kind_check;
ALTER TABLE content_producers ADD CONSTRAINT content_producers_target_kind_check
    CHECK (target_kind IN ('asset', 'resource', 'collection'));

-- Backfill the collections a managed script created, from the only signal in
-- the schema that names one unambiguously: a collection whose owner_id is a
-- script principal, resolvable to an id where exactly one script bears that
-- name. Where two owners each keep a script of that name the link is genuinely
-- ambiguous -- which is the defect this migration exists for -- so the row is
-- left out rather than guessed at, exactly as 000135 left the ambiguous
-- resources out.
INSERT INTO content_producers (
    target_kind, target_id, producer_kind, producer_id, producer_label,
    created, first_write_at, last_write_at, write_count, last_version
)
SELECT 'collection', c.id, 'script', s.id::text, s.name,
       TRUE, c.created_at, c.updated_at, 1, 0
FROM portal_collections c
JOIN scripts s ON 'script:' || s.name = c.owner_id
WHERE c.owner_id LIKE 'script:%'
  AND (SELECT COUNT(*) FROM scripts s2 WHERE s2.name = s.name) = 1
ON CONFLICT DO NOTHING;

-- The same backfill for the assets 000135 could not reach. That backfill read
-- the idempotency key, which only a declared `platform.export` output carries;
-- an asset a run created through save_asset or manage_asset records the
-- principal as its owner_id and nothing else. Those rows were enumerated by
-- that owner_id until this ticket moved a run's inventory onto the producer
-- relation, so without this they would leave the inventory of the very script
-- that wrote them.
--
-- Resolvable to an id only where exactly one script bears the name, for the
-- reason 000135 gave: where two owners each keep a script of that name the link
-- is genuinely ambiguous -- which is the defect this ticket is about -- so the
-- row is left out rather than guessed at. ON CONFLICT DO NOTHING leaves the
-- rows 000135 already wrote from the idempotency key exactly as they are.
INSERT INTO content_producers (
    target_kind, target_id, producer_kind, producer_id, producer_label,
    created, first_write_at, last_write_at, write_count, last_version
)
SELECT 'asset', a.id, 'script', s.id::text, s.name,
       TRUE, a.created_at, a.updated_at, GREATEST(a.current_version, 1), a.current_version
FROM portal_assets a
JOIN scripts s ON 'script:' || s.name = a.owner_id
WHERE a.owner_id LIKE 'script:%'
  AND (SELECT COUNT(*) FROM scripts s2 WHERE s2.name = s.name) = 1
ON CONFLICT DO NOTHING;
