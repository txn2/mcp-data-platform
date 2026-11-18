import type { ReactNode } from "react";

import { ConfigField, ConfigGroup, ConfigSelect, update } from "./fields";
import type { ConfigFormProps } from "./fields";
import { SignedJWTAuthFields } from "./SignedJWTAuthFields";

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
// name, which is what lets one block serve every flow.

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

// JWT_BEARER_GRANT is offered only by the kinds that sign the assertion: the
// HTTP-based ones built on internal/upstreamauth. The mcp kind refuses the
// grant on save, so its editor does not list it.
const JWT_BEARER_GRANT = {
  value: "jwt_bearer",
  label: "jwt_bearer (signed assertion, RFC 7523)",
};

// PASSWORD_GRANT is the legacy resource owner password grant (#2689), offered
// beside jwt_bearer by the same kinds: the platform speaks it only as a
// client, to an upstream that offers no other grant.
const PASSWORD_GRANT = {
  value: "password",
  label: "password (username and password, legacy)",
};

// COPY is the help text that differs between the editors that offer the
// jwt_bearer grant and the one that does not, and on the grant itself.
const COPY = {
  grantHelp:
    "Use authorization_code for upstreams that require a human sign-in (Google, Salesforce, Keycloak). After saving the connection, click Connect to authorize once — the platform refreshes the token automatically thereafter.",
  grantHelpWithJWTBearer:
    "Use authorization_code for upstreams that require a human sign-in (Google, Salesforce, Keycloak); after saving, click Connect to authorize once. Use jwt_bearer when the upstream registered a signing key for unattended server-to-server access: the platform signs a short-lived assertion and exchanges it for an access token, with no browser and no refresh token.",
  tokenURL: "OAuth token endpoint. The platform POSTs the grant here.",
  tokenURLJWTBearer:
    "OAuth token endpoint. The platform POSTs the signed assertion here, and it is the assertion's default audience.",
  secret:
    "Encrypted at rest. Use [REDACTED] to keep the existing value when re-saving.",
  secretJWTBearer:
    "Optional, as the client id. Encrypted at rest. Use [REDACTED] to keep the existing value when re-saving.",
  clientIDJWTBearer:
    "Optional. Only for an upstream that also authenticates the client on the token request; the assertion identifies it otherwise.",
  grantHelpPassword:
    "Use password only for an upstream that offers no other grant. The platform posts the API user's username and password to the token endpoint, reuses the token until it expires, renews it with the refresh token, and signs in again with the password when the upstream refuses that. OAuth 2.1 removes this grant.",
  clientIDPassword: "Optional. Only for an upstream that also identifies the client.",
  secretPassword: "Optional, with a client ID. Encrypted at rest.",
  username: "The upstream's API user. May name a stored secret.",
  password: "Encrypted at rest and never shown again. Leave [REDACTED] to keep it. May name a stored secret.",
};

// ClientCredentialFields edits the client id and secret. Every grant but
// jwt_bearer requires both; under jwt_bearer they are optional client
// authentication on the token request, and the help says so.
function ClientCredentialFields({
  config,
  onChange,
  isJWTBearer,
  isPassword,
}: ConfigFormProps & { isJWTBearer: boolean; isPassword: boolean }) {
  const idHelp = isPassword ? COPY.clientIDPassword : isJWTBearer ? COPY.clientIDJWTBearer : undefined;
  const secretHelp = isPassword ? COPY.secretPassword : isJWTBearer ? COPY.secretJWTBearer : COPY.secret;
  return (
    <div className="grid grid-cols-2 gap-3">
      <ConfigField
        label="Client ID"
        help={idHelp}
        value={stored(config, "oauth_client_id")}
        onChange={(v) => onChange(update(config, "oauth_client_id", v))}
        placeholder="platform-client"
        mono
      />
      <ConfigField
        label="Client Secret"
        help={secretHelp}
        value={stored(config, "oauth_client_secret")}
        onChange={(v) => onChange(update(config, "oauth_client_secret", v))}
        sensitive
      />
    </div>
  );
}

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

// grantOptions are the grants a kind's editor offers: the HTTP-based kinds
// add jwt_bearer and password, which only they implement.
function grantOptions(jwtBearer?: boolean) {
  return jwtBearer ? [...GRANTS, JWT_BEARER_GRANT, PASSWORD_GRANT] : GRANTS;
}

// grantHelp is the grant picker's help for the editor and the chosen grant.
function grantHelp(jwtBearer: boolean | undefined, isPassword: boolean): string {
  if (isPassword) return COPY.grantHelpPassword;
  return jwtBearer ? COPY.grantHelpWithJWTBearer : COPY.grantHelp;
}

// PasswordUserFields edits the API user the password grant posts (#2689).
function PasswordUserFields({ config, onChange }: ConfigFormProps) {
  return (
    <div className="grid grid-cols-2 gap-3">
      <ConfigField
        label="Username"
        help={COPY.username}
        value={stored(config, "oauth_username")}
        onChange={(v) => onChange(update(config, "oauth_username", v))}
        placeholder="api-integration"
        mono
      />
      <ConfigField
        label="Password"
        help={COPY.password}
        value={stored(config, "oauth_password")}
        onChange={(v) => onChange(update(config, "oauth_password", v))}
        sensitive
      />
    </div>
  );
}

// OAuthFields renders the grant picker and the endpoints and credentials the
// chosen grant needs, on the canonical oauth_* keys.
//
// endpointAuthStyle adds the token-endpoint credential placement, which the
// HTTP-based kinds expose. jwtBearer offers the RFC 7523 grant and its signing
// fields, and the legacy password grant (#2689), which only those kinds
// implement. connect is the browser sign-in
// affordance, rendered under the authorization_code fields by the kinds whose
// editor carries one.
export function OAuthFields({
  config,
  onChange,
  endpointAuthStyle,
  jwtBearer,
  connect,
}: ConfigFormProps & {
  endpointAuthStyle?: boolean;
  jwtBearer?: boolean;
  connect?: ReactNode;
}) {
  const isAuthCode = config.oauth_grant === "authorization_code";
  const isJWTBearer = jwtBearer === true && config.oauth_grant === "jwt_bearer";
  const isPassword = jwtBearer === true && config.oauth_grant === "password";
  return (
    <ConfigGroup title="OAuth 2.1">
      <ConfigSelect
        label="Grant type"
        value={stored(config, "oauth_grant", "client_credentials")}
        onChange={(v) => onChange(update(config, "oauth_grant", v))}
        options={grantOptions(jwtBearer)}
        help={grantHelp(jwtBearer, isPassword)}
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
        help={isJWTBearer ? COPY.tokenURLJWTBearer : COPY.tokenURL}
        value={stored(config, "oauth_token_url")}
        onChange={(v) => onChange(update(config, "oauth_token_url", v))}
        placeholder="https://idp.example.com/oauth/token"
        mono
      />
      {isPassword && <PasswordUserFields config={config} onChange={onChange} />}
      <ClientCredentialFields
        config={config}
        onChange={onChange}
        isJWTBearer={isJWTBearer}
        isPassword={isPassword}
      />
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
      {isJWTBearer && (
        <SignedJWTAuthFields
          config={config}
          onChange={onChange}
          variant="jwt_bearer"
        />
      )}
    </ConfigGroup>
  );
}
