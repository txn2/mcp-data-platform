-- Where in a managed script each of its tool calls was made (#1907): the
-- source position of every call on the script's stack, outermost first, as
-- "line:col". The Flow tab draws a run's calls on the card that made them by
-- this column, joined to the run on session_id. NULL on every row that is not
-- a script's call, and on a script's calls audited before this existed.
ALTER TABLE audit_logs ADD COLUMN IF NOT EXISTS call_site TEXT[];
