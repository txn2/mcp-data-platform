import { useCallback, useState } from "react";
import { AlertCircle } from "lucide-react";

import { useStartAPIGatewayOAuth } from "@/api/admin/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
  update,
  type ConfigFormProps,
} from "./fields";

// The auth half of the api-kind connection editor: the mode picker and the
// credential fields each mode needs. Split from ApiGatewayConfigForm so the
// form file states the connection's shape and this one states its auth.

const AUTH_MODES = [
  { value: "none", label: "None" },
  { value: "bearer", label: "Bearer token" },
  { value: "api_key", label: "API key" },
  { value: "basic", label: "Basic (RFC 7617)" },
  { value: "oauth2_client_credentials", label: "OAuth 2.1 client_credentials" },
  {
    value: "oauth2_authorization_code",
    label: "OAuth 2.1 authorization_code (browser sign-in)",
  },
  { value: "mtls", label: "mTLS (client certificate is the credential)" },
];

const API_KEY_PLACEMENTS = [
  { value: "header", label: "Header" },
  { value: "query", label: "Query string" },
];

const ENDPOINT_AUTH_STYLES = [
  { value: "header", label: "Header (HTTP Basic) — OAuth 2.1 default" },
  { value: "params", label: "Form params — some IdPs require this" },
];

const PROMPTS = [
  { value: "", label: "(default — no prompt parameter)" },
  { value: "login", label: "login (force fresh credentials each Connect)" },
  { value: "consent", label: "consent (force consent screen)" },
  { value: "select_account", label: "select_account (force account picker)" },
  { value: "none", label: "none (silent auth)" },
];

function ApiKeyFields({ config, onChange }: ConfigFormProps) {
  return (
    <ConfigGroup title="API key">
      <ConfigField
        label="Credential"
        help="The API key value. Encrypted at rest. Use [REDACTED] to keep an existing value when re-saving."
        value={String(config.credential ?? "")}
        onChange={(v) => onChange(update(config, "credential", v))}
        sensitive
      />
      <div className="grid grid-cols-2 gap-3">
        <ConfigSelect
          label="Placement"
          value={String(config.api_key_placement ?? "header")}
          onChange={(v) => onChange(update(config, "api_key_placement", v))}
          options={API_KEY_PLACEMENTS}
        />
        {config.api_key_placement === "query" ? (
          <ConfigField
            label="Query parameter name"
            help="e.g. api_key, apikey, key."
            value={String(config.api_key_param ?? "")}
            onChange={(v) => onChange(update(config, "api_key_param", v))}
            placeholder="api_key"
            mono
          />
        ) : (
          <ConfigField
            label="Header name"
            help="Defaults to X-API-Key."
            value={String(config.api_key_header ?? "")}
            onChange={(v) => onChange(update(config, "api_key_header", v))}
            placeholder="X-API-Key"
            mono
          />
        )}
      </div>
    </ConfigGroup>
  );
}

function BasicAuthFields({ config, onChange }: ConfigFormProps) {
  return (
    <ConfigGroup title="HTTP Basic (RFC 7617)">
      <ConfigField
        label="Username"
        help="The userid. May contain any character except ':' (RFC 7617 §2)."
        value={String(config.username ?? "")}
        onChange={(v) => onChange(update(config, "username", v))}
        mono
      />
      <ConfigField
        label="Password"
        help="Encrypted at rest. Use [REDACTED] when re-saving without changing it. May be empty for legacy 'token in userid' patterns."
        value={String(config.password ?? "")}
        onChange={(v) => onChange(update(config, "password", v))}
        sensitive
      />
    </ConfigGroup>
  );
}

// ConnectPanel is the browser sign-in affordance for authorization_code. The
// Connect button needs a saved connection (the IdP redirect resolves the
// connection by name), so it states that requirement next to the disabled
// button rather than failing after the click.
function ConnectPanel({
  connectionName,
  isCreate,
}: {
  connectionName: string;
  isCreate: boolean;
}) {
  const startOAuth = useStartAPIGatewayOAuth();
  const [oauthError, setOAuthError] = useState<string | null>(null);
  const handleConnect = useCallback(() => {
    setOAuthError(null);
    if (!connectionName) {
      setOAuthError("Save the connection first, then click Connect.");
      return;
    }
    startOAuth.mutate(
      { name: connectionName, returnURL: window.location.pathname },
      {
        onSuccess: (resp) => {
          // Open the IdP authorization URL in a new tab so the
          // editor's unsaved fields survive the round-trip; the
          // callback handler redirects the new tab back to the
          // portal after persisting tokens.
          window.open(resp.authorization_url, "_blank", "noopener,noreferrer");
        },
        onError: (err) => {
          setOAuthError(err instanceof Error ? err.message : "Connect failed");
        },
      },
    );
  }, [connectionName, startOAuth]);

  return (
    <div className="space-y-2 rounded-md border bg-background px-3 py-3">
      <p className="text-xs">
        <strong>Connect</strong> opens the IdP sign-in page in a new tab. After the
        browser flow completes, the platform persists the refresh token (encrypted)
        so subsequent tool calls refresh access tokens silently.
      </p>
      <p className="text-xs text-muted-foreground">
        Save the connection first; Connect needs the connection registered before
        the IdP redirect can find it.
      </p>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={handleConnect}
        disabled={isCreate || startOAuth.isPending || !connectionName}
      >
        {startOAuth.isPending ? "Opening IdP…" : "Connect"}
      </Button>
      {oauthError && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertDescription>{oauthError}</AlertDescription>
        </Alert>
      )}
    </div>
  );
}

