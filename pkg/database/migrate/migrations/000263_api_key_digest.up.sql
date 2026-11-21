-- api_keys.key_digest is the SHA-256 of a database key's value, in hex: the
-- index a presented key is looked up by, so authenticating one costs one hash
-- rather than a bcrypt comparison against every stored key. A key's value is
-- 32 random bytes, which a digest cannot be reversed from. A key stored before
-- this column has none until it is next used, when the comparison that finds
-- it records one.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_digest TEXT;
