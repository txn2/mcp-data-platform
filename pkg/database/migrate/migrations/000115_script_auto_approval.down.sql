-- Reverting drops the distinction between an approval a person made and one the
-- platform made for a personal script's owner. The approvals themselves survive
-- (approved_by, approved_at and grants are untouched), so a rolled-back
-- deployment keeps executing what it was executing and reads every approval as
-- a reviewed one.
ALTER TABLE script_versions DROP COLUMN IF EXISTS auto_approved;
