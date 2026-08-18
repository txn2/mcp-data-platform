-- Session-start notice watermark (#1278). platform_info briefs a caller on
-- unaddressed feedback left on assets they own and on artifacts newly shared
-- with them. The watermark records when that briefing was last delivered, so
-- the next session reports only what arrived since.
--
-- It is a table of its own rather than a column on users because the two have
-- different lifecycles: users rows are upserted asynchronously and throttled by
-- pkg/user.Directory on every authentication, which may refresh last_seen_at in
-- the very session whose digest is being computed, and users is populated only
-- for OIDC/OAuth people while a notice watermark applies to any authenticated
-- caller.
CREATE TABLE IF NOT EXISTS user_notice_watermarks (
    -- The caller's normalized email when they have one, otherwise their user
    -- id. Feedback threads and share grants are both matched by email OR id,
    -- so the key follows the same identity the digest is built from.
    user_key     TEXT        PRIMARY KEY,
    -- The instant the last digest was delivered to this caller. Set to a time
    -- captured before the digest queries run, so activity landing mid-build is
    -- reported next session rather than lost.
    delivered_at TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
