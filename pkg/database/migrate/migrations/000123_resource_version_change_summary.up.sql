-- 000123: why a resource revision was written (#1450).
--
-- A revision records what changed -- type, size, blob key, who wrote it -- and
-- nothing about why. That is enough while every revision is a person picking a
-- file, since the uploader is the answer. It is not enough for a revision the
-- platform writes on somebody's behalf: a registration that cannot read a CSV
-- the way it is stored saves a corrected version of it (#1441), and in the
-- version panel that correction is indistinguishable from an upload.
--
-- A portal asset corrected the same way carries the reason, because
-- portal_asset_versions has had change_summary since 000022. This is the same
-- column on the other kind's trail, so both histories say the same thing.
--
-- Empty rather than NULL for the same reason the asset column is: absent and
-- blank are not two states a reader of a version list can act on differently,
-- and every revision already recorded was written before there was anything to
-- record.
ALTER TABLE resource_versions
    ADD COLUMN IF NOT EXISTS change_summary TEXT NOT NULL DEFAULT '';
