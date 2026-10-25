-- External users sign in through the identity provider (#2290).
--
-- A person outside the organization is an identity-provider account whose
-- role resolves to a persona marked external; persona_definitions.external
-- stays. The accounts the platform created itself, and the one-time links they
-- signed in with (#2092), are gone.
DROP TABLE IF EXISTS external_signin_links;
DROP TABLE IF EXISTS external_accounts;

-- An external persona's uploads are no longer shared on arrival: they stay in
-- the uploader's own folder, which External files reaches. portal.uploads was
-- who each upload was shared with, and is now whether the persona's people
-- upload at all.
UPDATE persona_definitions SET portal = jsonb_set(portal, '{uploads}', 'true'::jsonb)
WHERE jsonb_typeof(portal->'uploads') = 'object';
