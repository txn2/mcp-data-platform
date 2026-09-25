-- 000162 forgot tiles that showed a file's zip bytes in place of its
-- content-type icon. There is nothing to restore: rolling back leaves those
-- files on their icon, which the rule this migration shipped with never drew.
SELECT 1;
