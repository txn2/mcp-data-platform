-- #2658: a tile per site environment, drawn by the tile worker from the
-- release the environment serves, and redrawn when that release changes.
-- drawn_release is the release the tile (or its failure) is of;
-- attempt_release and attempts count the tries at the one being drawn.
CREATE TABLE IF NOT EXISTS site_tiles (
    site_id          TEXT        NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
    environment      TEXT        NOT NULL,
    drawn_release    INT,
    attempt_release  INT,
    attempts         INT         NOT NULL DEFAULT 0,
    renderer         INT         NOT NULL DEFAULT 0,
    tile_key         TEXT        NOT NULL DEFAULT '',
    failure          TEXT        NOT NULL DEFAULT '',
    lease_until      TIMESTAMPTZ,
    drawn_at         TIMESTAMPTZ,
    PRIMARY KEY (site_id, environment)
);
