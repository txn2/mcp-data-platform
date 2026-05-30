-- 000050: unify OAuth connection-config keys onto the canonical schema
--
-- The api-gateway (kind='api') toolkit historically authored OAuth
-- connections with an oauth2_* config vocabulary and encoded the grant
-- in the auth_mode value (oauth2_authorization_code /
-- oauth2_client_credentials). The mcp gateway used the canonical oauth_*
-- vocabulary with auth_mode='oauth' + oauth_grant. This migration
-- rewrites every legacy api-kind row in connection_instances.config onto
-- the canonical schema so both kinds share one set of keys.
--
-- Scope: api-kind rows only. mcp-kind rows are already canonical.
--
-- Token safety: connection_oauth_tokens keys on (connection_kind,
-- connection_name), neither of which changes here, so every persisted
-- access/refresh token survives untouched. No operator re-authenticates.
--
-- Secret safety: oauth2_client_secret values are stored as the
-- platform's "enc:" AES-256-GCM blobs. Encryption uses no additional
-- authenticated data (the field name is not bound into the ciphertext),
-- so moving the blob verbatim to oauth_client_secret keeps it
-- decryptable; oauth_client_secret is already in the FieldEncryptor's
-- sensitive-key set.
--
-- Idempotent: the WHERE filter selects only rows that still carry a
-- legacy key or legacy auth_mode value, so a re-run (or a row created
-- natively in the canonical shape) is a no-op. Canonical keys win when
-- both shapes are somehow present: the legacy value is dropped without
-- overwriting an existing canonical sibling.

DO $$
DECLARE
    r   RECORD;
    cfg jsonb;
BEGIN
    FOR r IN
        SELECT kind, name, config
          FROM connection_instances
         WHERE kind = 'api'
           AND (config ? 'oauth2_token_url'
             OR config ? 'oauth2_authorization_url'
             OR config ? 'oauth2_client_id'
             OR config ? 'oauth2_client_secret'
             OR config ? 'oauth2_scopes'
             OR config ? 'oauth2_prompt'
             OR config ? 'oauth2_endpoint_auth_style'
             OR config ->> 'auth_mode' IN ('oauth2_authorization_code', 'oauth2_client_credentials'))
    LOOP
        cfg := r.config;

        -- Scalar key renames. For each, copy to the canonical key only
        -- when the canonical key is absent, then drop the legacy key.
        IF cfg ? 'oauth2_token_url' THEN
            IF NOT cfg ? 'oauth_token_url' THEN
                cfg := cfg || jsonb_build_object('oauth_token_url', cfg -> 'oauth2_token_url');
            END IF;
            cfg := cfg - 'oauth2_token_url';
        END IF;

        IF cfg ? 'oauth2_authorization_url' THEN
            IF NOT cfg ? 'oauth_authorization_url' THEN
                cfg := cfg || jsonb_build_object('oauth_authorization_url', cfg -> 'oauth2_authorization_url');
            END IF;
            cfg := cfg - 'oauth2_authorization_url';
        END IF;

        IF cfg ? 'oauth2_client_id' THEN
            IF NOT cfg ? 'oauth_client_id' THEN
                cfg := cfg || jsonb_build_object('oauth_client_id', cfg -> 'oauth2_client_id');
            END IF;
            cfg := cfg - 'oauth2_client_id';
        END IF;

        IF cfg ? 'oauth2_client_secret' THEN
            IF NOT cfg ? 'oauth_client_secret' THEN
                cfg := cfg || jsonb_build_object('oauth_client_secret', cfg -> 'oauth2_client_secret');
            END IF;
            cfg := cfg - 'oauth2_client_secret';
        END IF;

        IF cfg ? 'oauth2_prompt' THEN
            IF NOT cfg ? 'oauth_prompt' THEN
                cfg := cfg || jsonb_build_object('oauth_prompt', cfg -> 'oauth2_prompt');
            END IF;
            cfg := cfg - 'oauth2_prompt';
        END IF;

        IF cfg ? 'oauth2_endpoint_auth_style' THEN
            IF NOT cfg ? 'oauth_endpoint_auth_style' THEN
                cfg := cfg || jsonb_build_object('oauth_endpoint_auth_style', cfg -> 'oauth2_endpoint_auth_style');
            END IF;
            cfg := cfg - 'oauth2_endpoint_auth_style';
        END IF;

        -- Scope shape change: legacy array -> canonical space-delimited
        -- string (the OAuth 2.0 wire form).
        IF cfg ? 'oauth2_scopes' THEN
            IF NOT cfg ? 'oauth_scope' THEN
                cfg := cfg || jsonb_build_object('oauth_scope',
                    array_to_string(ARRAY(SELECT jsonb_array_elements_text(cfg -> 'oauth2_scopes')), ' '));
            END IF;
            cfg := cfg - 'oauth2_scopes';
        END IF;

        -- auth_mode -> canonical 'oauth' + oauth_grant. The grant is set
        -- only when absent so an explicit oauth_grant is preserved.
        IF cfg ->> 'auth_mode' = 'oauth2_authorization_code' THEN
            cfg := cfg || jsonb_build_object('auth_mode', 'oauth');
            IF NOT cfg ? 'oauth_grant' THEN
                cfg := cfg || jsonb_build_object('oauth_grant', 'authorization_code');
            END IF;
        ELSIF cfg ->> 'auth_mode' = 'oauth2_client_credentials' THEN
            cfg := cfg || jsonb_build_object('auth_mode', 'oauth');
            IF NOT cfg ? 'oauth_grant' THEN
                cfg := cfg || jsonb_build_object('oauth_grant', 'client_credentials');
            END IF;
        END IF;

        UPDATE connection_instances
           SET config = cfg, updated_at = NOW()
         WHERE kind = r.kind AND name = r.name;
    END LOOP;
END $$;
