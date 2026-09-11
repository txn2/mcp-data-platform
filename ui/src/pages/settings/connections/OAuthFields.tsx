import type { ReactNode } from "react";

import { ConfigField, ConfigGroup, ConfigSelect, update } from "./fields";
import type { ConfigFormProps } from "./fields";

// The OAuth 2.1 block of a connection editor, in one copy.
//
// Every kind that authenticates with OAuth reads the same canonical config keys
// through pkg/connoauth, so every kind's editor writes the same fields. It was
// written twice, once in the mcp form and once in the api one, and the two
// drifted: the api copy went on speaking the legacy oauth2_* vocabulary after
// the server and migration 000050 had moved to oauth_*, so a migrated OAuth
// connection rendered with no auth configuration at all (#1681).
//
// The grant is a field of its own (oauth_grant) rather than part of the mode
// name, which is what lets one block serve both flows.

const GRANTS = [
  {
    value: "client_credentials",
    label: "client_credentials (machine-to-machine)",
  },
  {
    value: "authorization_code",
    label: "authorization_code + PKCE (browser sign-in)",
  },
];

const PROMPTS = [
  { value: "", label: "(default — no prompt parameter)" },
  { value: "login", label: "login (force fresh credentials each Connect)" },
  { value: "consent", label: "consent (force consent screen)" },
  { value: "select_account", label: "select_account (force account picker)" },
  { value: "none", label: "none (silent auth, fails if interaction needed)" },
];

const ENDPOINT_AUTH_STYLES = [
  { value: "header", label: "Header (HTTP Basic) — OAuth 2.1 default" },
  { value: "params", label: "Form params — some IdPs require this" },
];

// stored reads one canonical key as the string its field edits, with the
// default the platform applies when the connection states nothing.
function stored(
  config: Record<string, unknown>,
  key: string,
  fallback = "",
): string {
  return String(config[key] ?? fallback);
}

// AuthCodeTail is the part of the block only the browser-driven grant uses:
// the OIDC prompt parameter and whatever sign-in affordance the kind carries.
function AuthCodeTail({
  config,
  onChange,
  connect,
}: ConfigFormProps & { connect?: ReactNode }) {
  return (
    <>
      <ConfigSelect
        label="OIDC prompt"
        value={stored(config, "oauth_prompt")}
        onChange={(v) => onChange(update(config, "oauth_prompt", v))}
        options={PROMPTS}
        help={
          <>
            OIDC <code>prompt</code> parameter (§3.1.2.1). Leave default for
            non-OIDC OAuth providers that reject unknown parameters. Use{" "}
            <code>login</code> for Keycloak / Auth0 / Okta to defeat stale-form
            bugs by forcing a fresh credential prompt on every Connect.
          </>
        }
      />
      {connect}
    </>
  );
}

// ScopeField edits the canonical space-delimited scope string. What to put in
// it differs by grant, which is the only reason it is its own component.
function ScopeField({
  config,
  onChange,
  isAuthCode,
}: ConfigFormProps & { isAuthCode: boolean }) {
  return (
    <ConfigField
      label="Scope"
      help={
        isAuthCode
          ? "Space-delimited scopes. Include the offline/refresh scope the IdP needs so scheduled work continues without re-authenticating."
          : "Optional space-delimited scope string. Leave empty if the IdP does not require it."
      }
      value={stored(config, "oauth_scope")}
      onChange={(v) => onChange(update(config, "oauth_scope", v))}
      placeholder={isAuthCode ? "openid profile email" : "read:users"}
      mono
    />
  );
}

// OAuthFields renders the grant picker and the endpoints and credentials the
// chosen grant needs, on the canonical oauth_* keys.
//
// endpointAuthStyle adds the token-endpoint credential placement, which the
// HTTP-based kinds expose. connect is the browser sign-in affordance, rendered
// under the authorization_code fields by the kinds whose editor carries one.
export function OAuthFields({
  config,
  onChange,
  endpointAuthStyle,
  connect,
}: ConfigFormProps & {
  endpointAuthStyle?: boolean;
  connect?: ReactNode;
}) {
  const isAuthCode = config.oauth_grant === "authorization_code";
  return (
    <ConfigGroup title="OAuth 2.1">
      <ConfigSelect
        label="Grant type"
        value={stored(config, "oauth_grant", "client_credentials")}
        onChange={(v) => onChange(update(config, "oauth_grant", v))}
        options={GRANTS}
        help="Use authorization_code for upstreams that require a human sign-in (Google, Salesforce, Keycloak). After saving the connection, click Connect to authorize once — the platform refreshes the token automatically thereafter."
      />
      {isAuthCode && (
        <ConfigField
          label="Authorization URL"
          help="Where the browser is sent to sign in. e.g. https://accounts.google.com/o/oauth2/v2/auth"
          value={stored(config, "oauth_authorization_url")}
          onChange={(v) =>
            onChange(update(config, "oauth_authorization_url", v))
          }
          placeholder="https://idp.example.com/oauth/authorize"
          mono
        />
      )}
      <ConfigField
        label="Token URL"
        help="OAuth token endpoint. The platform POSTs the grant here."
        value={stored(config, "oauth_token_url")}
        onChange={(v) => onChange(update(config, "oauth_token_url", v))}
        placeholder="https://idp.example.com/oauth/token"
        mono
      />
      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Client ID"
          value={stored(config, "oauth_client_id")}
          onChange={(v) => onChange(update(config, "oauth_client_id", v))}
          placeholder="platform-client"
          mono
        />
        <ConfigField
          label="Client Secret"
          help="Encrypted at rest. Use [REDACTED] to keep the existing value when re-saving."
          value={stored(config, "oauth_client_secret")}
          onChange={(v) => onChange(update(config, "oauth_client_secret", v))}
          sensitive
        />
      </div>
      <ScopeField config={config} onChange={onChange} isAuthCode={isAuthCode} />
      {endpointAuthStyle && (
        <ConfigSelect
          label="Endpoint auth style"
          value={stored(config, "oauth_endpoint_auth_style", "header")}
          onChange={(v) =>
            onChange(update(config, "oauth_endpoint_auth_style", v))
          }
          options={ENDPOINT_AUTH_STYLES}
        />
      )}
      {isAuthCode && (
        <AuthCodeTail config={config} onChange={onChange} connect={connect} />
      )}
    </ConfigGroup>
  );
}
