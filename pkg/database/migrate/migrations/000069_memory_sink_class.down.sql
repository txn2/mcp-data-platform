DROP INDEX IF EXISTS idx_memory_records_sink_class;
ALTER TABLE memory_records DROP COLUMN IF EXISTS sink_class;
