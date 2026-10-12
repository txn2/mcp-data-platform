-- #2085: v1.143.0 began refusing a script's timeout_seconds above
-- scripts.sql.max_timeout_seconds (#2073). Before it, mcp-trino cut such a
-- request to 300 seconds, so a version saved earlier ran for weeks and then
-- failed every run after the upgrade. Every version that exists when this
-- migration runs was saved before the refusal reached it here, and is marked:
-- a run of it runs the request at the ceiling. A version written afterwards
-- takes the column default, false, and is refused as #2073 intends.
ALTER TABLE script_versions
    ADD COLUMN IF NOT EXISTS predates_statement_cap BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE script_versions
    ALTER COLUMN predates_statement_cap SET DEFAULT FALSE;
