-- 000182 cleared recorded tile failures so the rows would be drawn again. There
-- is nothing to restore: a row the binary rolled back to still cannot draw is
-- recorded again on its next attempt.
SELECT 1;
