-- Sharing an asset, collection or prompt, with a person or to a channel, is
-- a persona permission (#2241). True keeps what every persona stored before
-- this could do.
ALTER TABLE persona_definitions ADD COLUMN IF NOT EXISTS allow_sharing BOOLEAN NOT NULL DEFAULT TRUE;
