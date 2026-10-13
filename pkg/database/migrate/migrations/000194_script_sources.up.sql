-- 000194: what a managed script is made from besides its own source.
--
-- script_pending_creates keeps the source of a create the save gate refused
-- (#2114), one per owner and name, so the author fixes the failing test or the
-- lint finding with a small patch instead of sending the whole script again.
-- fields holds the rest of the create (display name, description, params,
-- tags, category). A successful save of the name removes the row, and a row is
-- replaced by the next refused create of the same name.
CREATE TABLE IF NOT EXISTS script_pending_creates (
    owner_email TEXT        NOT NULL,
    name        TEXT        NOT NULL,
    source      TEXT        NOT NULL,
    fields      JSONB       NOT NULL DEFAULT '{}',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (owner_email, name)
);

-- script_resource_pins records the version of each managed resource a script
-- version loads as text with load("res:...") (#2113), so every run of that
-- script version, and a replay of one, reads the bytes its save read. A
-- pinned resource version is kept past the resource's version cap while a
-- script version names it.
CREATE TABLE IF NOT EXISTS script_resource_pins (
    version_id       UUID    NOT NULL REFERENCES script_versions(id) ON DELETE CASCADE,
    module           TEXT    NOT NULL,
    resource_id      TEXT    NOT NULL,
    resource_version INTEGER NOT NULL,
    PRIMARY KEY (version_id, module)
);

CREATE INDEX IF NOT EXISTS idx_script_resource_pins_resource
    ON script_resource_pins (resource_id, resource_version);
