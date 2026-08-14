-- Re-alert state for every review queue, not just the knowledge one (#1287).
--
-- 000094 created this table for the knowledge insight queue with the constant
-- TRUE as its primary key, which made "one row for the whole deployment" a
-- constraint the database enforced. Managed scripts add a second review queue
-- with the same alerting mechanism, so the row becomes one per queue and the
-- key becomes the queue's name.
--
-- The existing row keeps its cooldown: it is renamed in place rather than
-- dropped and recreated, so a deployment whose knowledge queue is mid-cooldown
-- does not re-alert on the first check after upgrading.
ALTER TABLE knowledge_review_alert_state RENAME TO review_alert_state;

ALTER TABLE review_alert_state ADD COLUMN queue TEXT NOT NULL DEFAULT 'knowledge_review';
ALTER TABLE review_alert_state DROP CONSTRAINT knowledge_review_alert_state_pkey;
ALTER TABLE review_alert_state DROP COLUMN id;
ALTER TABLE review_alert_state ADD PRIMARY KEY (queue);

-- The default existed only to fill the column for the row already here. Leaving
-- it in place would let a claim that forgot its key silently take the knowledge
-- queue's cooldown.
ALTER TABLE review_alert_state ALTER COLUMN queue DROP DEFAULT;
