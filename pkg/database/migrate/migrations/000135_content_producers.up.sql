-- content_producers records what wrote a file: the scripts, sessions and people
-- that produced or modified a portal asset or a managed resource (#1569).
--
-- Nothing on the platform could answer that before, in either direction. An
-- asset's provenance column answers a different question -- which data calls
-- the content was built from -- and cannot be read backwards without scanning
-- every asset's JSON. A script's link to its own outputs was the asset's
-- idempotency_key, one string per (script, output name) that nothing joins on
-- and that says nothing about an asset the run modified without declaring. A
-- resource recorded its writer as uploader_sub, which for a run is the script's
-- NAME, so renaming the script severed the link.
--
-- The relation is many-to-many by nature: one script writes many files, and one
-- file is written by many producers over its life. A person editing a report a
-- script also refreshes is the ordinary case.
--
-- target_kind is part of the primary key for the reason it is in
-- portal_asset_refs: asset ids and resource ids are separate id spaces, so the
-- same string can name one of each, and two rows about different files must not
-- collide. producer_kind is in the key for the same reason.
--
-- There is deliberately NO foreign key to scripts(id), portal_assets(id) or
-- resources(id). Deleting a script must leave behind the record that it wrote
-- this file rather than silently erasing it -- the same reasoning
-- portal_asset_refs applies to a deleted resource. The producer's name is kept
-- alongside its id so a surface can still say WHICH script that was after the
-- script is gone; the id remains the identity, so a rename changes the label
-- and nothing else.
CREATE TABLE IF NOT EXISTS content_producers (
    target_kind    TEXT        NOT NULL CHECK (target_kind IN ('asset', 'resource')),
    target_id      TEXT        NOT NULL,
    producer_kind  TEXT        NOT NULL CHECK (producer_kind IN ('script', 'session', 'person')),
    producer_id    TEXT        NOT NULL,
    -- producer_label is what the producer was called at the time it wrote: a
    -- script's name, a person's address. Display only, and empty for a session,
    -- whose id is its own label.
    producer_label TEXT        NOT NULL DEFAULT '',
    -- created distinguishes the producer that brought the file into existence
    -- from every producer that has only changed it since. It is set on the row
    -- that records the create and never cleared: a later modification by the
    -- same producer must not demote it to a modifier.
    created        BOOLEAN     NOT NULL DEFAULT FALSE,
    first_write_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_write_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    write_count    INTEGER     NOT NULL DEFAULT 1,
    -- last_version is the target's version this producer last wrote. Zero for a
    -- target whose kind does not number its writes.
    last_version   INTEGER     NOT NULL DEFAULT 0,
    PRIMARY KEY (target_kind, target_id, producer_kind, producer_id)
);

-- "What wrote this file?" reads by target, which the primary key's leading
-- columns already serve. "What has this script written?" reads by producer,
-- newest first, and needs its own index.
CREATE INDEX IF NOT EXISTS idx_content_producers_by_producer
    ON content_producers (producer_kind, producer_id, last_write_at DESC);

-- Backfill from the two signals already in the schema that are unambiguous.
-- Anything else is left out: a history that was never recorded is not
-- reconstructed by guessing, and a wrong producer is worse than none.

-- An asset whose idempotency_key is script:<script id>:<output name> was
-- created by that script's output writer and by nothing else, so the row is a
-- create. The id sits between the first and second colon; a script id is a
-- UUID, so split_part on the second field is exact whatever the output is
-- named. current_version is used as the write count because every version of a
-- script-output asset came from a run of it.
INSERT INTO content_producers (
    target_kind, target_id, producer_kind, producer_id, producer_label,
    created, first_write_at, last_write_at, write_count, last_version
)
SELECT 'asset', a.id, 'script', split_part(a.idempotency_key, ':', 2),
       COALESCE(s.name, ''),
       TRUE, a.created_at, a.updated_at, GREATEST(a.current_version, 1), a.current_version
FROM portal_assets a
LEFT JOIN scripts s ON s.id::text = split_part(a.idempotency_key, ':', 2)
WHERE a.idempotency_key LIKE 'script:%:%'
  AND split_part(a.idempotency_key, ':', 2) <> ''
ON CONFLICT DO NOTHING;

-- A resource uploaded by a run carries uploader_sub = script:<name>, which is
-- resolvable to an id only where exactly one script bears that name. Where two
-- owners each keep a script of the same name the link is genuinely ambiguous
-- and the row is left out rather than guessed at.
INSERT INTO content_producers (
    target_kind, target_id, producer_kind, producer_id, producer_label,
    created, first_write_at, last_write_at, write_count, last_version
)
SELECT 'resource', r.id, 'script', s.id::text, s.name,
       TRUE, r.created_at, r.updated_at, 1, 0
FROM resources r
JOIN scripts s ON s.name = substring(r.uploader_sub FROM 8)
WHERE r.uploader_sub LIKE 'script:%'
  AND (SELECT COUNT(*) FROM scripts s2 WHERE s2.name = substring(r.uploader_sub FROM 8)) = 1
ON CONFLICT DO NOTHING;
