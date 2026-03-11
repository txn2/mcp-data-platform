ALTER TABLE portal_shares ADD COLUMN IF NOT EXISTS notice_text TEXT NOT NULL DEFAULT 'Proprietary & Confidential. Only share with authorized viewers.';
