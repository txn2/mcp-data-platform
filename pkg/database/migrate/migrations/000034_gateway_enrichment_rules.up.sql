-- Gateway Enrichment Rules: declarative rules that enrich proxied tool
-- responses with context from other toolkits (Trino queries, DataHub lookups).
CREATE TABLE IF NOT EXISTS gateway_enrichment_rules (
    id              VARCHAR(32) NOT NULL PRIMARY KEY,
    connection_name VARCHAR(255) NOT NULL,
    tool_name       VARCHAR(512) NOT NULL,
    when_predicate  JSONB NOT NULL DEFAULT '{}'::JSONB,
    enrich_action   JSONB NOT NULL,
    merge_strategy  JSONB NOT NULL DEFAULT '{}'::JSONB,
    description     TEXT NOT NULL DEFAULT '',
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    created_by      VARCHAR(255) NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Forward lookup: "for tool X in connection Y, which enabled rules apply?"
CREATE INDEX IF NOT EXISTS idx_gateway_rules_tool
    ON gateway_enrichment_rules(connection_name, tool_name)
    WHERE enabled = TRUE;
