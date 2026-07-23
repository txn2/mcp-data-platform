-- prompt_resource_attachments links a managed resource to a prompt as the
-- reference material the prompt's procedure depends on (#1013): the template it
-- fills, the checklist it follows, the brand header it embeds.
--
-- The link stores a resource id rather than a copy of the file, so editing the
-- uploaded resource updates every prompt that attaches it.
--
-- There is deliberately NO foreign key to resources(id). Deleting a resource
-- must leave the attachment row behind: the prompt still serves, the served
-- result notes the material as missing, and the portal flags the broken link.
-- A cascading delete would erase the evidence that the SOP is now incomplete.
-- Attachments are, however, meaningless without their prompt, so prompt_id
-- cascades.
CREATE TABLE IF NOT EXISTS prompt_resource_attachments (
    prompt_id    UUID        NOT NULL REFERENCES prompts(id) ON DELETE CASCADE,
    resource_id  TEXT        NOT NULL,
    position     INTEGER     NOT NULL DEFAULT 0,
    attached_by  TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (prompt_id, resource_id)
);

-- Serving reads one prompt's attachments in authored order on every
-- prompts/get and manage_prompt use.
CREATE INDEX IF NOT EXISTS idx_prompt_attachments_order
    ON prompt_resource_attachments(prompt_id, position);

-- The resource detail view answers "which prompts depend on this file?" before
-- an operator deletes it.
CREATE INDEX IF NOT EXISTS idx_prompt_attachments_resource
    ON prompt_resource_attachments(resource_id);
