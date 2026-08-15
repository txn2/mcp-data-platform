-- 000103: prompt-to-script references (#1289).
--
-- A prompt references the managed scripts its procedure depends on: the report
-- the analysis reads, the export it compares against. Serving the prompt hands
-- the agent each script's contract and its latest results, and the instruction
-- to call run_script for fresh ones. Serving never executes a script — a prompt
-- read is a read path, and executing side-effecting code from it would blur
-- audit attribution and turn every read into a potential asset write.
--
-- The row stores the script's canonical reference (mcp:script:<id>, #1302)
-- rather than a bare id, because that is the platform's one way to name a
-- script from outside pkg/script: the same string search emits on a hit, fetch
-- dereferences, and an agent can carry between the two. Storing an id here
-- would be a second, prompt-private way to say the same thing.
--
-- The reference resolves by id, so renaming a script leaves every attachment
-- intact.
--
-- It parallels prompt_resource_attachments (000088) rather than extending it.
-- Now that every internal entity has a reference, ONE attachment table keyed by
-- that reference would be the tidier end state, and it is deliberately not what
-- this migration does: converting the resource table would have to survive a
-- rolling upgrade in which an old replica is still WRITING resource_id while a
-- new one reads references, which is the direction a migration alone cannot fix.
-- What must not be duplicated is the RULE, and it is not: one audience test
-- (prompt.CheckAttachScope) governs both kinds.
--
-- The table inherits 000088's two deliberate decisions:
--
--   * NO foreign key to scripts(id). Deleting a script must leave the row
--     behind so the prompt still serves and both the served payload and the
--     portal can flag the reference as broken. A cascading delete would erase
--     the evidence that the procedure is now incomplete.
--   * prompt_id DOES cascade: an attachment is meaningless without its prompt.

CREATE TABLE IF NOT EXISTS prompt_script_attachments (
    prompt_id    UUID        NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    script_ref   TEXT        NOT NULL,
    position     INTEGER     NOT NULL DEFAULT 0,
    attached_by  TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (prompt_id, script_ref)
);

-- Serving reads one prompt's referenced scripts in authored order on every
-- prompts/get and manage_prompt use.
CREATE INDEX IF NOT EXISTS idx_prompt_script_attachments_order
    ON prompt_script_attachments(prompt_id, position);
