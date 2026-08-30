-- An asset a managed script produces is stamped with the script principal as
-- its owner_id and the script owner's address as owner_email, so every asset
-- read matches ownership on either identifier (#1551). The predicate case-folds
-- the address, so the index is over the folded value.
--
-- Deliberately not a partial index over `owner_email <> ''`: the predicate is
-- `LOWER(owner_email) = LOWER($1)`, and the planner cannot prove that implies a
-- non-empty column without knowing the parameter, so a partial index would sit
-- unused. Rows with no recorded address cost an entry rather than the arm
-- costing a sequential scan.
CREATE INDEX IF NOT EXISTS idx_portal_assets_owner_email_lower
    ON portal_assets (LOWER(owner_email));
