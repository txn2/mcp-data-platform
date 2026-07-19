-- Per-user notification preferences. Keyed by email to match the users
-- directory. Absence of a row means the defaults apply (mode 'immediate',
-- all categories enabled) per the platform's important-features-default-on
-- convention. No FK to users(email): recipients of a share may not yet be
-- in the directory, and a preference row must be creatable for them.
CREATE TABLE IF NOT EXISTS user_notification_prefs (
    email          TEXT        PRIMARY KEY,
    mode           TEXT        NOT NULL DEFAULT 'immediate'
                   CHECK (mode IN ('off', 'immediate', 'daily')),
    shares_enabled   BOOLEAN   NOT NULL DEFAULT TRUE,
    comments_enabled BOOLEAN   NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
