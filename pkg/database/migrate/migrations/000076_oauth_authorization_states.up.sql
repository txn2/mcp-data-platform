-- oauth_authorization_states persists in-flight authorization state linking an
-- /oauth/authorize redirect to its upstream IdP callback. In multi-replica
-- deployments the callback can land on a different replica than the one that
-- started the flow, so this state cannot live in process memory. Rows are
-- short-lived: the callback deletes its row, and the OAuth store cleanup
-- routine sweeps abandoned flows by created_at.
CREATE TABLE IF NOT EXISTS oauth_authorization_states (
    state_key  TEXT PRIMARY KEY,
    payload    JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_oauth_authorization_states_created_at
    ON oauth_authorization_states (created_at);
