import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
  update,
  type ConfigFormProps,
} from "./fields";

const AUTH_MODES = [
  { value: "none", label: "None" },
  { value: "bearer", label: "Bearer token" },
  { value: "api_key", label: "API key" },
  { value: "oauth", label: "OAuth 2.1" },
];

const GRANTS = [
  { value: "client_credentials", label: "client_credentials (machine-to-machine)" },
  { value: "authorization_code", label: "authorization_code + PKCE (browser sign-in)" },
];

const PROMPTS = [
  { value: "", label: "(default — no prompt parameter)" },
  { value: "login", label: "login (force fresh credentials each Reconnect)" },
  { value: "consent", label: "consent (force consent screen)" },
  { value: "select_account", label: "select_account (force account picker)" },
  { value: "none", label: "none (silent auth, fails if interaction needed)" },
];

const TRUST_LEVELS = [
  { value: "untrusted", label: "Untrusted (default)" },
  { value: "trusted", label: "Trusted" },
];

// GatewayOAuthFields renders the OAuth 2.1 block of the gateway form: the
// grant picker and the endpoints/credentials each grant needs. Split from the
// form itself so neither function carries both the always-on fields and the
// grant-conditional ones.
function GatewayOAuthFields({ config, onChange }: ConfigFormProps) {
  const isAuthCode = config.oauth_grant === "authorization_code";
  return (
    <ConfigGroup title="OAuth 2.1">
      <ConfigSelect
        label="Grant type"
        value={String(config.oauth_grant ?? "client_credentials")}
        onChange={(v) => onChange(update(config, "oauth_grant", v))}
        options={GRANTS}
        help="Use authorization_code for upstreams that require a human sign-in (Salesforce Hosted MCP, etc.). After saving the connection, click Connect to authorize once — the platform refreshes the token automatically thereafter."
      />
      {isAuthCode && (
        <ConfigField
          label="Authorization URL"
          help="Where the browser is sent to sign in. e.g. https://login.salesforce.com/services/oauth2/authorize"
          value={String(config.oauth_authorization_url ?? "")}
          onChange={(v) => onChange(update(config, "oauth_authorization_url", v))}
          placeholder="https://login.salesforce.com/services/oauth2/authorize"
          mono
        />
      )}
      <ConfigField
        label="Token URL"
        help="OAuth token endpoint. The platform POSTs the grant here."
        value={String(config.oauth_token_url ?? "")}
        onChange={(v) => onChange(update(config, "oauth_token_url", v))}
        placeholder="https://vendor.example.com/oauth/token"
        mono
      />
      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Client ID"
          value={String(config.oauth_client_id ?? "")}
          onChange={(v) => onChange(update(config, "oauth_client_id", v))}
          placeholder="platform-client"
          mono
        />
        <ConfigField
          label="Client Secret"
          help="Encrypted at rest. Use [REDACTED] to keep the existing value when re-saving."
          value={String(config.oauth_client_secret ?? "")}
          onChange={(v) => onChange(update(config, "oauth_client_secret", v))}
          sensitive
        />
      </div>
      <ConfigField
        label="Scope"
        help={isAuthCode
          ? "Space-delimited scopes. Include 'refresh_token' so cron jobs work without re-authenticating."
          : "Optional space-delimited scope string."}
        value={String(config.oauth_scope ?? "")}
        onChange={(v) => onChange(update(config, "oauth_scope", v))}
        placeholder={isAuthCode ? "api refresh_token" : "read"}
        mono
      />
      {isAuthCode && (
        <ConfigSelect
          label="OIDC prompt"
          value={String(config.oauth_prompt ?? "")}
          onChange={(v) => onChange(update(config, "oauth_prompt", v))}
          options={PROMPTS}
          help={
            <>
              OIDC <code>prompt</code> parameter (§3.1.2.1). Leave default for non-OIDC OAuth
              providers that reject unknown parameters. Use <code>login</code> for Keycloak /
              Auth0 / Okta connections an admin holds — defeats stale-form bugs by forcing a
              fresh credential prompt on every Reconnect.
            </>
          }
        />
      )}
    </ConfigGroup>
  );
}

// Editor form for kind=mcp connections — the MCP gateway toolkit that proxies
// tools from an upstream MCP server. Field shape matches pkg/toolkits/gateway.
export function GatewayConfigForm({ config, onChange }: ConfigFormProps) {
  return (
    <>
      <ConfigField
        label="Endpoint"
        help="HTTPS URL of the upstream MCP server (Streamable HTTP transport)."
        value={String(config.endpoint ?? "")}
        onChange={(v) => onChange(update(config, "endpoint", v))}
        placeholder="https://vendor.example.com/mcp"
        mono
        required
      />
      <ConfigSelect
        label="Auth mode"
        value={String(config.auth_mode ?? "none")}
        onChange={(v) => onChange(update(config, "auth_mode", v))}
        options={AUTH_MODES}
        help="Bearer sends Authorization header; API key sends X-API-Key; OAuth obtains a managed bearer token via client_credentials or authorization_code+PKCE."
      />
      {(config.auth_mode === "bearer" || config.auth_mode === "api_key") && (
        <ConfigField
          label="Credential"
          help="Encrypted at rest. Use [REDACTED] when re-saving without changing it."
          value={String(config.credential ?? "")}
          onChange={(v) => onChange(update(config, "credential", v))}
          sensitive
        />
      )}
      {config.auth_mode === "oauth" && (
        <GatewayOAuthFields config={config} onChange={onChange} />
      )}
      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Connect timeout"
          help="Initial dial + tool discovery (e.g. 10s, 1m)."
          value={String(config.connect_timeout ?? "")}
          onChange={(v) => onChange(update(config, "connect_timeout", v))}
          placeholder="10s"
          mono
        />
        <ConfigField
          label="Call timeout"
          help="Per-tool-call upstream timeout (e.g. 60s)."
          value={String(config.call_timeout ?? "")}
          onChange={(v) => onChange(update(config, "call_timeout", v))}
          placeholder="60s"
          mono
        />
      </div>
      <ConfigSelect
        label="Trust level"
        value={String(config.trust_level ?? "untrusted")}
        onChange={(v) => onChange(update(config, "trust_level", v))}
        options={TRUST_LEVELS}
        help={`Reserved for future content-fencing of upstream responses. Leave at "untrusted" unless you control the upstream.`}
      />
    </>
  );
}
