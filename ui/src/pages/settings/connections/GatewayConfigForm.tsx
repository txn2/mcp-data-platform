import { ConfigField, update, type ConfigFormProps } from "./fields";

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
      <div>
        <label className="mb-1 block text-xs font-medium">Auth mode</label>
        <select
          value={String(config.auth_mode ?? "none")}
          onChange={(e) => onChange(update(config, "auth_mode", e.target.value))}
          className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="none">None</option>
          <option value="bearer">Bearer token</option>
          <option value="api_key">API key</option>
          <option value="oauth">OAuth 2.1</option>
        </select>
        <p className="mt-1 text-xs text-muted-foreground">
          Bearer sends Authorization header; API key sends X-API-Key; OAuth obtains a managed bearer token via client_credentials or authorization_code+PKCE.
        </p>
      </div>
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
        <div className="rounded-md border bg-muted/20 px-3 py-3 space-y-3">
          <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
            OAuth 2.1
          </div>
          <div>
            <label className="block text-xs font-medium text-foreground/80">Grant type</label>
            <select
              value={String(config.oauth_grant ?? "client_credentials")}
              onChange={(e) => onChange(update(config, "oauth_grant", e.target.value))}
              className="mt-1 w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
            >
              <option value="client_credentials">client_credentials (machine-to-machine)</option>
              <option value="authorization_code">authorization_code + PKCE (browser sign-in)</option>
            </select>
            <p className="mt-1 text-xs text-muted-foreground">
              Use authorization_code for upstreams that require a human sign-in (Salesforce Hosted MCP, etc.). After saving the connection, click Connect to authorize once — the platform refreshes the token automatically thereafter.
            </p>
          </div>
          {config.oauth_grant === "authorization_code" && (
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
            help={config.oauth_grant === "authorization_code"
              ? "Space-delimited scopes. Include 'refresh_token' so cron jobs work without re-authenticating."
              : "Optional space-delimited scope string."}
            value={String(config.oauth_scope ?? "")}
            onChange={(v) => onChange(update(config, "oauth_scope", v))}
            placeholder={config.oauth_grant === "authorization_code" ? "api refresh_token" : "read"}
            mono
          />
          {config.oauth_grant === "authorization_code" && (
            <div>
              <label className="mb-1 block text-xs font-medium">OIDC prompt</label>
              <select
                value={String(config.oauth_prompt ?? "")}
                onChange={(e) => onChange(update(config, "oauth_prompt", e.target.value))}
                className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
              >
                <option value="">(default — no prompt parameter)</option>
                <option value="login">login (force fresh credentials each Reconnect)</option>
                <option value="consent">consent (force consent screen)</option>
                <option value="select_account">select_account (force account picker)</option>
                <option value="none">none (silent auth, fails if interaction needed)</option>
              </select>
              <p className="mt-1 text-xs text-muted-foreground">
                OIDC <code>prompt</code> parameter (§3.1.2.1). Leave default for non-OIDC OAuth providers
                that reject unknown parameters. Use <code>login</code> for Keycloak / Auth0 / Okta
                connections an admin holds — defeats stale-form bugs by forcing a fresh credential
                prompt on every Reconnect.
              </p>
            </div>
          )}
        </div>
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
      <div>
        <label className="mb-1 block text-xs font-medium">Trust level</label>
        <select
          value={String(config.trust_level ?? "untrusted")}
          onChange={(e) => onChange(update(config, "trust_level", e.target.value))}
          className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="untrusted">Untrusted (default)</option>
          <option value="trusted">Trusted</option>
        </select>
        <p className="mt-1 text-xs text-muted-foreground">
          Reserved for future content-fencing of upstream responses. Leave at "untrusted" unless you control the upstream.
        </p>
      </div>
    </>
  );
}
