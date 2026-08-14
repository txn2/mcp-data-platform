-- Reverse 000101: back to one row keyed by the constant TRUE.
--
-- Every queue other than the knowledge one has to go: the shape being restored
-- holds exactly one row, and the columns that identify which queue it belongs
-- to are about to disappear.
DELETE FROM review_alert_state WHERE queue <> 'knowledge_review';

ALTER TABLE review_alert_state ADD COLUMN id BOOLEAN NOT NULL DEFAULT TRUE CHECK (id);
ALTER TABLE review_alert_state DROP CONSTRAINT review_alert_state_pkey;
ALTER TABLE review_alert_state DROP COLUMN queue;
ALTER TABLE review_alert_state ADD PRIMARY KEY (id);

ALTER TABLE review_alert_state RENAME TO knowledge_review_alert_state;

-- The constraints added above were named after the table they were added to,
-- so restoring the table's name is not enough: 000101's up drops the primary
-- key BY NAME, and a down that left these named review_alert_state_* would make
-- the next up fail on a constraint that does not exist, leaving the schema
-- dirty. Reversing a migration has to restore the names as well as the shape.
ALTER TABLE knowledge_review_alert_state
    RENAME CONSTRAINT review_alert_state_pkey TO knowledge_review_alert_state_pkey;
ALTER TABLE knowledge_review_alert_state
    RENAME CONSTRAINT review_alert_state_id_check TO knowledge_review_alert_state_id_check;