// The toolkit stores scopes as a string array but the field edits them as one
// space-delimited string; older rows may still hold a bare string.
function scopesValue(raw: unknown): string {
  if (Array.isArray(raw)) return (raw as string[]).join(" ");
  return String(raw ?? "");
}

// AuthCodeExtras is the tail of the authorization_code form: the OIDC prompt
// parameter and the browser sign-in panel, which only that grant uses.
function AuthCodeExtras({
  config,
  onChange,
  connectionName,
  isCreate,
}: ConfigFormProps & { connectionName: string; isCreate: boolean }) {
  return (
    <>
      <ConfigSelect
        label="OIDC prompt"
        value={String(config.oauth2_prompt ?? "")}
        onChange={(v) => onChange(update(config, "oauth2_prompt", v))}
        options={PROMPTS}
        help={
          <>
            Leave default for non-OIDC OAuth providers that reject unknown parameters.
            Use <code>login</code> for Keycloak / Auth0 / Okta to defeat stale-form bugs
            by forcing a fresh credential prompt on every Connect.
          </>
        }
      />
      <ConnectPanel connectionName={connectionName} isCreate={isCreate} />
    </>
  );
}

function OAuthFields({
  config,
  onChange,
  connectionName,
  isCreate,
}: ConfigFormProps & { connectionName: string; isCreate: boolean }) {
  const isAuthCode = config.auth_mode === "oauth2_authorization_code";
  return (
    <ConfigGroup
      title={`OAuth 2.1 — ${isAuthCode ? "authorization_code" : "client_credentials"}`}
    >
      <ConfigField
        label="Token URL"
        help="OAuth token endpoint."
        value={String(config.oauth2_token_url ?? "")}
        onChange={(v) => onChange(update(config, "oauth2_token_url", v))}
        placeholder="https://idp.example.com/oauth/token"
        mono
      />
      {isAuthCode && (
        <ConfigField
          label="Authorization URL"
          help="Where the browser is sent to sign in."
          value={String(config.oauth2_authorization_url ?? "")}
          onChange={(v) => onChange(update(config, "oauth2_authorization_url", v))}
          placeholder="https://idp.example.com/oauth/authorize"
          mono
        />
      )}
      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Client ID"
          value={String(config.oauth2_client_id ?? "")}
          onChange={(v) => onChange(update(config, "oauth2_client_id", v))}
          placeholder="platform-client"
          mono
        />
        <ConfigField
          label="Client Secret"
          help="Encrypted at rest. Use [REDACTED] to keep the existing value when re-saving."
          value={String(config.oauth2_client_secret ?? "")}
          onChange={(v) => onChange(update(config, "oauth2_client_secret", v))}
          sensitive
        />
      </div>
      <ConfigField
        label="Scopes"
        help="Space-delimited scope string. Leave empty if the IdP does not require it."
        value={scopesValue(config.oauth2_scopes)}
        onChange={(v) =>
          onChange(update(config, "oauth2_scopes", v.trim() ? v.split(/\s+/) : []))
        }
        placeholder="read:users write:orders"
        mono
      />
      <ConfigSelect
        label="Endpoint auth style"
        value={String(config.oauth2_endpoint_auth_style ?? "header")}
        onChange={(v) => onChange(update(config, "oauth2_endpoint_auth_style", v))}
        options={ENDPOINT_AUTH_STYLES}
      />
      {isAuthCode && (
        <AuthCodeExtras
          config={config}
          onChange={onChange}
          connectionName={connectionName}
          isCreate={isCreate}
        />
      )}
    </ConfigGroup>
  );
}

// ApiGatewayAuthFields renders the mode picker plus whichever credential
// block the selected mode needs. The mode set matches what the apigateway
// toolkit accepts (pkg/toolkits/apigateway/config.go); mtls carries no fields
// here because the certificate itself is the credential and lives in the TLS
// material editor.
export function ApiGatewayAuthFields({
  config,
  onChange,
  connectionName,
  isCreate,
  onOpenHelp,
}: ConfigFormProps & {
  connectionName: string;
  isCreate: boolean;
  onOpenHelp: () => void;
}) {
  const mode = String(config.auth_mode ?? "none");
  return (
    <>
      <ConfigSelect
        label="Auth mode"
        value={mode}
        onChange={(v) => onChange(update(config, "auth_mode", v))}
        options={AUTH_MODES}
        action={
          <Button type="button" variant="link" size="xs" onClick={onOpenHelp}>
            Learn about auth modes
          </Button>
        }
      />
      {mode === "bearer" && (
        <ConfigField
          label="Credential"
          help="Bearer token. Encrypted at rest. Use [REDACTED] when re-saving without changing it."
          value={String(config.credential ?? "")}
          onChange={(v) => onChange(update(config, "credential", v))}
          sensitive
        />
      )}
      {mode === "api_key" && <ApiKeyFields config={config} onChange={onChange} />}
      {mode === "basic" && <BasicAuthFields config={config} onChange={onChange} />}
      {(mode === "oauth2_client_credentials" ||
        mode === "oauth2_authorization_code") && (
        <OAuthFields
          config={config}
          onChange={onChange}
          connectionName={connectionName}
          isCreate={isCreate}
        />
      )}
    </>
  );
}
