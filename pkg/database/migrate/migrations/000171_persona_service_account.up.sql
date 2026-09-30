-- 000171: a persona can be marked as a service account (#1980).
--
-- An automated caller signs in with an API key whose roles map to a persona,
-- and calls the same tools people use, once per upstream record. The call
-- catalog keeps a record of every call for a later session to re-run or cite;
-- an automated caller's calls are neither, and one deployment held 1.4 million
-- of them from a single key. A persona marked here has its calls audited but
-- not cataloged, and the records it wrote before it was marked are removed by
-- the catalog's next sweep. FALSE is every persona stored before this column
-- existed: their calls are cataloged as they were.
ALTER TABLE persona_definitions
    ADD COLUMN IF NOT EXISTS service_account BOOLEAN NOT NULL DEFAULT FALSE;
