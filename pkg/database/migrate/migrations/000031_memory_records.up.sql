-- Enable pgvector extension for embedding similarity search.
CREATE EXTENSION IF NOT EXISTS vector;

-- memory_records is the universal store for agent/analyst session memory.
-- It replaces knowledge_insights as the single backing table for all memory
-- types: preferences, corrections, domain context, institutional knowledge,
-- and insights with suggested catalog changes.
CREATE TABLE IF NOT EXISTS memory_records (
    id              TEXT PRIMARY KEY,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Ownership and scoping
    created_by      TEXT NOT NULL DEFAULT '',       -- user email
    persona         TEXT NOT NULL DEFAULT '',       -- persona visibility scope

    -- LOCOMO dimension: knowledge, event, entity, relationship, preference
    dimension       TEXT NOT NULL DEFAULT 'knowledge',

    -- Content
    content         TEXT NOT NULL,
    category        TEXT NOT NULL DEFAULT 'business_context',
    confidence      TEXT NOT NULL DEFAULT 'medium',
    source          TEXT NOT NULL DEFAULT 'user',

    -- Entity references
    entity_urns     JSONB NOT NULL DEFAULT '[]',
    related_columns JSONB NOT NULL DEFAULT '[]',

    -- Embedding (768-dim nomic-embed-text via Ollama)
    embedding       vector(768),

    -- Flexible metadata (correction chains via superseded_by, suggested_actions, etc.)
    metadata        JSONB NOT NULL DEFAULT '{}',

    -- Status lifecycle: active, stale, superseded, archived
    status          TEXT NOT NULL DEFAULT 'active',

    -- Staleness tracking
    stale_reason    TEXT,
    stale_at        TIMESTAMPTZ,
    last_verified   TIMESTAMPTZ
);

-- Scoping and filtering indexes
CREATE INDEX IF NOT EXISTS idx_memory_records_created_by ON memory_records(created_by);
CREATE INDEX IF NOT EXISTS idx_memory_records_persona ON memory_records(persona);
CREATE INDEX IF NOT EXISTS idx_memory_records_dimension ON memory_records(dimension);
CREATE INDEX IF NOT EXISTS idx_memory_records_status ON memory_records(status);
CREATE INDEX IF NOT EXISTS idx_memory_records_category ON memory_records(category);
CREATE INDEX IF NOT EXISTS idx_memory_records_created_at ON memory_records(created_at);

-- GIN indexes for JSONB containment queries
CREATE INDEX IF NOT EXISTS idx_memory_records_entity_urns ON memory_records USING gin(entity_urns);
CREATE INDEX IF NOT EXISTS idx_memory_records_related_columns ON memory_records USING gin(related_columns);

-- Migrate existing knowledge_insights data into memory_records.
INSERT INTO memory_records (
    id, created_at, updated_at, created_by, persona, dimension,
    content, category, confidence, source,
    entity_urns, related_columns, metadata, status
)
SELECT
    id,
    created_at,
    COALESCE(GREATEST(reviewed_at, applied_at), created_at),
    captured_by,
    persona,
    'knowledge',
    insight_text,
    category,
    confidence,
    COALESCE(NULLIF(source, ''), 'user'),
    entity_urns,
    related_columns,
    jsonb_build_object(
        'suggested_actions', suggested_actions,
        'session_id', session_id,
        'legacy_status', status,
        'reviewed_by', COALESCE(reviewed_by, ''),
        'review_notes', COALESCE(review_notes, ''),
        'applied_by', COALESCE(applied_by, ''),
        'changeset_ref', COALESCE(changeset_ref, '')
    ),
    CASE
        WHEN status = 'superseded' THEN 'superseded'
        WHEN status IN ('rejected', 'rolled_back') THEN 'archived'
        ELSE 'active'
    END
FROM knowledge_insights;

-- Drop the old table now that data is migrated.
DROP TABLE IF EXISTS knowledge_insights;
