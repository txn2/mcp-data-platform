-- A person's library: favorites, what they opened, and the duplicate groups
-- they dismissed (#2504, #2509).
--
-- A favorite is private to the person who starred it and grants nothing:
-- every read joins it to the asset or collection under the portal's reach
-- rule, so a starred item the person can no longer open is simply absent.
CREATE TABLE IF NOT EXISTS portal_favorites (
    user_id     TEXT NOT NULL,
    target_type TEXT NOT NULL CHECK (target_type IN ('asset', 'collection')),
    target_id   TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, target_type, target_id)
);

CREATE INDEX IF NOT EXISTS idx_portal_favorites_user ON portal_favorites (user_id, created_at DESC);

-- One row per person and item they opened in the portal: how many times and
-- when last. Recents read a person's own rows; Unused reads the counts and the
-- last open across everyone, never who opened.
CREATE TABLE IF NOT EXISTS portal_opens (
    user_id         TEXT NOT NULL,
    target_type     TEXT NOT NULL CHECK (target_type IN ('asset', 'collection')),
    target_id       TEXT NOT NULL,
    open_count      BIGINT NOT NULL DEFAULT 1,
    first_opened_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_opened_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, target_type, target_id)
);

CREATE INDEX IF NOT EXISTS idx_portal_opens_user_recent ON portal_opens (user_id, last_opened_at DESC);
CREATE INDEX IF NOT EXISTS idx_portal_opens_target ON portal_opens (target_type, target_id);

-- A possible-duplicates group a person dismissed. The digest is taken over
-- the group's members and the version each was at, so the group comes back
-- when one of them gets a new version or the group gains or loses a member.
CREATE TABLE IF NOT EXISTS portal_duplicate_dismissals (
    user_id      TEXT NOT NULL,
    group_digest TEXT NOT NULL,
    dismissed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, group_digest)
);
