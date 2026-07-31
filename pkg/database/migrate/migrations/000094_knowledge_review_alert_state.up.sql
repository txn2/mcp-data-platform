-- Re-alert state for the knowledge review-queue staleness alert (#803).
--
-- One row for the whole deployment: the scheduled check claims it with a
-- single conditional UPDATE, so a queue that stays over threshold alerts once
-- per cooldown and only one replica's check wins a given window. The primary
-- key is the constant TRUE, which is what makes "one row" a constraint the
-- database enforces rather than a convention the code remembers.
--
-- alerting tracks whether the queue was over threshold at the last check.
-- Clearing it when the queue is worked back down is what lets the next
-- crossing alert immediately instead of serving out the previous cooldown.
CREATE TABLE IF NOT EXISTS knowledge_review_alert_state (
    id            BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (id),
    alerting      BOOLEAN     NOT NULL DEFAULT FALSE,
    last_alert_at TIMESTAMPTZ
);
