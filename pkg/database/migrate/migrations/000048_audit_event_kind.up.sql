ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS event_kind VARCHAR(64);

UPDATE audit_logs SET event_kind = 'apigateway_invoke'
WHERE event_kind IS NULL AND tool_name LIKE 'api\_%' ESCAPE '\';

UPDATE audit_logs SET event_kind = 'mcp_tool_call'
WHERE event_kind IS NULL;
