-- Add the sink-class axis (#633): the single organizing axis for the unified
-- memory_capture write path. It replaces the agent-facing category/dimension
-- split and drives routing (which records stay as memory vs are promoted to a
-- canonical sink). Nullable; new captures set it, existing rows are backfilled
-- below from their dimension (and entity_urns for the knowledge dimension).
ALTER TABLE memory_records ADD COLUMN IF NOT EXISTS sink_class TEXT;

UPDATE memory_records
SET sink_class = CASE
    WHEN dimension = 'preference' THEN 'personal_preference'
    WHEN dimension = 'event' THEN 'episodic_event'
    WHEN dimension = 'entity' THEN 'schema_entity'
    WHEN dimension = 'relationship' THEN 'business_knowledge'
    WHEN dimension = 'knowledge' AND entity_urns <> '[]'::jsonb THEN 'schema_entity'
    WHEN dimension = 'knowledge' THEN 'business_knowledge'
    ELSE 'business_knowledge'
END
WHERE sink_class IS NULL;

CREATE INDEX IF NOT EXISTS idx_memory_records_sink_class ON memory_records(sink_class);
