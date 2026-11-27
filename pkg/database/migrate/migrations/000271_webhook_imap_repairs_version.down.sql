ALTER TABLE webhook_imap_repairs
    DROP COLUMN IF EXISTS collapsed_files,
    DROP COLUMN IF EXISTS collapsed,
    DROP COLUMN IF EXISTS version;
