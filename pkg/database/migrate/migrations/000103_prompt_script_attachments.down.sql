-- Reverse 000103: drop the prompt-to-script reference table. Its indexes go
-- with it.
DROP TABLE IF EXISTS prompt_script_attachments;
