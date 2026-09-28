-- 000168: script libraries (#1941).
--
-- A library is a script with no main(): pure code another script loads by name
-- and pinned version, load("lib:<name>@<version>", "fn"). Whether a script is a
-- library is decided when it is created and does not change. A library is
-- shared -- anyone may load one -- so its name is unique across the deployment,
-- whoever owns it, and a load names exactly one library. A script's name stays
-- unique within its owner (idx_scripts_name_owner), and a library and another
-- owner's script may share a name: a load resolves among libraries only.
--
-- library_loads is the library versions the script's current source loads, as
-- "<name>@<version>", written with the source on every save. It is what a
-- library's page lists as the scripts using it, and what refuses deleting a
-- library another script still loads. No row loads anything yet, since load()
-- had no loader before this.
ALTER TABLE scripts ADD COLUMN IF NOT EXISTS library BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE scripts ADD COLUMN IF NOT EXISTS library_loads TEXT[] NOT NULL DEFAULT '{}';
CREATE UNIQUE INDEX IF NOT EXISTS idx_scripts_library_name ON scripts(name) WHERE library;
