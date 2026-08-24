-- Reverse 000124. The repair overwrote a column whose prior contents were
-- themselves the artifact this migration exists to remove, and nothing records
-- them, so there is nothing to restore. A downgraded deployment keeps the
-- repaired dates.
SELECT 1;
