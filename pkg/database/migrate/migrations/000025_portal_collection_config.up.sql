-- Extensible configuration for collections (thumbnail size, templates, etc.)
ALTER TABLE portal_collections ADD COLUMN config JSONB NOT NULL DEFAULT '{}';
