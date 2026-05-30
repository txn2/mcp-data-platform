-- 000050 down: revert api-kind OAuth connections to the legacy oauth2_*
-- schema so a rollback to a pre-unification binary (whose api toolkit
-- only reads oauth2_* keys and the oauth2_* auth_mode values) still
-- parses every connection. mcp-kind rows are left untouched.
--
-- This converts canonical api-kind OAuth rows even if they were created
-- natively in the canonical shape after the up migration; that is the
-- correct end state for a rollback, since the reverted binary expects
-- the legacy vocabulary. Tokens are unaffected (keyed on kind+name).

DO $$
DECLARE
    r     RECORD;
    cfg   jsonb;
    grant_val text;
BEGIN
    FOR r IN
        SELECT kind, name, config
          FROM connection_instances
         WHERE kind = 'api'
           AND config ->> 'auth_mode' = 'oauth'
    LOOP
        cfg := r.config;
        grant_val := cfg ->> 'oauth_grant';

        -- auth_mode 'oauth' + oauth_grant -> the legacy combined mode.
        IF grant_val = 'authorization_code' THEN
            cfg := cfg || jsonb_build_object('auth_mode', 'oauth2_authorization_code');
        ELSE
            cfg := cfg || jsonb_build_object('auth_mode', 'oauth2_client_credentials');
        END IF;
        cfg := cfg - 'oauth_grant';

        -- Scalar key renames back to oauth2_*.
        IF cfg ? 'oauth_token_url' THEN
            cfg := cfg || jsonb_build_object('oauth2_token_url', cfg -> 'oauth_token_url');
            cfg := cfg - 'oauth_token_url';
        END IF;
        IF cfg ? 'oauth_authorization_url' THEN
            cfg := cfg || jsonb_build_object('oauth2_authorization_url', cfg -> 'oauth_authorization_url');
            cfg := cfg - 'oauth_authorization_url';
        END IF;
        IF cfg ? 'oauth_client_id' THEN
            cfg := cfg || jsonb_build_object('oauth2_client_id', cfg -> 'oauth_client_id');
            cfg := cfg - 'oauth_client_id';
        END IF;
        IF cfg ? 'oauth_client_secret' THEN
            cfg := cfg || jsonb_build_object('oauth2_client_secret', cfg -> 'oauth_client_secret');
            cfg := cfg - 'oauth_client_secret';
        END IF;
        IF cfg ? 'oauth_prompt' THEN
            cfg := cfg || jsonb_build_object('oauth2_prompt', cfg -> 'oauth_prompt');
            cfg := cfg - 'oauth_prompt';
        END IF;
        IF cfg ? 'oauth_endpoint_auth_style' THEN
            cfg := cfg || jsonb_build_object('oauth2_endpoint_auth_style', cfg -> 'oauth_endpoint_auth_style');
            cfg := cfg - 'oauth_endpoint_auth_style';
        END IF;

        -- Scope: canonical space-delimited string -> legacy array. An
        -- empty/whitespace-only scope yields an empty array.
        IF cfg ? 'oauth_scope' THEN
            cfg := cfg || jsonb_build_object('oauth2_scopes',
                COALESCE(
                    (SELECT jsonb_agg(tok)
                       FROM regexp_split_to_table(trim(cfg ->> 'oauth_scope'), '\s+') AS tok
                      WHERE tok <> ''),
                    '[]'::jsonb));
            cfg := cfg - 'oauth_scope';
        END IF;

        UPDATE connection_instances
           SET config = cfg, updated_at = NOW()
         WHERE kind = r.kind AND name = r.name;
    END LOOP;
END $$;
